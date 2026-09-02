package worker

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/paralin/fraglands/core"
	"github.com/paralin/fraglands/orchestrator"
	"github.com/paralin/fraglands/server"
)

// ---------------------------------------------------------------------------
// test doubles
// ---------------------------------------------------------------------------

// fakeProcess implements server.Process, server.ProcessStopper,
// server.ProcessReadinessWaiter, and the hostexec CrashReason getter for
// allocator tests.
type fakeProcess struct {
	mtx       sync.Mutex
	spec      server.ProcessSpec
	state     server.ProcessState
	fact      *server.ReadinessFact
	reason    *server.CrashReason
	stopped   bool
	stopCount int
	readyOnce sync.Once
	// readyAfter is how long after launch readiness is recorded.
	// Negative means readiness is never recorded.
	readyAfter time.Duration
	// crashBeforeReady makes the process crash instead of becoming ready.
	crashBeforeReady bool
	// stateReads and readyReads count external observations; the
	// allocator must not poll either while waiting for readiness.
	stateReads int
	readyReads int
}

func newFakeAllocatorProcess(spec server.ProcessSpec, readyAfter time.Duration) *fakeProcess {
	return &fakeProcess{
		spec:       spec,
		state:      server.ProcessStateLaunching,
		readyAfter: readyAfter,
	}
}

func (f *fakeProcess) Spec() server.ProcessSpec { return f.spec }

func (f *fakeProcess) State() server.ProcessState {
	f.mtx.Lock()
	defer f.mtx.Unlock()
	f.stateReads++
	return f.state
}

func (f *fakeProcess) Readiness() (server.ReadinessFact, error) {
	f.mtx.Lock()
	defer f.mtx.Unlock()
	f.readyReads++
	if f.fact == nil {
		return server.ReadinessFact{}, server.ErrNoReadinessFact
	}
	return *f.fact, nil
}

func (f *fakeProcess) MarkRunning() error {
	f.mtx.Lock()
	defer f.mtx.Unlock()
	if f.state != server.ProcessStateLaunching {
		return server.ErrNotRunning
	}
	f.state = server.ProcessStateRunning
	return nil
}

func (f *fakeProcess) MarkReady(fact server.ReadinessFact) error {
	f.mtx.Lock()
	defer f.mtx.Unlock()
	if f.fact != nil {
		return server.ErrReadinessAlreadyRecorded
	}
	if f.state != server.ProcessStateRunning {
		return server.ErrNotReady
	}
	f.fact = &fact
	f.state = server.ProcessStateReady
	return nil
}

func (f *fakeProcess) MarkCrashed(reason server.CrashReason) error {
	f.mtx.Lock()
	defer f.mtx.Unlock()
	if f.state.Terminal() {
		return server.ErrAlreadyStopped
	}
	f.reason = &reason
	f.state = server.ProcessStateCrashed
	return nil
}

func (f *fakeProcess) MarkStopped() error {
	f.mtx.Lock()
	defer f.mtx.Unlock()
	if f.state.Terminal() {
		return server.ErrAlreadyStopped
	}
	f.state = server.ProcessStateStopped
	return nil
}

// CrashReason returns the recorded crash reason, mirroring the hostexec
// handle's optional getter.
func (f *fakeProcess) CrashReason() (server.CrashReason, error) {
	f.mtx.Lock()
	defer f.mtx.Unlock()
	if f.state != server.ProcessStateCrashed || f.reason == nil {
		return server.CrashReason{}, server.ErrCrashed
	}
	return *f.reason, nil
}

func (f *fakeProcess) WaitTerminal(ctx context.Context) (server.ProcessState, error) {
	for {
		if s := f.State(); s.Terminal() {
			return s, nil
		}
		select {
		case <-ctx.Done():
			return f.State(), ctx.Err()
		case <-time.After(2 * time.Millisecond):
		}
	}
}

func (f *fakeProcess) DeliverArtifact(a server.Artifact) error {
	f.mtx.Lock()
	defer f.mtx.Unlock()
	if f.state != server.ProcessStateRunning && f.state != server.ProcessStateReady {
		return server.ErrNotRunning
	}
	return nil
}

func (f *fakeProcess) Artifacts() []server.Artifact { return nil }

// Stop implements server.ProcessStopper: it moves the process to Stopped
// and records that a stop was requested.
func (f *fakeProcess) Stop(ctx context.Context) error {
	f.mtx.Lock()
	defer f.mtx.Unlock()
	f.stopCount++
	if f.state == server.ProcessStateStopped {
		return nil
	}
	if f.state == server.ProcessStateCrashed {
		return server.ErrCrashed
	}
	f.stopped = true
	f.state = server.ProcessStateStopped
	return nil
}

