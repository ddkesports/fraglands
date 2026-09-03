package core

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"sync"
)

// This file defines the generation-scoped server credential lease: the
// authority mints one credential per server process generation as a lease
// carrying a monotonically increasing version. Re-issuing a lease for a
// generation supersedes the previous credential, and revoking the lease at
// the process terminal state invalidates the credential. Terminal
// revocation is linearizable with result acceptance: CommitLease runs the
// acceptance commit inside the lease's critical section, so a revoked
// generation can never commit a result, and a committed result always
// stands.

// Typed lease refusals. Callers match with errors.Is.
var (
	// ErrLeaseUnknown is returned when a credential was never issued or is
	// no longer the active lease for its generation.
	ErrLeaseUnknown = errors.New("core: server lease unknown")
	// ErrLeaseRevoked is returned when the lease was revoked at the server
	// process terminal state.
	ErrLeaseRevoked = errors.New("core: server lease revoked")
	// ErrLeaseInvalidBinding is returned when a lease is requested without
	// a participant identity or a non-zero process generation.
	ErrLeaseInvalidBinding = errors.New("core: server lease binding incomplete")
	// ErrLeaseCommitRequired is returned when CommitLease is called
	// without a commit closure.
	ErrLeaseCommitRequired = errors.New("core: server lease commit closure is required")
	// ErrLeaseRandom is returned when the credential token cannot be
	// generated.
	ErrLeaseRandom = errors.New("core: server lease credential generation failed")
)

// ServerLease is one issued generation-scoped server credential. The
// credential is a bearer secret: it is readable only through Credential and
// is never serialized by the authority.
type ServerLease struct {
	// participantID is the durable server participant identity the
	// credential resolves to.
	participantID string
	// generation is the server process generation the lease is scoped to.
	generation uint64
	// version is the monotonic lease version for the generation: 1 on the
	// first issue, one more than the previous lease on every re-issue.
	version uint64
	// credential is the opaque bearer credential presented by the server
	// process.
	credential string
}

// ParticipantID returns the server participant identity the lease resolves to.
func (l *ServerLease) ParticipantID() string {
	if l == nil {
		return ""
	}
	return l.participantID
}

// Generation returns the server process generation the lease is scoped to.
func (l *ServerLease) Generation() uint64 {
	if l == nil {
		return 0
	}
	return l.generation
}

// Version returns the monotonic lease version for the generation.
func (l *ServerLease) Version() uint64 {
	if l == nil {
		return 0
	}
	return l.version
}

// Credential returns the opaque bearer credential presented by the server
// process. The only intended consumer is the deployment that delivers the
// credential to the process it was minted for.
func (l *ServerLease) Credential() string {
	if l == nil {
		return ""
	}
	return l.credential
}

// String redacts the lease: log lines can never carry the credential.
func (l *ServerLease) String() string {
	return "server-lease(redacted)"
}

// ServerLeaseAuthority is the in-memory generation-scoped server credential
// authority. It mints one credential per server process generation as a
// versioned lease, authenticates presented credentials into server
// participants, revokes leases at the process terminal state, and gates
// result acceptance commits on lease liveness.
//
// It satisfies the orchestrator.ServerAuthority shape: a deployment can wire
// one authority as both the server credential source and the revocation gate
// for result acceptance. It is safe for concurrent use.
type ServerLeaseAuthority struct {
	// mtx guards the maps below and serializes revocation with the
	// commit gate: CommitLease and Revoke are linearizable.
	mtx sync.Mutex
	// active maps generation to its active lease record. A generation has
	// at most one active lease at a time.
	active map[uint64]*serverLeaseRecord
	// credentials maps a presented credential to its record.
	credentials map[string]*serverLeaseRecord
}

// serverLeaseRecord is the minted state of one lease.
type serverLeaseRecord struct {
	participantID string
	generation    uint64
	version       uint64
	revoked       bool
}

// NewServerLeaseAuthority constructs an empty lease authority.
func NewServerLeaseAuthority() *ServerLeaseAuthority {
	return &ServerLeaseAuthority{
		active:      make(map[uint64]*serverLeaseRecord),
		credentials: make(map[string]*serverLeaseRecord),
	}
}

