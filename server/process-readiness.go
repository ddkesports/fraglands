package server

import "context"

// ProcessReadinessWaiter is the optional readiness-wait seam of a supervised
// process. The Process contract records readiness as an explicit fact but
// does not itself provide a wait: a real launcher exposes one through this
// interface, and a caller type-asserts the handle it launched. Unrelated
// Process implementations are never forced to provide it.
//
// A WaitReadiness implementation must be race-safe against both the
// readiness recording and a terminal transition: it cannot miss either, and
// it must wake every waiter on whichever happens first.
type ProcessReadinessWaiter interface {
	// WaitReadiness waits until the readiness fact is recorded or the
	// process reaches a terminal state. It returns the recorded fact when
	// readiness is proven, ctx.Err() when the context ends first, and
	// ErrNotReady when the process became terminal without readiness. The
	// crash truth of a terminal process stays on the process itself.
	WaitReadiness(ctx context.Context) (ReadinessFact, error)
}