// WaitReadiness implements server.ProcessReadinessWaiter: it records
// readiness after readyAfter, or reports ErrNotReady if the process became
// terminal first.
func (f *fakeProcess) WaitReadiness(ctx context.Context) (server.ReadinessFact, error) {
	// The fake records readiness after its configured delay, exactly once.
	// A negative delay means readiness never comes.
	if f.readyAfter >= 0 && !f.crashBeforeReady {
		f.readyOnce.Do(func() {
			go func() {
				time.Sleep(f.readyAfter)
				f.mtx.Lock()
				defer f.mtx.Unlock()
				if f.fact != nil || f.state.Terminal() {
					return
				}
				if f.state == server.ProcessStateLaunching {
					f.state = server.ProcessStateRunning
				}
				if f.state != server.ProcessStateRunning {
					return
				}
				f.fact = &server.ReadinessFact{Evidence: "fake: ready", RecordedAt: time.Now()}
				f.state = server.ProcessStateReady
			}()
		})
	}
	if f.crashBeforeReady {
		f.mtx.Lock()
		if !f.state.Terminal() {
			f.reason = &server.CrashReason{Code: "exit_nonzero", Message: "crashed before ready"}
			f.state = server.ProcessStateCrashed
		}
		f.mtx.Unlock()
		return server.ReadinessFact{}, server.ErrNotReady
	}

	// Wait on the state transitions without polling: readiness or terminal.
	for {
		f.mtx.Lock()
		if f.fact != nil {
			fact := *f.fact
			f.mtx.Unlock()
			return fact, nil
		}
		if f.state.Terminal() {
			f.mtx.Unlock()
			return server.ReadinessFact{}, server.ErrNotReady
		}
		f.mtx.Unlock()
		select {
		case <-ctx.Done():
			return server.ReadinessFact{}, ctx.Err()
		case <-time.After(2 * time.Millisecond):
		}
	}
}

// stopFailsProcess wraps a fakeProcess whose Stop fails while the process
// is live. The wrapper is the handle the supervisor registers, so reaping
// the wrapper keeps the registry identity intact.
type stopFailsProcess struct {
	*fakeProcess
}

func (f *stopFailsProcess) Stop(ctx context.Context) error {
	if f.fakeProcess.State() == server.ProcessStateStopped {
		return f.fakeProcess.Stop(ctx)
	}
	return errors.New("fake: stop failed (host unreachable)")
}

// stopErrorsButCrashes wraps a fakeProcess whose Stop reports an error even
// though the process reached the terminal Crashed state during the stop.
type stopErrorsButCrashes struct {
	*fakeProcess
}

func (f *stopErrorsButCrashes) Stop(ctx context.Context) error {
	_ = f.fakeProcess.MarkCrashed(server.CrashReason{Code: "exit_nonzero", Message: "crashed during stop"})
	return errors.New("fake: stop error after crash")
}

// recoverableStopProcess wraps a fakeProcess whose first Stop fails (the
// host is briefly unreachable) and whose later Stops succeed.
type recoverableStopProcess struct {
	*fakeProcess
	failFirst *sync.Once
}

func (f *recoverableStopProcess) Stop(ctx context.Context) error {
	failed := false
	f.failFirst.Do(func() { failed = true })
	if failed {
		return errors.New("fake: host unreachable")
	}
	return f.fakeProcess.Stop(ctx)
}

// bareProcess is a fakeProcess reduced to exactly the server.Process
// contract: no WaitReadiness, no Stop. Methods are delegated explicitly so
// the optional seams are not promoted from the embedded fake.
type bareProcess struct {
	f *fakeProcess
}

func (b *bareProcess) Spec() server.ProcessSpec { return b.f.Spec() }
func (b *bareProcess) State() server.ProcessState {
	return b.f.State()
}
func (b *bareProcess) Readiness() (server.ReadinessFact, error) {
	return b.f.Readiness()
}
func (b *bareProcess) MarkRunning() error { return b.f.MarkRunning() }
func (b *bareProcess) MarkReady(fact server.ReadinessFact) error {
	return b.f.MarkReady(fact)
}
func (b *bareProcess) MarkCrashed(reason server.CrashReason) error {
	return b.f.MarkCrashed(reason)
}
func (b *bareProcess) MarkStopped() error { return b.f.MarkStopped() }
func (b *bareProcess) WaitTerminal(ctx context.Context) (server.ProcessState, error) {
	return b.f.WaitTerminal(ctx)
}
func (b *bareProcess) DeliverArtifact(a server.Artifact) error {
	return b.f.DeliverArtifact(a)
}
func (b *bareProcess) Artifacts() []server.Artifact { return b.f.Artifacts() }