// IssueLease mints the credential for one server process generation. The
// version is one more than the previously issued lease for the generation,
// so a stale credential is always refused against the active version.
// Issuing over a live lease supersedes it: the previous credential stops
// authenticating immediately.
func (a *ServerLeaseAuthority) IssueLease(participantID string, generation uint64) (*ServerLease, error) {
	if participantID == "" || generation == 0 {
		return nil, ErrLeaseInvalidBinding
	}

	a.mtx.Lock()
	defer a.mtx.Unlock()

	credential, err := newLeaseCredential(a.credentials)
	if err != nil {
		return nil, err
	}
	// Version is one more than the previous lease for this generation: the
	// first lease for a generation is version 1, and every re-issue after
	// revocation bumps the version. Stale credentials can never be mistaken
	// for the active lease.
	prev := a.active[generation]
	version := uint64(1)
	if prev != nil {
		version = prev.version + 1
	}
	record := &serverLeaseRecord{
		participantID: participantID,
		generation:    generation,
		version:       version,
	}
	a.credentials[credential] = record
	a.active[generation] = record
	return &ServerLease{
		participantID: participantID,
		generation:    generation,
		version:       record.version,
		credential:    credential,
	}, nil
}

// AuthenticateServer derives the server participant for one presented
// credential, or returns a typed refusal: unknown (including superseded
// credentials) or revoked. A revoked or superseded credential never
// authenticates.
func (a *ServerLeaseAuthority) AuthenticateServer(ctx context.Context, credential string) (*ServerParticipant, error) {
	a.mtx.Lock()
	defer a.mtx.Unlock()
	record, ok := a.credentials[credential]
	if !ok {
		return nil, ErrLeaseUnknown
	}
	if record.revoked {
		return nil, ErrLeaseRevoked
	}
	if a.active[record.generation] != record {
		return nil, ErrLeaseUnknown
	}
	return &ServerParticipant{ID: record.participantID, ProcessGeneration: record.generation}, nil
}

// Revoke revokes the active lease for the generation at the server process
// terminal state. It is idempotent: revoking a generation with no active
// lease changes nothing. Once Revoke returns, the credential can no longer
// authenticate and no CommitLease for the generation can succeed.
func (a *ServerLeaseAuthority) Revoke(generation uint64) error {
	a.mtx.Lock()
	defer a.mtx.Unlock()
	record := a.active[generation]
	if record == nil {
		return nil
	}
	record.revoked = true
	return nil
}

// CommitLease executes commit iff the lease for the process generation is
// currently valid. The validity check and the commit run under one lock, so
// revocation is linearizable with the commit: a Revoke that takes the lock
// first refuses every later commit, and a commit that takes the lock first
// has landed and stands. A refused commit never runs and leaves no trace. A
// failing commit leaves the lease valid so the acceptance can be retried.
func (a *ServerLeaseAuthority) CommitLease(generation uint64, commit func() error) error {
	if commit == nil {
		return ErrLeaseCommitRequired
	}
	a.mtx.Lock()
	defer a.mtx.Unlock()
	record := a.active[generation]
	if record == nil {
		return ErrLeaseUnknown
	}
	if record.revoked {
		return ErrLeaseRevoked
	}
	return commit()
}

// CommitCredential executes commit iff the presented credential is the
// currently active, unrevoked lease for generation. The credential identity
// and commit are checked under one lock, so re-issue and terminal revocation
// cannot allow an operation authenticated by an older lease to commit.
func (a *ServerLeaseAuthority) CommitCredential(credential string, generation uint64, commit func() error) error {
	if commit == nil {
		return ErrLeaseCommitRequired
	}
	a.mtx.Lock()
	defer a.mtx.Unlock()
	record, ok := a.credentials[credential]
	if !ok || record.generation != generation {
		return ErrLeaseUnknown
	}
	if record.revoked {
		return ErrLeaseRevoked
	}
	if a.active[generation] != record {
		return ErrLeaseUnknown
	}
	return commit()
}

// newLeaseCredential generates a fresh opaque credential token. Callers
// must hold the authority lock.
func newLeaseCredential(taken map[string]*serverLeaseRecord) (string, error) {
	for {
		raw := make([]byte, 32)
		if _, err := rand.Read(raw); err != nil {
			return "", ErrLeaseRandom
		}
		credential := base64.RawURLEncoding.EncodeToString(raw)
		if _, exists := taken[credential]; !exists {
			return credential, nil
		}
	}
}
