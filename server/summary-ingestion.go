package server

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"sync"
)

// SummaryIngestionGate binds decoded TerminalSummary artifacts to an
// authenticated server participant and the orchestrator's prior admission.
// It is the crash-stop gate between a decoded spool artifact and result
// acceptance: nothing is accepted unless identity, revision, and generation
// all line up, and the same artifact can never be ingested twice.
type SummaryIngestionGate struct {
	// participants resolves a presented server credential into an
	// authenticated ServerParticipant.
	participants ServerParticipantResolver

	// admissionFor resolves the orchestrator's prior admission for one
	// account on one process generation, or nil when the account was never
	// admitted.
	admissionFor AdmissionLookup

	// accept delivers the decoded summary to the orchestrator's result
	// acceptance path (AcceptResult). Acceptance is the single store
	// operation: a failed accept leaves no trace.
	accept SummaryAccept

	// seenArtifact records artifact content digests already ingested, so a
	// re-delivered or replayed artifact is refused even if its file name
	// differs.
	seenArtifacts map[string]bool

	mtx sync.Mutex
}

// ServerParticipantResolver resolves a presented server credential into an
// authenticated ServerParticipant, mirroring the orchestrator's
// ServerAuthority seam.
type ServerParticipantResolver interface {
	// ResolveParticipant derives the authenticated server participant for
	// one presented process credential, or returns an authentication error.
	ResolveParticipant(credential string) (*ServerParticipant, error)
}

// AdmissionLookup resolves the orchestrator's prior admission for one
// account on one process generation, or nil when the account was never
// admitted.
type AdmissionLookup func(accountID string, processGeneration uint64) *AdmissionRecord

// SummaryAccept delivers one accepted summary to the orchestrator's result
// acceptance path. Acceptance is the single store operation: a failed
// accept leaves no trace.
type SummaryAccept func(summary *TerminalSummary) error

// AdmissionRecord is the prior admission fact recorded at join-intent
// consumption.
type AdmissionRecord struct {
	// AccountID is the admitted account.
	AccountID string
	// RevisionID is the admitted revision.
	RevisionID string
	// ProcessGeneration is the admitted process generation.
	ProcessGeneration uint64
}

// NewSummaryIngestionGate constructs an ingestion gate over the injected
// participant resolver, admission lookup, and acceptance callback.
func NewSummaryIngestionGate(
	participants ServerParticipantResolver,
	admissionFor AdmissionLookup,
	accept SummaryAccept,
) (*SummaryIngestionGate, error) {
	if participants == nil {
		return nil, fmt.Errorf("%w: participant resolver is required", ErrInvalidSpec)
	}
	if admissionFor == nil {
		return nil, fmt.Errorf("%w: admission lookup is required", ErrInvalidSpec)
	}
	if accept == nil {
		return nil, fmt.Errorf("%w: accept callback is required", ErrInvalidSpec)
	}
	return &SummaryIngestionGate{
		participants:  participants,
		admissionFor:  admissionFor,
		accept:        accept,
		seenArtifacts: make(map[string]bool),
	}, nil
}

// IngestRequest is one authenticated ingestion request for a spool artifact.
type IngestRequest struct {
	// Credential is the presented server process credential. The participant
	// is derived from it; generation is never accepted from the payload.
	Credential string
	// ArtifactName is the spool artifact name.
	ArtifactName string
	// Data is the raw artifact payload.
	Data []byte
	// AccountID is the account the result belongs to. It must match a prior
	// admission on the participant's process generation.
	AccountID string
	// RevisionID is the revision claimed for the attempt. It must match the
	// prior admission and the decoded summary.
	RevisionID string
	// AttemptGeneration is the attempt generation used to validate the
	// artifact name. It must match the decoded summary's attempt generation.
	AttemptGeneration uint64
}

// Ingest authenticates the server participant, decodes the artifact, binds
// the decoded identity to the participant and the prior admission, and
// accepts the result. Crash-stop semantics: on any refusal, nothing is
// accepted, nothing is recorded, and no partial state remains.
func (g *SummaryIngestionGate) Ingest(req IngestRequest) error {
	// 1. Authenticate the server participant from the credential. The
	//    process generation is derived from the credential, never from the
	//    payload.
	participant, err := g.participants.ResolveParticipant(req.Credential)
	if err != nil || participant == nil {
		return fmt.Errorf("%w: participant authentication failed", ErrUnauthenticated)
	}

	// 2. Refuse traversal and unknown artifact names before touching the
	//    payload.
	if err := ValidateArtifactName(req.ArtifactName, req.AttemptGeneration); err != nil {
		return err
	}

	// 3. Refuse oversize artifacts before parsing.
	if err := CheckArtifactSize(len(req.Data)); err != nil {
		return err
	}

	// 4. Refuse duplicate artifacts by content digest, regardless of file
	//    name.
	digest := artifactDigest(req.Data)
	g.mtx.Lock()
	if g.seenArtifacts[digest] {
		g.mtx.Unlock()
		return ErrSummaryDuplicate
	}
	g.mtx.Unlock()

	// 5. Decode strictly. A malformed artifact is crash-stop: nothing is
	//    recorded and nothing is accepted.
	summary, err := ParseTerminalSummaryArtifact(req.Data)
	if err != nil {
		return err
	}

	// 6. Bind the decoded identity to the authenticated participant: the
	//    participant cannot submit summaries for another process
	//    generation. The artifact filename's generation must also agree
	//    with the payload, closing the name/payload split.
	if summary.ServerProcessGeneration != participant.ProcessGeneration {
		return ErrSummaryIdentityMismatch
	}
	if summary.AttemptGeneration != req.AttemptGeneration {
		return ErrSummaryIdentityMismatch
	}

	// 7. Bind the decoded identity to the prior admission: the account must
	//    have been admitted on this process generation against this
	//    revision.
	adm := g.admissionFor(req.AccountID, participant.ProcessGeneration)
	if adm == nil {
		return ErrSummaryIdentityMismatch
	}
	if adm.RevisionID != summary.Revision {
		return ErrSummaryIdentityMismatch
	}
	if req.RevisionID != "" && req.RevisionID != summary.Revision {
		return ErrSummaryIdentityMismatch
	}

	// 8. Reserve the artifact digest before acceptance so concurrent
	//    ingestion of the same artifact yields exactly one acceptance.
	g.mtx.Lock()
	if g.seenArtifacts[digest] {
		g.mtx.Unlock()
		return ErrSummaryDuplicate
	}
	g.seenArtifacts[digest] = true
	g.mtx.Unlock()

	// 9. Accept: the single store operation. A failed accept rolls back the
	//    digest reservation so a retry is possible and no trace remains.
	if err := g.accept(summary); err != nil {
		g.mtx.Lock()
		delete(g.seenArtifacts, digest)
		g.mtx.Unlock()
		return err
	}
	return nil
}

// artifactDigest returns the hex sha256 digest of the artifact payload.
func artifactDigest(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

// ServerParticipant mirrors the orchestrator's authenticated server
// participant within the server package so ingestion can bind decoded
// identity to the authenticated participant without an import cycle.
type ServerParticipant struct {
	// ID is the durable server participant identifier.
	ID string
	// ProcessGeneration is the generation this instance serves.
	ProcessGeneration uint64
}
