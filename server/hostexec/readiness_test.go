package hostexec

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/paralin/fraglands/server"
)

// waitReadyChannel returns the exact evidence recorded by the launcher.
func TestWaitReadinessReturnsExactEvidence(t *testing.T) {
	l := mustLauncher(t)
	proc := launchHelper(t, l, helperModeSleepExit)
	defer reapForTest(t, proc)

	waiter, ok := proc.(server.ProcessReadinessWaiter)
	if !ok {
		t.Fatalf("process %T must implement server.ProcessReadinessWaiter", proc)
	}

	fact, err := waiter.WaitReadiness(context.Background())
	if err != nil {
		t.Fatalf("expected readiness, got error: %v", err)
	}
	if fact.Evidence != helperReadyLine {
		t.Fatalf("expected exact evidence %q, got %q", helperReadyLine, fact.Evidence)
	}
	if fact.RecordedAt.IsZero() {
		t.Fatal("expected non-zero RecordedAt")
	}
	if proc.State() != server.ProcessStateReady {
		t.Fatalf("expected ready state, got %s", proc.State())
	}
}

// A waiter started before the ready line is printed must not miss it.
func TestWaitReadinessStartedBeforeReady(t *testing.T) {
	l := mustLauncher(t)
	proc := launchHelper(t, l, helperModeSigterm)
	defer reapForTest(t, proc)

	type result struct {
		fact server.ReadinessFact
		err  error
	}
	done := make(chan result, 1)
	go func() {
		waiter := proc.(server.ProcessReadinessWaiter)
		fact, err := waiter.WaitReadiness(context.Background())
		done <- result{fact, err}
	}()

	time.Sleep(50 * time.Millisecond)
	// Give the helper a chance to print readiness and the watcher to record
	// it while the waiter is blocked.
	time.Sleep(150 * time.Millisecond)

	select {
	case r := <-done:
		if r.err != nil {
			t.Fatalf("expected readiness, got error: %v", r.err)
		}
		if r.fact.Evidence != helperReadyLine {
			t.Fatalf("expected exact evidence %q, got %q", helperReadyLine, r.fact.Evidence)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("WaitReadiness did not return")
	}
}

// A process that becomes terminal without readiness must return
// ErrNotReady, never a fabricated fact. The silent helper prints nothing:
// the launcher's readiness timeout crash-stops it while the waiter blocks.
func TestWaitReadinessTerminalBeforeReady(t *testing.T) {
	cfg := helperConfig(t)
	cfg.ReadyTimeout = 300 * time.Millisecond
	l, err := NewLauncher(cfg)
	if err != nil {
		t.Fatal(err)
	}
	proc := launchHelper(t, l, helperModeSilent)
	defer reapForTest(t, proc)

	waiter := proc.(server.ProcessReadinessWaiter)
	_, err = waiter.WaitReadiness(context.Background())
	if err != server.ErrNotReady {
		t.Fatalf("expected ErrNotReady, got %v", err)
	}
	if proc.State() != server.ProcessStateCrashed {
		t.Fatalf("expected crashed, got %s", proc.State())
	}
}

// A child that exits before printing the ready line is a crash without
// readiness: the waiter must observe ErrNotReady and the crash truth must
// stay on the process.
func TestWaitReadinessCrashWithoutReadyLine(t *testing.T) {
	cfg := helperConfig(t)
	cfg.ReadyLine = "never-matches-any-output"
	l, err := NewLauncher(cfg)
	if err != nil {
		t.Fatal(err)
	}
	proc := launchHelperWithEnv(t, l, helperModeExitCode,
		helperExitCodeEnv, "3")
	defer reapForTest(t, proc)

	waiter := proc.(server.ProcessReadinessWaiter)
	_, err = waiter.WaitReadiness(context.Background())
	if err != server.ErrNotReady {
		t.Fatalf("expected ErrNotReady, got %v", err)
	}
	if proc.State() != server.ProcessStateCrashed {
		t.Fatalf("expected crashed, got %s", proc.State())
	}
}

// WaitReadiness must respect the context. The silent helper prints nothing
// and hangs, so readiness never arrives.
func TestWaitReadinessContextCancel(t *testing.T) {
	l := mustLauncher(t)
	proc := launchHelper(t, l, helperModeSilent)
	defer reapForTest(t, proc)

	waiter := proc.(server.ProcessReadinessWaiter)
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	if _, err := waiter.WaitReadiness(ctx); err != context.DeadlineExceeded {
		t.Fatalf("expected context deadline exceeded, got %v", err)
	}
}

// WaitReadiness must be safe to call concurrently with readiness recording
// and state reads.
func TestRaceWaitReadinessConcurrent(t *testing.T) {
	l := mustLauncher(t)
	proc := launchHelper(t, l, helperModeSilent)
	defer reapForTest(t, proc)

	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			waiter := proc.(server.ProcessReadinessWaiter)
			_, _ = waiter.WaitReadiness(context.Background())
			_ = proc.State()
			_, _ = proc.Readiness()
		}()
	}
	wg.Wait()
}

