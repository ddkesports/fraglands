package server

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"sync"

	"github.com/paralin/fraglands/core"
)

// SummaryIngestionGate is the crash-stop gate between a decoded spool
// artifact and result acceptance. It authenticates the server participant,
// decodes the artifact under the versioned contract, binds the decoded
// identity to the authenticated participant, and hands the summary to the
// accept seam exactly once. It owns identity binding and duplicate
// rejection only: account attribution and admission fencing live in the
// composition layer and the orchestrator, never here.
type SummaryIngestionGate struct {
	// participants resolves a presented server credential into an
	// authenticated server participant. The generation is derived from the
	// credential, never from the payload.
	participants ServerParticipantResolver

	// accept delivers the bound summary to the result acceptance path.
	// Acceptance is the single store operation: a failed accept leaves no
	// trace.
	accept SummaryAccept
	// acceptCredential is used by compositions whose acceptance callback must
	// run inside the exact credential lease transaction.
	acceptCredential SummaryAcceptCredential

	// seenArtifacts records artifact content digests already ingested, so a
	// re-delivered or replayed artifact is refused even if its file name
	// differs.
	seenArtifacts map[string]bool

	mtx sync.Mutex
}

// ServerParticipantResolver resolves a presented server credential into an
// authenticated server participant.
type ServerParticipantResolver interface {
	// ResolveParticipant derives the server participant for one presented
	// process credential, or returns an authentication error.
	ResolveParticipant(ctx context.Context, credential string) (*core.ServerParticipant, error)
}

// ServerCredentialCommitter is an optional capability paired with a
// participant resolver. Implementations revalidate the exact credential and
// lease version in the same transaction as the acceptance commit. Resolvers
// without this capability retain the existing generation-gated accept path.
type ServerCredentialCommitter interface {
	CommitCredential(credential string, generation uint64, commit func() error) error
}

// SummaryAccept delivers one accepted summary, bound to its authenticated
// server participant, to the result acceptance path. Acceptance is the
// single store operation: a failed accept leaves no trace.
type SummaryAccept func(participant *core.ServerParticipant, summary *TerminalSummary) error

// SummaryAcceptCredential is the credential-aware acceptance seam. It is
// invoked only inside the resolver's exact credential commit transaction.
type SummaryAcceptCredential func(credential string, participant *core.ServerParticipant, summary *TerminalSummary) error

// NewSummaryIngestionGate constructs an ingestion gate over the injected
// participant resolver and acceptance callback.
func NewSummaryIngestionGate(
	participants ServerParticipantResolver,
	accept SummaryAccept,
) (*SummaryIngestionGate, error) {
	if participants == nil {
		return nil, fmt.Errorf("%w: participant resolver is required", ErrInvalidSpec)
	}
	if accept == nil {
		return nil, fmt.Errorf("%w: accept callback is required", ErrInvalidSpec)
	}
	return &SummaryIngestionGate{
		participants:  participants,
		accept:        accept,
		seenArtifacts: make(map[string]bool),
	}, nil
}

// NewSummaryIngestionGateWithCredentialCommit constructs a gate whose
// credential-aware callback runs inside the exact lease commit transaction.
// This avoids nesting the generation acceptance gate around the callback.
func NewSummaryIngestionGateWithCredentialCommit(
	participants ServerParticipantResolver,
	accept SummaryAcceptCredential,
) (*SummaryIngestionGate, error) {
	if accept == nil {
		return nil, fmt.Errorf("%w: credential accept callback is required", ErrInvalidSpec)
	}
	gate, err := NewSummaryIngestionGate(participants, func(*core.ServerParticipant, *TerminalSummary) error {
		return fmt.Errorf("%w: credential-aware callback required", ErrInvalidSpec)
	})
	if err != nil {
		return nil, err
	}
	gate.acceptCredential = accept
	return gate, nil
}

// IngestRequest is one authenticated ingestion request for a spool artifact.
// It carries only the credential and the artifact: no account, revision, or
// generation identity is accepted from the caller.
type IngestRequest struct {
	// Credential is the presented server process credential.
	Credential string
	// ArtifactName is the spool artifact name.
	ArtifactName string
	// Data is the raw artifact payload.
	Data []byte
}

// Ingest authenticates the server participant, decodes the artifact, binds
// the decoded identity to the participant, and accepts the result exactly
// once. Crash-stop semantics: on any refusal, nothing is accepted, nothing
// is recorded, and no partial state remains.
func (g *SummaryIngestionGate) Ingest(req IngestRequest) error {
	// 1. Authenticate the server participant from the credential. The
	// resolver's typed refusal is preserved so callers can distinguish a
	// revoked lease from an unknown credential.
	participant, err := g.participants.ResolveParticipant(context.Background(), req.Credential)
	if err != nil || participant == nil {
		if err == nil {
			return fmt.Errorf("%w: participant authentication failed", ErrUnauthenticated)
		}
		return fmt.Errorf("%w: participant authentication failed: %w", ErrUnauthenticated, err)
	}

	// 2. Refuse oversize artifacts before parsing.
	if err := CheckArtifactSize(len(req.Data)); err != nil {
		return err
	}

	// 3. Refuse duplicate artifacts by content digest, regardless of file
	//    name.
	digest := artifactDigest(req.Data)
	if g.seen(digest) {
		return ErrSummaryDuplicate
	}

	// 4. Decode strictly. A malformed artifact is crash-stop: nothing is
	//    recorded and nothing is accepted.
	summary, err := ParseTerminalSummaryArtifact(req.Data)
	if err != nil {
		return err
	}

	// 5. Bind the artifact name to the decoded payload: the name must be
	//    the writer's exact name for the decoded attempt generation. This
	//    closes the name/payload split and refuses traversal names.
	if err := ValidateArtifactName(req.ArtifactName, summary.AttemptGeneration); err != nil {
		return err
	}

	// 6. Bind the decoded identity to the authenticated participant: the
	//    participant cannot submit summaries for another process
	//    generation.
	if summary.ServerProcessGeneration != participant.ProcessGeneration {
		return ErrSummaryIdentityMismatch
	}

	// 7. Reserve the artifact digest before acceptance so concurrent
	//    ingestion of the same artifact yields exactly one acceptance.
	g.mtx.Lock()
	if g.seenArtifacts[digest] {
		g.mtx.Unlock()
		return ErrSummaryDuplicate
	}
	g.seenArtifacts[digest] = true
	g.mtx.Unlock()

	// 8. Accept: the single store operation. Lease-aware resolvers revalidate
	//    the exact credential and its lease version around this commit;
	//    otherwise the composed authority's generation gate remains the
	//    authority boundary.
	commit := func() error {
		if g.acceptCredential != nil {
			return g.acceptCredential(req.Credential, participant, summary)
		}
		return g.accept(participant, summary)
	}
	var acceptErr error
	if committer, ok := g.participants.(ServerCredentialCommitter); ok {
		acceptErr = committer.CommitCredential(req.Credential, participant.ProcessGeneration, commit)
	} else {
		acceptErr = commit()
	}
	if acceptErr != nil {
		g.mtx.Lock()
		delete(g.seenArtifacts, digest)
		g.mtx.Unlock()
		return acceptErr
	}
	return nil
}

// seen reports whether the digest was already ingested.
func (g *SummaryIngestionGate) seen(digest string) bool {
	g.mtx.Lock()
	defer g.mtx.Unlock()
	return g.seenArtifacts[digest]
}

// artifactDigest returns the hex sha256 digest of the artifact payload.
func artifactDigest(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}
