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

// NewServerIngestionGate composes a SummaryIngestionGate with the
// orchestrator at the outer application boundary. Credential authentication
// runs through the orchestrator's real ServerAuthority, and the decoded
// summary is delivered to the orchestrator's result acceptance path with the
// authenticated participant. No account identity is accepted from callers.
func NewServerIngestionGate(orch *orchestrator.Orchestrator) (*server.SummaryIngestionGate, error) {
	if orch == nil {
		return nil, fmt.Errorf("%w: orchestrator is required", server.ErrInvalidSpec)
	}
	return server.NewSummaryIngestionGate(
		serverGateAdapter{orch: orch},
		func(participant *core.ServerParticipant, summary *server.TerminalSummary) error {
			// The summary carries no account: the account of the
			// result is a fact of the admission, not of the
			// artifact. The orchestrator attributes it.
			accountID, err := orch.AdmittedAccountFor(summary.ServerProcessGeneration, summary.Revision)
			if err != nil {
				return err
			}
			return orch.AcceptResult(participant, &core.AttemptResult{
				AccountID:         accountID,
				RevisionID:        summary.Revision,
				ProcessGeneration: summary.ServerProcessGeneration,
				AttemptGeneration: summary.AttemptGeneration,
				ReplayID:          summary.ReplayIdentity,
				TakeoverTick:      summary.TakeoverTick,
			})
		},
	)
}
