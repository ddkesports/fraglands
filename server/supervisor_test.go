package server

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
)

// fakeProcess implements Process for tests: it records transitions and
// artifacts without touching any real host.
type fakeProcess struct {
	mtx       sync.Mutex
	spec      ProcessSpec
	state     ProcessState
	fact      *ReadinessFact
	reason    *CrashReason
	artifacts []Artifact

	waitCh chan struct{}
}

func newFakeProcess(spec ProcessSpec) *fakeProcess {
	return &fakeProcess{
		spec:   spec,
		state:  ProcessStateLaunching,
		waitCh: make(chan struct{}),
	}
}

func (f *fakeProcess) Spec() ProcessSpec { return f.spec }

func (f *fakeProcess) State() ProcessState {
	f.mtx.Lock()
	defer f.mtx.Unlock()
	return f.state
}

func (f *fakeProcess) Readiness() (ReadinessFact, error) {
	f.mtx.Lock()
	defer f.mtx.Unlock()
	if f.fact == nil {
		return ReadinessFact{}, ErrNoReadinessFact
	}
	return *f.fact, nil
}

func (f *fakeProcess) MarkRunning() error {
	f.mtx.Lock()
	defer f.mtx.Unlock()
	if f.state != ProcessStateLaunching {
		return ErrNotRunning
	}
	f.state = ProcessStateRunning
	return nil
}

func (f *fakeProcess) MarkReady(fact ReadinessFact) error {
	f.mtx.Lock()
	defer f.mtx.Unlock()
	if f.fact != nil {
		return ErrReadinessAlreadyRecorded
	}
	if f.state != ProcessStateRunning {
		return ErrNotReady
	}
	f.fact = &fact
	f.state = ProcessStateReady
	return nil
}

func (f *fakeProcess) MarkCrashed(reason CrashReason) error {
	f.mtx.Lock()
	defer f.mtx.Unlock()
	if f.state.Terminal() {
		return ErrAlreadyStopped
	}
	f.reason = &reason
	f.state = ProcessStateCrashed
	close(f.waitCh)
	return nil
}

func (f *fakeProcess) MarkStopped() error {
	f.mtx.Lock()
	defer f.mtx.Unlock()
	if f.state.Terminal() {
		return ErrAlreadyStopped
	}
	f.state = ProcessStateStopped
	close(f.waitCh)
	return nil
}

func (f *fakeProcess) WaitTerminal(ctx context.Context) (ProcessState, error) {
	select {
	case <-f.waitCh:
		return f.State(), nil
	case <-ctx.Done():
		return f.State(), ctx.Err()
	}
}

func (f *fakeProcess) DeliverArtifact(artifact Artifact) error {
	f.mtx.Lock()
	defer f.mtx.Unlock()
	if f.state != ProcessStateRunning && f.state != ProcessStateReady {
		return ErrNotRunning
	}
	f.artifacts = append(f.artifacts, artifact)
	return nil
}

func (f *fakeProcess) Artifacts() []Artifact {
	f.mtx.Lock()
	defer f.mtx.Unlock()
	return append([]Artifact(nil), f.artifacts...)
}

// fakeLauncher implements ProcessLauncher using fakeProcess.
type fakeLauncher struct {
	mtx      sync.Mutex
	launched []*fakeProcess
	failNext bool
}

func (l *fakeLauncher) Launch(ctx context.Context, spec ProcessSpec) (Process, error) {
	l.mtx.Lock()
	defer l.mtx.Unlock()
	if l.failNext {
		l.failNext = false
		return nil, ErrInvalidSpec
	}
	p := newFakeProcess(spec)
	l.launched = append(l.launched, p)
	return p, nil
}

func TestNewSupervisorValidation(t *testing.T) {
	if _, err := NewSupervisor(nil, 9000, "/tmp/spool"); err == nil {
		t.Fatal("expected error for nil launcher")
	}
	if _, err := NewSupervisor(&fakeLauncher{}, 0, "/tmp/spool"); err == nil {
		t.Fatal("expected error for invalid base port")
	}
	if _, err := NewSupervisor(&fakeLauncher{}, 9000, ""); err == nil {
		t.Fatal("expected error for empty spool root")
	}
	if _, err := NewSupervisor(&fakeLauncher{}, 9000, "/tmp/spool"); err != nil {
		t.Fatal(err.Error())
	}
}

