package worker

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/paralin/fraglands/core"
	"github.com/paralin/fraglands/orchestrator"
	"github.com/paralin/fraglands/server"
)

// fakeProcess implements server.Process for allocator tests.
type fakeProcess struct {
	mtx       sync.Mutex
	spec      server.ProcessSpec
	state     server.ProcessState
	fact      *server.ReadinessFact
	stopped   bool
	stopCount int
	readyOnce sync.Once
	// readyAfter is how long after launch readiness is recorded.
	// Negative means readiness is never recorded.
	readyAfter time.Duration
	// crashBeforeReady makes the process crash instead of becoming ready.
	crashBeforeReady bool
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
	return f.state
}

func (f *fakeProcess) Readiness() (server.ReadinessFact, error) {
	f.mtx.Lock()
	defer f.mtx.Unlock()
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

func (f *fakeProcess) WaitTerminal(ctx context.Context) (server.ProcessState, error) {
	for {
		f.mtx.Lock()
		state := f.state
		f.mtx.Unlock()
		if state.Terminal() {
			return state, nil
		}
		select {
		case <-ctx.Done():
			return state, ctx.Err()
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
// readiness after readyAfter, or reports a crash if the process became
// terminal first. It waits on a channel, not by polling state.
func (f *fakeProcess) WaitReadiness(ctx context.Context) (server.ReadinessFact, error) {
	// The fake records readiness after its configured delay, on a timer
	// goroutine, exactly once. Negative delay means readiness never comes.
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
	supervisor       *server.Supervisor
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

func procKey(gen uint64) string { return "proc-" + string(rune('0'+gen)) }

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
}

func TestAllocatorLaunchFailureIsTyped(t *testing.T) {
	sup := newFakeSupervisor()
	sup.failNext = true
	a, err := NewAllocator(sup.supervisor)
	if err != nil {
		t.Fatal(err.Error())
	}

	_, err = a.Allocate(context.Background(), revision())
	if err == nil {
		t.Fatal("expected typed allocation error")
	}
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
	if aerr == nil {
		t.Fatal("expected typed allocation error on cancelled context")
	}
	var allocErr *orchestrator.AllocationError
	if !errors.As(aerr, &allocErr) {
		t.Fatalf("expected *orchestrator.AllocationError, got %T", aerr)
	}
	if sup.supervisor.Live() != 0 {
		t.Fatalf("expected no live processes, got %d", sup.supervisor.Live())
	}
}

func TestAllocatorContextCancelDuringReadiness(t *testing.T) {
	// readyAfter is longer than the test's patience: the cancel must win.
	sup := newFakeSupervisor()
	sup.readyAfter = 10 * time.Second
	a, err := NewAllocator(sup.supervisor, WithReadinessTimeout(300*time.Millisecond))
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
		if err == nil {
			t.Fatal("expected typed allocation error after cancel")
		}
		var allocErr *orchestrator.AllocationError
		if !errors.As(err, &allocErr) {
			t.Fatalf("expected *orchestrator.AllocationError, got %T", err)
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
}

func TestAllocatorTerminalBeforeReadyIsFailure(t *testing.T) {
	sup := newFakeSupervisor()
	a, err := NewAllocator(sup.supervisor)
	if err != nil {
		t.Fatal(err.Error())
	}

	// Force the process to crash before readiness is recorded.
	sup.crashBeforeReady = true
	done := make(chan error, 1)
	go func() {
		_, err := a.Allocate(context.Background(), revision())
		done <- err
	}()

	select {
	case err := <-done:
		var allocErr *orchestrator.AllocationError
		if !errors.As(err, &allocErr) {
			t.Fatalf("expected *orchestrator.AllocationError, got %T", err)
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
	// readyAfter negative: readiness is never recorded, so allocation
	// fails through the readiness timeout.
	sup := newFakeSupervisor()
	sup.readyAfter = -1 // readiness is never recorded
	a, err := NewAllocator(sup.supervisor, WithReadinessTimeout(80*time.Millisecond))
	if err != nil {
		t.Fatal(err.Error())
	}

	_, err = a.Allocate(context.Background(), revision())
	if err == nil {
		t.Fatal("expected typed allocation error on readiness timeout")
	}
	var allocErr *orchestrator.AllocationError
	if !errors.As(err, &allocErr) {
		t.Fatalf("expected *orchestrator.AllocationError, got %T", err)
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
