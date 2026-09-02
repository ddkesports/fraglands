package orchestrator

import (
	"context"

	"github.com/paralin/fraglands/core"
)

// ProcessAllocator starts one server process for one ready revision. The
// orchestrator depends on this interface only: a real worker implementation
// replaces the in-memory one without touching the coordinated path.
type ProcessAllocator interface {
	// Allocate starts one server process generation for the revision. It
	// may block until readiness is proven, in which case the returned
	// process carries the generation, connect address, and readiness
	// evidence; or it may return the process before readiness is proven,
	// in which case readiness is recorded separately on the process as
	// explicit evidence. It must not return a process marked ready
	// without evidence, and it must not return a process that has
	// already become terminal.
	Allocate(ctx context.Context, revision *core.ScenarioRevision) (*AllocatedProcess, error)
}