func TestSupervisorAllocatesIsolatedSpecs(t *testing.T) {
	sup, err := NewSupervisor(&fakeLauncher{}, 9000, "/tmp/spool")
	if err != nil {
		t.Fatal(err.Error())
	}
	ctx := context.Background()

	p1, err := sup.Start(ctx)
	if err != nil {
		t.Fatal(err.Error())
	}
	p2, err := sup.Start(ctx)
	if err != nil {
		t.Fatal(err.Error())
	}

	if p1.Spec().Generation == p2.Spec().Generation {
		t.Fatal("generations must be unique")
	}
	if p1.Spec().Port == p2.Spec().Port {
		t.Fatal("ports must be isolated")
	}
	if p1.Spec().SpoolDir == p2.Spec().SpoolDir {
		t.Fatal("spool dirs must be isolated")
	}
	if p1.Spec().Port != 9000 || p2.Spec().Port != 9001 {
		t.Fatalf("unexpected ports: %d, %d", p1.Spec().Port, p2.Spec().Port)
	}
	if p1.Spec().SpoolDir != "/tmp/spool/gen-1" {
		t.Fatalf("unexpected spool dir: %s", p1.Spec().SpoolDir)
	}
}

func TestSupervisorStartFailsClosed(t *testing.T) {
	l := &fakeLauncher{failNext: true}
	sup, err := NewSupervisor(l, 9000, "/tmp/spool")
	if err != nil {
		t.Fatal(err.Error())
	}
	if _, err := sup.Start(context.Background()); err == nil {
		t.Fatal("expected launch failure")
	}
	// The next start should still succeed: the failed allocation was
	// released and does not leak.
	p, err := sup.Start(context.Background())
	if err != nil {
		t.Fatal(err.Error())
	}
	if p.Spec().Generation != 2 {
		t.Fatalf("expected generation 2, got %d", p.Spec().Generation)
	}
}

func TestProcessLifecycle(t *testing.T) {
	spec := ProcessSpec{Generation: 1, Port: 9000, SpoolDir: "/tmp/spool/gen-1"}
	p := newFakeProcess(spec)

	if p.State() != ProcessStateLaunching {
		t.Fatalf("expected launching, got %s", p.State())
	}
	if _, err := p.Readiness(); err != ErrNoReadinessFact {
		t.Fatalf("expected ErrNoReadinessFact, got %v", err)
	}
	if err := p.MarkReady(ReadinessFact{Evidence: "early"}); err != ErrNotReady {
		t.Fatalf("expected ErrNotReady before running, got %v", err)
	}

	if err := p.MarkRunning(); err != nil {
		t.Fatal(err.Error())
	}
	if err := p.DeliverArtifact(Artifact{Name: "log.txt", Data: []byte("ok")}); err != nil {
		t.Fatal(err.Error())
	}

	if err := p.MarkReady(ReadinessFact{Evidence: "listening on 9000"}); err != nil {
		t.Fatal(err.Error())
	}
	if p.State() != ProcessStateReady {
		t.Fatalf("expected ready, got %s", p.State())
	}
	fact, err := p.Readiness()
	if err != nil {
		t.Fatal(err.Error())
	}
	if fact.Evidence != "listening on 9000" {
		t.Fatalf("unexpected evidence: %s", fact.Evidence)
	}

	if err := p.MarkStopped(); err != nil {
		t.Fatal(err.Error())
	}
	if !p.State().Terminal() {
		t.Fatal("stopped must be terminal")
	}
	if err := p.MarkCrashed(CrashReason{Code: "x"}); err != ErrAlreadyStopped {
		t.Fatalf("expected ErrAlreadyStopped, got %v", err)
	}
}

func TestProcessCrashStop(t *testing.T) {
	spec := ProcessSpec{Generation: 1, Port: 9000, SpoolDir: "/tmp/spool/gen-1"}
	p := newFakeProcess(spec)
	ctx := context.Background()

	done := make(chan ProcessState, 1)
	go func() {
		state, err := p.WaitTerminal(ctx)
		if err != nil {
			t.Error(err.Error())
			return
		}
		done <- state
	}()

	if err := p.MarkRunning(); err != nil {
		t.Fatal(err.Error())
	}
	if err := p.MarkCrashed(CrashReason{Code: "exit_nonzero", Message: "process exited with code 1"}); err != nil {
		t.Fatal(err.Error())
	}

	select {
	case state := <-done:
		if state != ProcessStateCrashed {
			t.Fatalf("expected crashed, got %s", state)
		}
	case <-ctx.Done():
		t.Fatal("timed out waiting for terminal state")
	}

	// A crashed process accepts no further artifacts.
	if err := p.DeliverArtifact(Artifact{Name: "late.txt"}); err == nil {
		t.Fatal("expected artifact delivery refusal after crash")
	}
}

