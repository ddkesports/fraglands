package provider

import (
	"context"

	"github.com/paralin/fraglands/core"
	"github.com/paralin/fraglands/orchestrator"
)

// Preparer adapts the reconstruction provider to the orchestrator.Preparer
// seam. The provider already owns the preparation lifecycle: it moves the
// preparation exactly once to ready-with-revision or failed-with-typed-
// reason and never leaves partial state, so the adapter is a pure signature
// bridge and adds no rules of its own. A preparation already in a terminal
// state is refused by the provider itself; the adapter never overwrites a
// terminal outcome.
type Preparer struct {
	// provider is the reconstruction provider that owns the preparation
	// state machine.
	provider *Provider
}

// Compile-time contract assertion.
var _ orchestrator.Preparer = (*Preparer)(nil)

// NewPreparer wraps a reconstruction provider as an orchestrator Preparer.
func NewPreparer(p *Provider) (*Preparer, error) {
	if p == nil {
		return nil, ErrNilProvider
	}
	return &Preparer{provider: p}, nil
}

// Prepare runs the provider's preparation path to its provider-owned
// terminal state. Any refusal is already recorded on the preparation as one
// typed reason; the returned error is diagnostic and deliberately dropped,
// because the orchestrator reads the outcome from the preparation itself.
func (a *Preparer) Prepare(ctx context.Context, prep *core.ScenarioPreparation) {
	_ = a.provider.Prepare(ctx, prep)
}
