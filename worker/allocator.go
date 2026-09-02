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

// CleanupError is returned when a process failed before readiness and the
// allocator could not fully reclaim it: the process either could not be
// stopped, or could not be reaped by the supervisor. The process stays
// registered with the supervisor under Generation, so the caller or an
// operator can recover it instead of it leaking invisibly. This is a
// distinct, recoverable outcome: it must not be mistaken for a clean
// rollback.
type CleanupError struct {
	// Generation is the generation of the process that could not be
	// reclaimed.
	Generation uint64

	// Reason is the typed failure reason for the underlying allocation
	// failure (why readiness was never proven).
	Reason *core.FailureReason

	// Err is the underlying cleanup failure (why the stop or the reap
	// did not succeed).
	Err error
}

// Error returns the reclaim failure with the generation and both causes.
func (e *CleanupError) Error() string {
	return fmt.Sprintf("worker: allocation failed and cleanup could not reclaim process %d: %s: %v",
		e.Generation, e.Reason.Message, e.Err)
}

// Unwrap exposes the underlying cleanup failure.
func (e *CleanupError) Unwrap() error { return e.Err }

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

// Allocate starts one server process for the ready revision and blocks until
// explicit readiness evidence is observed. The returned process is marked
// ready with the exact evidence and carries the supervisor-assigned connect
// address.
//
// If the context is cancelled, the readiness wait times out, or the process
// reaches a terminal state before readiness is proven, the process is
// stopped if necessary and reaped exactly once, and a typed
// orchestrator.AllocationError is returned: no partial process is left
// behind. If the teardown itself cannot reclaim the process, a CleanupError
// is returned instead. It names the generation of the process still held by
// the supervisor so the caller can recover it; it is never a claim of clean
// rollback. On success the process stays supervisor-owned for the later
// shutdown path.
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
		return nil, a.failWithCleanup(proc, waitErr)
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

// crashReasonGetter is the optional seam for reading the typed crash reason
// from a terminal process. hostexec.LaunchedProcess implements it.
type crashReasonGetter interface {
	CrashReason() (server.CrashReason, error)
}

// failWithCleanup stops the process if it is still live, reaps it exactly
// once, and builds the typed failure. The crash reason is read after the
// teardown: a process can crash during the stop itself, and that typed
// crash reason is part of the failure truth. Both causes are preserved:
// the original reason readiness was never proven, and the crash when one
// was recorded. If the teardown cannot reclaim the process, the returned
// error is a CleanupError carrying the generation: an explicit, recoverable
// failure rather than a claim of clean rollback.
func (a *Allocator) failWithCleanup(proc server.Process, cause error) error {
	generation := proc.Spec().Generation
	message := fmt.Sprintf("process %d never became ready: %v", generation, cause)

	cleanupErr := a.cleanup(proc)

	// Read the crash truth after the teardown: the crash may have happened
	// during the stop itself.
	if getter, ok := proc.(crashReasonGetter); ok {
		if crash, err := getter.CrashReason(); err == nil && crash.Code != "" {
			message = fmt.Sprintf("%s (crash %s: %s)", message, crash.Code, crash.Message)
		}
	}
	reason := &core.FailureReason{
		Code:    orchestrator.AllocationFailureCode,
		Message: message,
	}

	if cleanupErr != nil {
		return &CleanupError{
			Generation: generation,
			Reason:     reason,
			Err:        cleanupErr,
		}
	}
	return &orchestrator.AllocationError{Reason: reason}
}

// cleanup stops the process if it is still live and reaps it exactly once.
// It returns an error when the process could not be reclaimed. The stop
// runs on a fresh context: the caller's context may already be cancelled,
// and a cancelled context must not prevent the teardown that protects the
// host from an orphaned process. A process that crashed on its own is
// already terminal, so the stop is skipped and the crash truth is
// preserved. A stop that reports an error is accepted when the process
// still reached a terminal state; otherwise the failure propagates and the
// process stays supervisor-registered for recovery.
func (a *Allocator) cleanup(proc server.Process) error {
	if !proc.State().Terminal() {
		stopper, ok := proc.(server.ProcessStopper)
		if !ok {
			return fmt.Errorf("process %d is live and implementation %T cannot be stopped",
				proc.Spec().Generation, proc)
		}
		stopCtx, cancel := context.WithTimeout(context.Background(), stopTimeout)
		defer cancel()
		if err := stopper.Stop(stopCtx); err != nil && !proc.State().Terminal() {
			return fmt.Errorf("stop of process %d failed: %w", proc.Spec().Generation, err)
		}
	}

	// Reaping requires a terminal process and releases the supervisor's
	// port and spool reservation. A second reap is refused with
	// ErrUnknownProcess, so this is exactly-once per handle.
	return a.supervisor.Reap(proc)
}

// stopTimeout bounds the teardown issued during failed-allocation cleanup.
const stopTimeout = 10 * time.Second
