package orchestrator

import (
	"context"

	"github.com/paralin/fraglands/core"
)

// Preparer runs one accepted ScenarioPreparation to a terminal state: ready
// with an immutable revision, or failed with one typed reason. The
// orchestrator calls it on its own goroutine; the reconstruction provider
// implements it and owns every game-state decision.
type Preparer interface {
	// Prepare moves the preparation to a terminal state. It must transition
	// the preparation exactly once and never leave partial state.
	Prepare(ctx context.Context, prep *core.ScenarioPreparation)
}
