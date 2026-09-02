package orchestrator

import (
	"context"

	"github.com/paralin/fraglands/core"
)

// ProcessAllocator starts one server process for one ready revision. The
// orchestrator depends on this interface only: a real worker implementation
// replaces the in-memory one without touching the coordinated path.
type ProcessAllocator interface {
	// Allocate starts one server process generation for the revision and
	// returns it before its readiness is proven. The returned process
	// carries the generation and connect address; readiness is recorded
	// separately on the process as explicit evidence.
	Allocate(ctx context.Context, revision *core.ScenarioRevision) (*AllocatedProcess, error)
}