// fakeSupervisor is a server.ProcessLauncher that hands out fake processes
// through a real *server.Supervisor, and tracks what happened to each
// process it launched.
type fakeSupervisor struct {
	mtx      sync.Mutex
	launched []*fakeProcess
	failNext bool
	// Behavior applied to every fake process handed out by Launch.
	readyAfter       time.Duration
	crashBeforeReady bool
	// wrap, when set, wraps the launched fake in an outer handle; the
	// supervisor registers the wrapped handle, keeping registry identity.
	wrap       func(*fakeProcess) server.Process
	supervisor *server.Supervisor
}

func newFakeSupervisor() *fakeSupervisor {
	s := &fakeSupervisor{}
	sup, err := server.NewSupervisor(s, 9000, "/tmp/spool")
	if err != nil {
		panic(err)
	}
	s.supervisor = sup
	return s
}

func (s *fakeSupervisor) Launch(ctx context.Context, spec server.ProcessSpec) (server.Process, error) {
	s.mtx.Lock()
	defer s.mtx.Unlock()
	if s.failNext {
		s.failNext = false
		return nil, errors.New("fake supervisor: launch refused")
	}
	proc := newFakeAllocatorProcess(spec, s.readyAfter)
	proc.crashBeforeReady = s.crashBeforeReady
	s.launched = append(s.launched, proc)
	if s.wrap != nil {
		return s.wrap(proc), nil
	}
	return proc, nil
}

// launchedByGeneration returns the fake process the supervisor launched for
// one generation.
func (s *fakeSupervisor) launchedByGeneration(gen uint64) *fakeProcess {
	s.mtx.Lock()
	defer s.mtx.Unlock()
	for _, p := range s.launched {
		if p.spec.Generation == gen {
			return p
		}
	}
	return nil
}

// assertReapedExactlyOnce verifies through the supervisor registry that the
// generation was reaped and its reservation freed: Get refuses the ID, and
// a second Reap of the same handle is refused, so a cleanup path that ran
// twice would be visible here.
func (s *fakeSupervisor) assertReapedExactlyOnce(t *testing.T, gen uint64, proc server.Process) {
	t.Helper()
	if _, err := s.supervisor.Get(procKey(gen)); !errors.Is(err, server.ErrUnknownProcess) {
		t.Fatalf("expected generation %d reaped (unknown), got %v", gen, err)
	}
	if err := s.supervisor.Reap(proc); !errors.Is(err, server.ErrUnknownProcess) {
		t.Fatalf("expected second reap refused for generation %d, got %v", gen, err)
	}
}

func procKey(gen uint64) string { return fmt.Sprintf("proc-%d", gen) }

func testAllocator() (*Allocator, *fakeSupervisor) {
	sup := newFakeSupervisor()
	a, err := NewAllocator(sup.supervisor)
	if err != nil {
		panic(err)
	}
	return a, sup
}

func revision() *core.ScenarioRevision {
	return &core.ScenarioRevision{ID: "rev-1", ReplayID: "replay-1", TakeoverTick: 100}
}

// ---------------------------------------------------------------------------
// tests
// ---------------------------------------------------------------------------

func TestAllocatorMarksReadyWithConnectAddress(t *testing.T) {
	a, sup := testAllocator()

	proc, err := a.Allocate(context.Background(), revision())
	if err != nil {
		t.Fatal(err.Error())
	}
	if !proc.Ready() {
		t.Fatal("allocated process must be ready")
	}
	if proc.Generation != 1 {
		t.Fatalf("expected generation 1, got %d", proc.Generation)
	}
	want := "127.0.0.1:9000"
	if proc.ConnectAddress != want {
		t.Fatalf("expected connect address %q, got %q", want, proc.ConnectAddress)
	}
	if proc.Evidence() != "fake: ready" {
		t.Fatalf("expected evidence on the allocated process, got %q", proc.Evidence())
	}
	// A successful allocation stays supervisor-owned: nothing was reaped.
	if sup.supervisor.Live() != 1 {
		t.Fatalf("expected 1 live process, got %d", sup.supervisor.Live())
	}
	if _, err := sup.supervisor.Get(procKey(1)); err != nil {
		t.Fatalf("successful process must stay supervisor-owned: %v", err)
	}
}

