// Package app composes the server ingestion gate with the orchestrator at
// the outer application boundary. It is the only place where the two are
// wired together: the orchestrator authenticates server credentials and owns
// admission state and result acceptance; the gate decodes spool artifacts
// and binds them to the authenticated participant. The adapter adds no
// rules of its own and copies no identity types.
package app

import (
	"context"
	"fmt"

	"github.com/paralin/fraglands/core"
	"github.com/paralin/fraglands/orchestrator"
	"github.com/paralin/fraglands/server"
)

// ErrUnauthenticated is returned when the orchestrator refuses the
// presented server credential.
var ErrUnauthenticated = fmt.Errorf("app: unauthenticated server participant")

// serverGateAdapter adapts the orchestrator into the gate's resolver seam.
// Credential authentication runs through the orchestrator's real
// ServerAuthority; the participant is derived from the credential, never
// from the payload.
type serverGateAdapter struct {
	orch *orchestrator.Orchestrator
}

// ResolveParticipant authenticates the presented credential through the
// orchestrator's ServerAuthority.
func (a serverGateAdapter) ResolveParticipant(ctx context.Context, credential string) (*core.ServerParticipant, error) {
	participant, err := a.orch.AuthenticateServer(ctx, credential)
	if err != nil {
		return nil, err
	}
	return participant, nil
}

// CommitCredential preserves the outer application's authority boundary while
// allowing the gate to revalidate the exact bearer lease at commit time.
func (a serverGateAdapter) CommitCredential(credential string, generation uint64, commit func() error) error {
	return a.orch.CommitServerCredential(credential, generation, commit)
}

// NewServerIngestionGate composes a SummaryIngestionGate with the
// orchestrator at the outer application boundary. Credential authentication
// runs through the orchestrator's real ServerAuthority, and the decoded
// summary is delivered to the orchestrator's result acceptance path with the
// authenticated participant. No account identity is accepted from callers.
func NewServerIngestionGate(orch *orchestrator.Orchestrator) (*server.SummaryIngestionGate, error) {
	return newServerIngestionGate(orch)
}

// NewServerIngestionGateWithLeases composes the gate over an orchestrator
// whose server authority is a generation-scoped server lease authority. The
// AcceptResult commit is gated on the live lease of the artifact's process
// generation inside the orchestrator: the liveness check and the commit run
// under one lock, so a terminal revocation is linearizable with result
// acceptance. A revoked generation can never commit a result, and a
// committed result always stands. The lease authority itself is injected
// into the orchestrator; this constructor validates the wiring and refuses
// a nil authority rather than composing a gate that cannot fence commits.
func NewServerIngestionGateWithLeases(
	orch *orchestrator.Orchestrator,
	leases *core.ServerLeaseAuthority,
) (*server.SummaryIngestionGate, error) {
	if leases == nil {
		return nil, fmt.Errorf("%w: lease authority is required", server.ErrInvalidSpec)
	}
	return newServerIngestionGate(orch)
}

// newServerIngestionGate builds the composed gate. Lease gating of the
// acceptance commit lives in the orchestrator's AcceptResult, the single
// linearization point shared by every result path.
func newServerIngestionGate(orch *orchestrator.Orchestrator) (*server.SummaryIngestionGate, error) {
	if orch == nil {
		return nil, fmt.Errorf("%w: orchestrator is required", server.ErrInvalidSpec)
	}
	return server.NewSummaryIngestionGateWithCredentialCommit(serverGateAdapter{orch: orch},
		func(_ string, participant *core.ServerParticipant, summary *server.TerminalSummary) error {
			// The summary carries no account: the account of the
			// result is a fact of the admission, not of the
			// artifact. The orchestrator attributes it.
			accountID, err := orch.AdmittedAccountFor(summary.ServerProcessGeneration, summary.Revision)
			if err != nil {
				return err
			}
			return orch.AcceptResultAfterLease(participant, &core.AttemptResult{
				AccountID:         accountID,
				RevisionID:        summary.Revision,
				ProcessGeneration: summary.ServerProcessGeneration,
				AttemptGeneration: summary.AttemptGeneration,
				ReplayID:          summary.ReplayIdentity,
				TakeoverTick:      summary.TakeoverTick,
			})
		})
}

// The lease authority matches the orchestrator ServerAuthority shape, so a
// deployment can wire one concrete authority as both the server credential
// source and the revocation gate.
var _ orchestrator.ServerAuthority = (*core.ServerLeaseAuthority)(nil)