// A waiter blocked on readiness must wake up and report ErrNotReady when a
// crash happens first. This is the concurrent readiness/terminal race: the
// crash must broadcast to readiness waiters, not leave them stuck.
func TestRaceWaitReadinessAgainstCrash(t *testing.T) {
	cfg := helperConfig(t)
	cfg.ReadyTimeout = 30 * time.Second
	l, err := NewLauncher(cfg)
	if err != nil {
		t.Fatal(err)
	}
	// The silent helper prints nothing and hangs, so the process is still
	// unready when it is killed below.
	proc := launchHelper(t, l, helperModeSilent)
	defer reapForTest(t, proc)

	type result struct {
		fact server.ReadinessFact
		err  error
	}
	done := make(chan result, 1)
	go func() {
		waiter := proc.(server.ProcessReadinessWaiter)
		fact, err := waiter.WaitReadiness(context.Background())
		done <- result{fact, err}
	}()

	// Let the waiter block on the readiness channel, then kill the child
	// directly. The launcher's reaper records the crash and must wake the
	// readiness waiter.
	time.Sleep(200 * time.Millisecond)
	if err := proc.(*LaunchedProcess).cmd.Process.Kill(); err != nil {
		t.Fatalf("could not kill helper: %v", err)
	}

	select {
	case r := <-done:
		if r.err != server.ErrNotReady {
			t.Fatalf("expected ErrNotReady after crash, got %v", r.err)
		}
		if r.fact.Evidence != "" {
			t.Fatalf("expected no fact after crash, got %q", r.fact.Evidence)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("WaitReadiness did not wake on crash")
	}
}

// A crash after readiness must not invalidate the recorded fact: the fact
// was proven while the process was alive.
func TestWaitReadinessAfterCrashKeepsFact(t *testing.T) {
	l := mustLauncher(t)
	proc := launchHelper(t, l, helperModeSleepExit)
	defer reapForTest(t, proc)

	waiter := proc.(server.ProcessReadinessWaiter)
	fact, err := waiter.WaitReadiness(context.Background())
	if err != nil {
		t.Fatalf("expected readiness, got error: %v", err)
	}
	if fact.Evidence != helperReadyLine {
		t.Fatalf("expected exact evidence %q, got %q", helperReadyLine, fact.Evidence)
	}

	// Stop the process, the fact must remain.
	stopper := proc.(server.ProcessStopper)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := stopper.Stop(ctx); err != nil {
		t.Fatalf("stop failed: %v", err)
	}
	got, err := proc.Readiness()
	if err != nil {
		t.Fatalf("readiness fact must survive terminal state: %v", err)
	}
	if got.Evidence != helperReadyLine {
		t.Fatalf("expected evidence %q, got %q", helperReadyLine, got.Evidence)
	}
	if !strings.HasPrefix(fact.Evidence, "hostexec-helper:") {
		t.Fatalf("evidence must come from the child stdout: %q", fact.Evidence)
	}
}