func TestAllocatorLaunchFailureIsTyped(t *testing.T) {
	sup := newFakeSupervisor()
	sup.failNext = true
	a, err := NewAllocator(sup.supervisor)
	if err != nil {
		t.Fatal(err.Error())
	}

	_, err = a.Allocate(context.Background(), revision())
	var allocErr *orchestrator.AllocationError
	if !errors.As(err, &allocErr) {
		t.Fatalf("expected *orchestrator.AllocationError, got %T", err)
	}
	if allocErr.Reason.Code != orchestrator.AllocationFailureCode {
		t.Fatalf("expected code %s, got %s", orchestrator.AllocationFailureCode, allocErr.Reason.Code)
	}
	if sup.supervisor.Live() != 0 {
		t.Fatalf("expected no live processes, got %d", sup.supervisor.Live())
	}
}

func TestAllocatorContextCancelBeforeStart(t *testing.T) {
	sup := newFakeSupervisor()
	a, err := NewAllocator(sup.supervisor)
	if err != nil {
		t.Fatal(err.Error())
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, aerr := a.Allocate(ctx, revision())
	var allocErr *orchestrator.AllocationError
	if !errors.As(aerr, &allocErr) {
		t.Fatalf("expected *orchestrator.AllocationError, got %v", aerr)
	}
	if sup.supervisor.Live() != 0 {
		t.Fatalf("expected no live processes, got %d", sup.supervisor.Live())
	}
}

func TestAllocatorContextCancelDuringReadiness(t *testing.T) {
	// Readiness is 10s away; the cancel must win long before.
	sup := newFakeSupervisor()
	sup.readyAfter = 10 * time.Second
	a, err := NewAllocator(sup.supervisor, WithReadinessTimeout(30*time.Second))
	if err != nil {
		t.Fatal(err.Error())
	}

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		_, err := a.Allocate(ctx, revision())
		done <- err
	}()
	time.Sleep(50 * time.Millisecond)
	cancel()

	select {
	case err := <-done:
		var allocErr *orchestrator.AllocationError
		if !errors.As(err, &allocErr) {
			t.Fatalf("expected *orchestrator.AllocationError, got %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("Allocate did not return after cancel")
	}

	// The cancelled allocation must not leave a live process behind: the
	// process was stopped and reaped exactly once.
	if sup.supervisor.Live() != 0 {
		t.Fatalf("expected 0 live processes after failed allocation, got %d", sup.supervisor.Live())
	}
	sup.assertReapedExactlyOnce(t, 1, sup.launchedByGeneration(1))

	// No polling: while waiting for readiness the allocator must not have
	// observed the process state or readiness fact repeatedly. The only
	// State() reads come from cleanup (and the supervisor's own reap
	// check); Readiness() must never have been read.
	fp := sup.launchedByGeneration(1)
	if fp.readyReads != 0 {
		t.Fatalf("allocator must not poll readiness, got %d reads", fp.readyReads)
	}
	if fp.stateReads > 2 {
		t.Fatalf("allocator must not poll state, got %d reads", fp.stateReads)
	}
}

func TestAllocatorTerminalBeforeReadyIsFailure(t *testing.T) {
	sup := newFakeSupervisor()
	sup.crashBeforeReady = true
	a, err := NewAllocator(sup.supervisor)
	if err != nil {
		t.Fatal(err.Error())
	}

	done := make(chan error, 1)
	go func() {
		_, err := a.Allocate(context.Background(), revision())
		done <- err
	}()

	select {
	case err := <-done:
		var allocErr *orchestrator.AllocationError
		if !errors.As(err, &allocErr) {
			t.Fatalf("expected *orchestrator.AllocationError, got %T (%v)", err, err)
		}
		// The specific crash reason must be preserved in the failure.
		if !strings.Contains(allocErr.Reason.Message, "exit_nonzero") {
			t.Fatalf("crash reason must be preserved, got %q", allocErr.Reason.Message)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("Allocate did not return after crash")
	}

	// A crashed process must not be handed back as a forever-unready
	// AllocatedProcess: the allocator reported a typed failure and the
	// process was reaped exactly once.
	if sup.supervisor.Live() != 0 {
		t.Fatalf("expected 0 live processes, got %d", sup.supervisor.Live())
	}
	sup.assertReapedExactlyOnce(t, 1, sup.launchedByGeneration(1))
}

func TestAllocatorCleansUpExactlyOnceOnFailure(t *testing.T) {
	// readiness is never recorded, so allocation fails through the
	// readiness timeout.
	sup := newFakeSupervisor()
	sup.readyAfter = -1
	a, err := NewAllocator(sup.supervisor, WithReadinessTimeout(80*time.Millisecond))
	if err != nil {
		t.Fatal(err.Error())
	}

	_, err = a.Allocate(context.Background(), revision())
	var allocErr *orchestrator.AllocationError
	if !errors.As(err, &allocErr) {
		t.Fatalf("expected *orchestrator.AllocationError, got %v", err)
	}

	if sup.supervisor.Live() != 0 {
		t.Fatalf("expected 0 live processes, got %d", sup.supervisor.Live())
	}
	sup.assertReapedExactlyOnce(t, 1, sup.launchedByGeneration(1))
	fp := sup.launchedByGeneration(1)
	if !fp.stopped {
		t.Fatal("expected the live process to be stopped before reaping")
	}
	if fp.stopCount != 1 {
		t.Fatalf("expected exactly 1 stop, got %d", fp.stopCount)
	}
}

// Stop fails while the process is live, but the process becomes terminal
// anyway: cleanup must treat the terminal state as the reclaim (crash truth
// preserved) and report an ordinary allocation failure, not a cleanup
// failure.
func TestAllocatorStopFailsButProcessTerminal(t *testing.T) {
	sup := newFakeSupervisor()
	sup.readyAfter = -1
	sup.wrap = func(fp *fakeProcess) server.Process {
		return &stopErrorsButCrashes{fakeProcess: fp}
	}
	a, err := NewAllocator(sup.supervisor, WithReadinessTimeout(80*time.Millisecond))
	if err != nil {
		t.Fatal(err.Error())
	}

	_, aerr := a.Allocate(context.Background(), revision())
	var allocErr *orchestrator.AllocationError
	if !errors.As(aerr, &allocErr) {
		t.Fatalf("expected *orchestrator.AllocationError, got %v", aerr)
	}
	// The crash reason from the stop window is preserved.
	if !strings.Contains(allocErr.Reason.Message, "exit_nonzero") {
		t.Fatalf("crash reason must be preserved, got %q", allocErr.Reason.Message)
	}

	if sup.supervisor.Live() != 0 {
		t.Fatalf("expected 0 live processes, got %d", sup.supervisor.Live())
	}
	sup.assertReapedExactlyOnce(t, 1, sup.launchedByGeneration(1))
}

// Stop fails while the process is live and stays live: cleanup cannot
// reclaim it, so Allocate must return a CleanupError (recoverable
// ownership) instead of an ordinary allocation failure.
func TestAllocatorStopFailsProcessRemainsLive(t *testing.T) {
	sup := newFakeSupervisor()
	sup.readyAfter = -1
	sup.wrap = func(fp *fakeProcess) server.Process {
		return &stopFailsProcess{fakeProcess: fp}
	}
	a, err := NewAllocator(sup.supervisor, WithReadinessTimeout(80*time.Millisecond))
	if err != nil {
		t.Fatal(err.Error())
	}

	_, err = a.Allocate(context.Background(), revision())
	var ce *CleanupError
	if !errors.As(err, &ce) {
		t.Fatalf("expected *CleanupError, got %v", err)
	}
	if ce.Generation != 1 {
		t.Fatalf("expected generation 1 in CleanupError, got %d", ce.Generation)
	}
	if ce.Reason == nil || ce.Reason.Code != orchestrator.AllocationFailureCode {
		t.Fatalf("CleanupError must carry the allocation reason, got %+v", ce.Reason)
	}

	// The process must still be visible in the supervisor registry (live,
	// recoverable) - NOT silently reaped or forgotten.
	if sup.supervisor.Live() != 1 {
		t.Fatalf("expected the un-reclaimed process to remain live, got %d live", sup.supervisor.Live())
	}
	if _, err := sup.supervisor.Get(procKey(1)); err != nil {
		t.Fatalf("process must remain recoverable via supervisor.Get: %v", err)
	}
}

// Stop fails while live, the generation is enumerable, and a later Recover
// (after the stop obstruction clears) reclaims the process: supervisor Live
// drops to 0, Get refuses the ID, and the reservation is released.
func TestAllocatorRecoverAfterStopFailsLive(t *testing.T) {
	sup := newFakeSupervisor()
	sup.readyAfter = -1
	// The stop obstruction is a one-shot: the first Stop (during failed
	// cleanup) fails, later Stops succeed. This models a host that is
	// briefly unreachable and then recovers.
	var failFirstStop sync.Once
	sup.wrap = func(fp *fakeProcess) server.Process {
		return &recoverableStopProcess{fakeProcess: fp, failFirst: &failFirstStop}
	}
	a, err := NewAllocator(sup.supervisor, WithReadinessTimeout(80*time.Millisecond))
	if err != nil {
		t.Fatal(err.Error())
	}

	_, aerr := a.Allocate(context.Background(), revision())
	var ce *CleanupError
	if !errors.As(aerr, &ce) {
		t.Fatalf("expected *CleanupError, got %v", aerr)
	}

	// The generation is enumerable through the allocator itself; recovery
	// never depends on the caller retaining the error.
	gens := a.FailedCleanupGenerations()
	if len(gens) != 1 || gens[0] != 1 {
		t.Fatalf("expected [1] retained, got %v", gens)
	}
	if sup.supervisor.Live() != 1 {
		t.Fatalf("expected the process to remain live before recovery, got %d", sup.supervisor.Live())
	}

	// Recover reclaims it: stop (now succeeding) + reap.
	if err := a.Recover(context.Background(), 1); err != nil {
		t.Fatalf("recovery failed: %v", err)
	}

	if sup.supervisor.Live() != 0 {
		t.Fatalf("expected 0 live processes after recovery, got %d", sup.supervisor.Live())
	}
	if _, err := sup.supervisor.Get(procKey(1)); !errors.Is(err, server.ErrUnknownProcess) {
		t.Fatalf("expected generation 1 unknown after recovery, got %v", err)
	}
	// The retained handle is deleted only after the verified reclaim.
	if gens := a.FailedCleanupGenerations(); len(gens) != 0 {
		t.Fatalf("expected no retained generations after recovery, got %v", gens)
	}
	sup.assertReapedExactlyOnce(t, 1, sup.launchedByGeneration(1))
}

// Recover on a generation that was never retained is a no-op.
func TestAllocatorRecoverUnknownGeneration(t *testing.T) {
	a, _ := testAllocator()
	if err := a.Recover(context.Background(), 99); err != nil {
		t.Fatalf("recover of unknown generation must be a no-op, got %v", err)
	}
}

// Concurrent Recover calls for one generation: exactly one owner performs
// the teardown, the rest observe success, and the process is reclaimed
// exactly once.
func TestRaceRecoverConcurrent(t *testing.T) {
	sup := newFakeSupervisor()
	sup.readyAfter = -1
	var failFirstStop sync.Once
	sup.wrap = func(fp *fakeProcess) server.Process {
		return &recoverableStopProcess{fakeProcess: fp, failFirst: &failFirstStop}
	}
	a, err := NewAllocator(sup.supervisor, WithReadinessTimeout(80*time.Millisecond))
	if err != nil {
		t.Fatal(err.Error())
	}

	_, aerr := a.Allocate(context.Background(), revision())
	var ce *CleanupError
	if !errors.As(aerr, &ce) {
		t.Fatalf("expected CleanupError, got %v", aerr)
	}

	const n = 6
	var wg sync.WaitGroup
	errs := make([]error, n)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			errs[i] = a.Recover(context.Background(), 1)
		}(i)
	}
	wg.Wait()

	for i, err := range errs {
		if err != nil {
			t.Fatalf("concurrent recover %d failed: %v", i, err)
		}
	}
	if sup.supervisor.Live() != 0 {
		t.Fatalf("expected 0 live processes, got %d", sup.supervisor.Live())
	}
	if gens := a.FailedCleanupGenerations(); len(gens) != 0 {
		t.Fatalf("expected no retained generations, got %v", gens)
	}
	sup.assertReapedExactlyOnce(t, 1, sup.launchedByGeneration(1))
}

// A failed recovery keeps the handle retained: it stays enumerable and can
// be retried later.
func TestAllocatorFailedRecoverStaysRetained(t *testing.T) {
	sup := newFakeSupervisor()
	sup.readyAfter = -1
	// Every Stop fails for now: recovery cannot reclaim the process.
	sup.wrap = func(fp *fakeProcess) server.Process {
		return &stopFailsProcess{fakeProcess: fp}
	}
	a, err := NewAllocator(sup.supervisor, WithReadinessTimeout(80*time.Millisecond))
	if err != nil {
		t.Fatal(err.Error())
	}

	_, aerr := a.Allocate(context.Background(), revision())
	var ce *CleanupError
	if !errors.As(aerr, &ce) {
		t.Fatalf("expected CleanupError, got %v", aerr)
	}

	// Recovery fails while the stop obstruction persists.
	if err := a.Recover(context.Background(), 1); err == nil {
		t.Fatal("expected recovery to fail while stop keeps failing")
	}
	if gens := a.FailedCleanupGenerations(); len(gens) != 1 || gens[0] != 1 {
		t.Fatalf("expected generation 1 to stay retained, got %v", gens)
	}
	if sup.supervisor.Live() != 1 {
		t.Fatalf("expected the process to stay live, got %d", sup.supervisor.Live())
	}

	// Swap the wrap behavior: clear the obstruction by directly stopping
	// through the underlying fake (the operator's out-of-band repair), then
	// retry recovery; the reap then succeeds.
	fp := sup.launchedByGeneration(1)
	if err := fp.Stop(context.Background()); err != nil {
		t.Fatalf("out-of-band stop failed: %v", err)
	}
	if err := a.Recover(context.Background(), 1); err != nil {
		t.Fatalf("retry recovery failed: %v", err)
	}
	if sup.supervisor.Live() != 0 {
		t.Fatalf("expected 0 live processes after retry, got %d", sup.supervisor.Live())
	}
	if gens := a.FailedCleanupGenerations(); len(gens) != 0 {
		t.Fatalf("expected no retained generations after retry, got %v", gens)
	}
}

// A Reap refusal (live process with no stopper) keeps the handle retained:
// enumerable, recoverable later, never silently dropped.
func TestAllocatorReapRefusalStaysRetained(t *testing.T) {
	sup := newFakeSupervisor()
	sup.readyAfter = -1
	sup.wrap = func(fp *fakeProcess) server.Process {
		return &bareProcess{f: fp}
	}
	a, err := NewAllocator(sup.supervisor, WithReadinessTimeout(60*time.Millisecond))
	if err != nil {
		t.Fatal(err.Error())
	}

	_, aerr := a.Allocate(context.Background(), revision())
	var ce *CleanupError
	if !errors.As(aerr, &ce) {
		t.Fatalf("expected CleanupError, got %v", aerr)
	}
	if gens := a.FailedCleanupGenerations(); len(gens) != 1 || gens[0] != 1 {
		t.Fatalf("expected [1] retained, got %v", gens)
	}

	// Recovery also fails (still no stopper, still live).
	if err := a.Recover(context.Background(), 1); err == nil {
		t.Fatal("expected recovery to fail for a live bare process")
	}
	if gens := a.FailedCleanupGenerations(); len(gens) != 1 || gens[0] != 1 {
		t.Fatalf("expected generation 1 to stay retained, got %v", gens)
	}
	if sup.supervisor.Live() != 1 {
		t.Fatalf("expected the process to stay live and registered, got %d", sup.supervisor.Live())
	}
}

// RecoverAll reclaims every retained generation; Close is the shutdown
// alias. Mixed state: one recovers, one stays retained and is reported.
func TestAllocatorRecoverAllAndClose(t *testing.T) {
	sup := newFakeSupervisor()
	sup.readyAfter = -1
	// Generation 1: recoverable (first stop fails, retry succeeds).
	// Generation 2: permanently un-reclaimable (stop always fails).
	var failFirstStop sync.Once
	wraps := 0
	sup.wrap = func(fp *fakeProcess) server.Process {
		wraps++
		if wraps == 1 {
			return &recoverableStopProcess{fakeProcess: fp, failFirst: &failFirstStop}
		}
		return &stopFailsProcess{fakeProcess: fp}
	}
	a, err := NewAllocator(sup.supervisor, WithReadinessTimeout(60*time.Millisecond))
	if err != nil {
		t.Fatal(err.Error())
	}

	// Two failed allocations: generations 1 and 2.
	for i := 0; i < 2; i++ {
		_, aerr := a.Allocate(context.Background(), revision())
		var ce *CleanupError
		if !errors.As(aerr, &ce) {
			t.Fatalf("expected CleanupError for allocation %d, got %v", i+1, aerr)
		}
	}
	gens := a.FailedCleanupGenerations()
	if len(gens) != 2 {
		t.Fatalf("expected 2 retained generations, got %v", gens)
	}

	// RecoverAll reclaims what it can.
	failures := a.RecoverAll(context.Background())
	if len(failures) != 1 {
		t.Fatalf("expected exactly 1 recovery failure, got %v", failures)
	}
	if _, ok := failures[2]; !ok {
		t.Fatalf("expected generation 2 to fail recovery, got %v", failures)
	}
	if sup.supervisor.Live() != 1 {
		t.Fatalf("expected 1 live process (generation 2), got %d", sup.supervisor.Live())
	}
	if gens := a.FailedCleanupGenerations(); len(gens) != 1 || gens[0] != 2 {
		t.Fatalf("expected only generation 2 retained, got %v", gens)
	}

	// Close is the shutdown alias: it retries and reports the same failure.
	failures = a.Close(context.Background())
	if len(failures) != 1 {
		if _, ok := failures[2]; !ok {
			t.Fatalf("expected generation 2 to fail on Close, got %v", failures)
		}
	}
	if gens := a.FailedCleanupGenerations(); len(gens) != 1 || gens[0] != 2 {
		t.Fatalf("expected only generation 2 retained after Close, got %v", gens)
	}
}

// A process that exposes neither WaitReadiness nor Stop and is already
// terminal: the allocation fails with a typed AllocationError and the
// process is reaped exactly once (never a silent leak).
func TestAllocatorNonWaiterNonStopper(t *testing.T) {
	sup := newFakeSupervisor()
	a, err := NewAllocator(sup.supervisor, WithReadinessTimeout(100*time.Millisecond))
	if err != nil {
		t.Fatal(err.Error())
	}

	// Launch through the supervisor with the optional seams hidden: the
	// registered handle is a bare server.Process. Crash it before
	// readiness so cleanup has a terminal process to reap.
	sup.wrap = func(fp *fakeProcess) server.Process {
		return &bareProcess{f: fp}
	}
	proc, err := sup.supervisor.Start(context.Background())
	if err != nil {
		t.Fatal(err.Error())
	}
	bare := proc.(*bareProcess)
	if err := bare.MarkRunning(); err != nil {
		t.Fatal(err.Error())
	}
	if err := bare.MarkCrashed(server.CrashReason{Code: "exit_nonzero", Message: "died"}); err != nil {
		t.Fatal(err.Error())
	}

	_, err = a.waitReadiness(context.Background(), bare)
	if err == nil {
		t.Fatal("expected readiness-wait failure for non-waiter process")
	}

	// Cleanup on a terminal non-stopper: skip stop (terminal), reap only.
	if err := a.cleanup(bare); err != nil {
		t.Fatalf("cleanup of a terminal non-stopper should succeed: %v", err)
	}
	sup.assertReapedExactlyOnce(t, 1, bare)
}

// Reap refuses a live process: cleanup must surface that as a recoverable
// CleanupError instead of pretending the reservation was freed.
func TestAllocatorReapRefusalSurfacesCleanupError(t *testing.T) {
	sup := newFakeSupervisor()
	sup.readyAfter = -1
	// A live process with NO stopper: cleanup cannot stop it, so Reap is
	// never reached or is refused. The supervisor registers the bare
	// handle, so the registry identity holds for the refused reap.
	sup.wrap = func(fp *fakeProcess) server.Process {
		return &bareProcess{f: fp}
	}
	a, err := NewAllocator(sup.supervisor, WithReadinessTimeout(60*time.Millisecond))
	if err != nil {
		t.Fatal(err.Error())
	}

	_, aerr := a.Allocate(context.Background(), revision())
	var ce *CleanupError
	if !errors.As(aerr, &ce) {
		t.Fatalf("expected *CleanupError, got %v", aerr)
	}
	if ce.Generation != 1 {
		t.Fatalf("expected generation 1 in CleanupError, got %d", ce.Generation)
	}

	// The live process remains registered (recoverable ownership): no
	// invisible live child, no lost reservation.
	if sup.supervisor.Live() != 1 {
		t.Fatalf("expected the process to remain live and registered, got %d", sup.supervisor.Live())
	}
	if _, err := sup.supervisor.Get(procKey(1)); err != nil {
		t.Fatalf("process must remain recoverable via supervisor.Get: %v", err)
	}
}

func TestAllocatorConcurrentAllocations(t *testing.T) {
	a, sup := testAllocator()

	var wg sync.WaitGroup
	results := make([]*orchestrator.AllocatedProcess, 4)
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			proc, err := a.Allocate(context.Background(), revision())
			if err != nil {
				t.Error(err.Error())
				return
			}
			results[i] = proc
		}(i)
	}
	wg.Wait()

	generations := map[uint64]bool{}
	for _, proc := range results {
		if proc == nil {
			t.Fatal("expected a process for every allocation")
		}
		if !proc.Ready() {
			t.Fatal("every allocated process must be ready")
		}
		generations[proc.Generation] = true
	}
	if len(generations) != 4 {
		t.Fatalf("expected 4 unique generations, got %d", len(generations))
	}
	if sup.supervisor.Live() != 4 {
		t.Fatalf("expected 4 live processes, got %d", sup.supervisor.Live())
	}
}