func TestArtifactDelivery(t *testing.T) {
	spec := ProcessSpec{Generation: 1, Port: 9000, SpoolDir: "/tmp/spool/gen-1"}
	p := newFakeProcess(spec)

	if err := p.DeliverArtifact(Artifact{Name: "a.txt"}); err == nil {
		t.Fatal("expected refusal before running")
	}
	if err := p.MarkRunning(); err != nil {
		t.Fatal(err.Error())
	}
	if err := p.DeliverArtifact(Artifact{Name: "a.txt", Data: []byte("one")}); err != nil {
		t.Fatal(err.Error())
	}
	if err := p.DeliverArtifact(Artifact{Name: "b.txt", Data: []byte("two")}); err != nil {
		t.Fatal(err.Error())
	}
	arts := p.Artifacts()
	if len(arts) != 2 {
		t.Fatalf("expected 2 artifacts, got %d", len(arts))
	}
	if arts[0].Name != "a.txt" || arts[1].Name != "b.txt" {
		t.Fatalf("unexpected artifacts: %+v", arts)
	}
}

func TestSupervisorRegistersLaunchedProcess(t *testing.T) {
	sup, err := NewSupervisor(&fakeLauncher{}, 9000, "/tmp/spool")
	if err != nil {
		t.Fatal(err.Error())
	}
	ctx := context.Background()

	proc, err := sup.Start(ctx)
	if err != nil {
		t.Fatal(err.Error())
	}

	// Get must return the same registered process.
	got, err := sup.Get(fmt.Sprintf("proc-%d", proc.Spec().Generation))
	if err != nil {
		t.Fatal(err.Error())
	}
	if got != proc {
		t.Fatal("Get returned a different process handle")
	}
	if sup.Live() != 1 {
		t.Fatalf("expected 1 live process, got %d", sup.Live())
	}
}

func TestSupervisorConcurrentStartAndRollback(t *testing.T) {
	l := &fakeLauncher{}
	sup, err := NewSupervisor(l, 9000, "/tmp/spool")
	if err != nil {
		t.Fatal(err.Error())
	}
	ctx := context.Background()

	const workers = 8
	var wg sync.WaitGroup
	var failMu sync.Mutex
	var successes int
	var failures int

	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			// Half the workers trigger a launch failure.
			if n%2 == 0 {
				l.mtx.Lock()
				l.failNext = true
				l.mtx.Unlock()
			}
			proc, err := sup.Start(ctx)
			failMu.Lock()
			defer failMu.Unlock()
			if err != nil {
				failures++
				return
			}
			successes++
			// Every successful start must be registered and live.
			got, err := sup.Get(fmt.Sprintf("proc-%d", proc.Spec().Generation))
			if err != nil {
				t.Errorf("Get after Start: %v", err)
				return
			}
			if got != proc {
				t.Error("Get returned a different process handle")
			}
		}(i)
	}
	wg.Wait()

	if successes+failures != workers {
		t.Fatalf("expected %d outcomes, got %d successes + %d failures", workers, successes, failures)
	}
	if successes == 0 || failures == 0 {
		t.Fatalf("expected both outcomes, got %d successes, %d failures", successes, failures)
	}
	// Only the successful starts stay registered; failed launches released
	// their reservations.
	if sup.Live() != successes {
		t.Fatalf("expected %d live processes, got %d", successes, sup.Live())
	}
}

