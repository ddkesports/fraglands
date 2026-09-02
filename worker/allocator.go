// Package worker implements the orchestrator's ProcessAllocator seam over
// the server Supervisor: one server process generation per ready revision,
// readiness proven with explicit evidence before the process is reported as
// usable. This is the only place where the two sides meet; no shared
// identity types are copied.
package worker

import (
	"context"
	"fmt"
	"time"

	"github.com/paralin/fraglands/core"
	"github.com/paralin/fraglands/orchestrator"
	"github.com/paralin/fraglands/server"
)

// Allocator allocates one server process generation per ready revision
// through a Supervisor. The supervisor owns the process lifecycle; the
// allocator proves readiness with explicit evidence before reporting the
// process as usable.
type Allocator struct {
	// supervisor launches and supervises the server processes.
	supervisor *server.Supervisor

	// readinessTimeout bounds the wait for explicit readiness evidence.
	// Zero means DefaultReadinessTimeout.
	readinessTimeout time.Duration
}

// Compile-time contract assertion.
var _ orchestrator.ProcessAllocator = (*Allocator)(nil)

// DefaultReadinessTimeout is the readiness wait applied when the configured
// timeout is zero.
const DefaultReadinessTimeout = 2 * time.Minute

// AllocatorOption configures an Allocator.
type AllocatorOption func(*Allocator)

// WithReadinessTimeout bounds the wait for readiness evidence. A process
// that stays unready past the bound is stopped and reported as a typed
// allocation failure.
func WithReadinessTimeout(d time.Duration) AllocatorOption {
	return func(a *Allocator) {
		a.readinessTimeout = d
	}
}

// NewAllocator constructs an allocator over a supervisor. Options may bound
// the readiness wait.
func NewAllocator(supervisor *server.Supervisor, opts ...AllocatorOption) (*Allocator, error) {
	if supervisor == nil {
		return nil, fmt.Errorf("worker: supervisor is required")
	}
	a := &Allocator{supervisor: supervisor}
	for _, opt := range opts {
		if opt != nil {
			opt(a)
		}
	}
	if a.readinessTimeout < 0 {
		return nil, fmt.Errorf("worker: readiness timeout must not be negative")
	}
	if a.readinessTimeout == 0 {
		a.readinessTimeout = DefaultReadinessTimeout
	}
	return a, nil
}

// Allocate starts one server process for the ready revision and waits for
// explicit readiness evidence before returning. The returned process is
// marked ready with the exact evidence and carries the supervisor-assigned
// connect address.
//
// On failure the process is stopped if necessary and reaped exactly once,
// and a typed orchestrator.AllocationError is returned; no partial process
// is left behind. On success the process stays supervisor-owned for the
// later shutdown path.
func (a *Allocator) Allocate(ctx context.Context, revision *core.ScenarioRevision) (*orchestrator.AllocatedProcess, error) {
	if revision == nil {
		return nil, &orchestrator.AllocationError{Reason: &core.FailureReason{
			Code:    orchestrator.AllocationFailureCode,
			Message: "no revision to allocate a process for",
		}}
	}
	if err := ctx.Err(); err != nil {
		return nil, &orchestrator.AllocationError{Reason: &core.FailureReason{
			Code:    orchestrator.AllocationFailureCode,
			Message: fmt.Sprintf("allocation cancelled before start: %v", err),
		}}
	}

	proc, err := a.supervisor.Start(ctx)
	if err != nil {
		// The launch failed before any process existed: the supervisor
		// already rolled back its reservation.
		return nil, &orchestrator.AllocationError{Reason: &core.FailureReason{
			Code:    orchestrator.AllocationFailureCode,
			Message: fmt.Sprintf("launch failed: %v", err),
		}}
	}

	fact, waitErr := a.waitReadiness(ctx, proc)
	if waitErr != nil {
		a.cleanup(proc)
		return nil, &orchestrator.AllocationError{Reason: &core.FailureReason{
			Code:    orchestrator.AllocationFailureCode,
			Message: fmt.Sprintf("process %d never became ready: %v", proc.Spec().Generation, waitErr),
		}}
	}

	allocated := &orchestrator.AllocatedProcess{
		Generation:     proc.Spec().Generation,
		ConnectAddress: fmt.Sprintf("127.0.0.1:%d", proc.Spec().Port),
	}
	allocated.MarkReady(fact.Evidence)
	return allocated, nil
}

// waitReadiness waits for explicit readiness evidence without polling. It
// fails when the context ends first or when the process reaches a terminal
// state without readiness.
func (a *Allocator) waitReadiness(ctx context.Context, proc server.Process) (server.ReadinessFact, error) {
	waiter, ok := proc.(server.ProcessReadinessWaiter)
	if !ok {
		return server.ReadinessFact{}, fmt.Errorf(
			"process implementation %T does not expose a readiness wait", proc)
	}

	waitCtx, cancel := context.WithTimeout(ctx, a.readinessTimeout)
	defer cancel()
	return waiter.WaitReadiness(waitCtx)
}

// cleanup stops the process if it is still live and reaps it exactly once.
// The stop runs on a fresh context: the caller's context may already be
// cancelled, and a cancelled context must not prevent the teardown that
// protects the host from an orphaned process. A process that crashed on its
// own is already terminal, so the stop is skipped and the crash truth is
// preserved.
func (a *Allocator) cleanup(proc server.Process) {
	if stopper, ok := proc.(server.ProcessStopper); ok && !proc.State().Terminal() {
		stopCtx, cancel := context.WithTimeout(context.Background(), stopTimeout)
		_ = stopper.Stop(stopCtx)
		cancel()
	}

	// Reaping requires a terminal process and releases the supervisor's
	// port and spool reservation. A second reap is refused with
	// ErrUnknownProcess, so this is exactly-once per handle.
	_ = a.supervisor.Reap(proc)
}

// stopTimeout bounds the teardown issued during failed-allocation cleanup.
const stopTimeout = 10 * time.Second
