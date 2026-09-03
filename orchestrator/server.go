package orchestrator

import (
	"context"

	"github.com/paralin/fraglands/core"
)

// ServerParticipant is one authenticated server process instance. The
// authoritative definition lives in core so the server ingestion gate binds
// decoded artifacts to the same identity the orchestrator authenticates:
// this alias keeps one type, not a copy.
type ServerParticipant = core.ServerParticipant

// ServerAuthority authenticates server process credentials and derives the
// bound process generation. It is injected and owned by the server: the
// generation is never accepted from request payloads.
type ServerAuthority interface {
	// AuthenticateServer derives the server participant for one presented
	// process credential, or returns an authentication error.
	AuthenticateServer(ctx context.Context, credential string) (*ServerParticipant, error)
}

// AuthenticateServer derives the server participant for one presented
// process credential.
func (o *Orchestrator) AuthenticateServer(ctx context.Context, credential string) (*ServerParticipant, error) {
	return o.servers.AuthenticateServer(ctx, credential)
}

// LeaseCommitter is the optional capability of a ServerAuthority that gates
// result acceptance on the live lease of the process generation. A
// generation-scoped credential authority implements it so terminal
// revocation is linearizable with the AcceptResult commit: the liveness
// check and the commit run under one lock, so a revoked generation can
// never commit a result, and a committed result always stands.
type LeaseCommitter interface {
	// CommitLease executes commit iff the lease for the process generation
	// is currently valid. The validity check and the commit run under one
	// lock. A refused commit never runs and leaves no trace.
	CommitLease(processGeneration uint64, commit func() error) error
}

// CredentialLeaseCommitter is the stronger optional authority capability. It
// revalidates the presented credential (including its lease version) in the
// same transaction as the commit.
type CredentialLeaseCommitter interface {
	CommitCredential(credential string, processGeneration uint64, commit func() error) error
}

// CommitServerCredential runs a result commit under the exact presented
// server credential when the authority supports lease credentials. Older
// authorities fall back to generation fencing.
func (o *Orchestrator) CommitServerCredential(credential string, processGeneration uint64, commit func() error) error {
	if committer, ok := o.servers.(CredentialLeaseCommitter); ok {
		return committer.CommitCredential(credential, processGeneration, commit)
	}
	if committer, ok := o.servers.(LeaseCommitter); ok {
		return committer.CommitLease(processGeneration, commit)
	}
	return commit()
}

// leaseCommitter returns the authority's lease-commit capability, or nil.
func (o *Orchestrator) leaseCommitter() LeaseCommitter {
	if committer, ok := o.servers.(LeaseCommitter); ok {
		return committer
	}
	return nil
}