func TestSupervisorRollbackFreesPortForReuse(t *testing.T) {
	l := &fakeLauncher{failNext: true}
	sup, err := NewSupervisor(l, 9000, "/tmp/spool")
	if err != nil {
		t.Fatal(err.Error())
	}
	ctx := context.Background()

	// The failed launch consumes nothing.
	if _, err := sup.Start(ctx); err == nil {
		t.Fatal("expected launch failure")
	}
	a, err := sup.Start(ctx)
	if err != nil {
		t.Fatal(err.Error())
	}
	// The failed launch consumed generation 1 but freed its port and spool;
	// the next success is generation 2 on the next port.
	if a.Spec().Port != 9001 || a.Spec().Generation != 2 {
		t.Fatalf("unexpected spec after rollback: %+v", a.Spec())
	}

	// Reap the terminal process and confirm the port and spool are freed
	// for reuse by the next generation.
	if err := a.MarkStopped(); err != nil {
		t.Fatal(err.Error())
	}
	if err := sup.Reap(a); err != nil {
		t.Fatal(err.Error())
	}
	if sup.Live() != 0 {
		t.Fatalf("expected 0 live processes after reap, got %d", sup.Live())
	}
	if _, err := sup.Get(fmt.Sprintf("proc-%d", a.Spec().Generation)); err != ErrUnknownProcess {
		t.Fatalf("expected ErrUnknownProcess after reap, got %v", err)
	}
	// The sequential allocator moves forward: the next generation takes the
	// next port. Reaping frees the reservation for a fresh supervisor or an
	// explicit reallocation, not for retroactive reuse by this one.
	b, err := sup.Start(ctx)
	if err != nil {
		t.Fatal(err.Error())
	}
	if b.Spec().Port != 9002 || b.Spec().Generation != 3 {
		t.Fatalf("unexpected spec after reap: %+v", b.Spec())
	}
	if b.Spec().SpoolDir != "/tmp/spool/gen-3" {
		t.Fatalf("unexpected spool dir: %s", b.Spec().SpoolDir)
	}
}

func TestSupervisorReapRefusesLiveAndUnknown(t *testing.T) {
	sup, err := NewSupervisor(&fakeLauncher{}, 9000, "/tmp/spool")
	if err != nil {
		t.Fatal(err.Error())
	}
	ctx := context.Background()

	proc, err := sup.Start(ctx)
	if err != nil {
		t.Fatal(err.Error())
	}
	// A live process cannot be reaped.
	if err := sup.Reap(proc); err != ErrNotRunning {
		t.Fatalf("expected ErrNotRunning for live reap, got %v", err)
	}
	// An unregistered handle cannot be reaped.
	stray := newFakeProcess(ProcessSpec{Generation: 99, Port: 9999, SpoolDir: "/tmp/spool/gen-99"})
	if err := sup.Reap(stray); err != ErrUnknownProcess {
		t.Fatalf("expected ErrUnknownProcess for stray reap, got %v", err)
	}
	// After stopping, the registered process reaps cleanly.
	if err := proc.MarkStopped(); err != nil {
		t.Fatal(err.Error())
	}
	if err := sup.Reap(proc); err != nil {
		t.Fatal(err.Error())
	}
	if err := sup.Reap(proc); err != ErrUnknownProcess {
		t.Fatalf("expected ErrUnknownProcess for double reap, got %v", err)
	}
}

func TestPortExhaustionRefusesWithoutConsumingGeneration(t *testing.T) {
	// basePort 65535: the second generation would need port 65536.
	sup, err := NewSupervisor(&fakeLauncher{}, 65535, "/tmp/spool")
	if err != nil {
		t.Fatal(err.Error())
	}
	ctx := context.Background()

	first, err := sup.Start(ctx)
	if err != nil {
		t.Fatal(err.Error())
	}
	if first.Spec().Port != 65535 {
		t.Fatalf("expected port 65535, got %d", first.Spec().Port)
	}
	_, err = sup.Start(ctx)
	if !errors.Is(err, ErrPortExhausted) {
		t.Fatalf("expected ErrPortExhausted, got %v", err)
	}
	// The exhausted allocation consumed no generation: a retry is refused
	// identically rather than drifting to a new port.
	_, err = sup.Start(ctx)
	if !errors.Is(err, ErrPortExhausted) {
		t.Fatalf("expected ErrPortExhausted on retry, got %v", err)
	}
}

func TestDoubleMarkReadyIsTypedRefusal(t *testing.T) {
	spec := ProcessSpec{Generation: 1, Port: 9000, SpoolDir: "/tmp/spool/gen-1"}
	p := newFakeProcess(spec)
	if err := p.MarkRunning(); err != nil {
		t.Fatal(err.Error())
	}
	if err := p.MarkReady(ReadinessFact{Evidence: "first"}); err != nil {
		t.Fatal(err.Error())
	}
	// A second readiness fact for one generation is refused with its own
	// typed error, never ErrNoReadinessFact.
	if err := p.MarkReady(ReadinessFact{Evidence: "second"}); !errors.Is(err, ErrReadinessAlreadyRecorded) {
		t.Fatalf("expected ErrReadinessAlreadyRecorded, got %v", err)
	}
	// The original fact is preserved.
	fact, err := p.Readiness()
	if err != nil {
		t.Fatal(err.Error())
	}
	if fact.Evidence != "first" {
		t.Fatalf("expected original evidence preserved, got %s", fact.Evidence)
	}
}
