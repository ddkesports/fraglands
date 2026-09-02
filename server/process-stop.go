package server

import "context"

// ProcessStopper is the optional stop seam of a launched process. The
// Process contract records terminal states but does not itself request a
// stop: a real launcher exposes deliberate teardown through this interface,
// and the operator type-asserts the handle it launched. A Stop must tear the
// whole process down (process group or job where the platform permits), must
// be safe for concurrent use, and must leave the process in exactly one
// terminal state. It never restarts the process.
type ProcessStopper interface {
	// Stop tears the process down deliberately and waits until it reaches a
	// terminal state, the teardown deadline expires, or the context is
	// cancelled. A process already in a terminal state refuses with a typed
	// error (ErrAlreadyStopped or ErrCrashed).
	Stop(ctx context.Context) error
}
