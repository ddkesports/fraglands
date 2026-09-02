package server

import "context"

// ProcessLauncher launches one server process generation. The supervisor
// depends on this interface only: a real host implementation replaces the
// fake one without touching the supervision path.
type ProcessLauncher interface {
	// Launch starts one server process for the spec and returns it in the
	// Launching state. Readiness is proven separately by the worker.
	Launch(ctx context.Context, spec ProcessSpec) (Process, error)
}
