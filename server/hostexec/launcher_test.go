package hostexec

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/signal"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/paralin/fraglands/server"
)

// ---------------------------------------------------------------------------
// helper process binary
//
// The test binary re-executes itself as the helper child. TestMain
// intercepts the re-execution when GO_WANT_HELPER_PROCESS=1 is set, so the
// helper needs no test flags on its argv and works with any launcher argv.
// Every race test runs against a real child process with no external
// dependency: the helper is the test binary itself.
// ---------------------------------------------------------------------------

const envHelperProcess = "GO_WANT_HELPER_PROCESS"

const (
	helperModeLongSleep = "long-sleep"
	helperModeSleepExit = "sleep-exit"
	helperModeExitCode  = "exit-code"
	helperModeSigterm   = "sigterm"
	helperModeSpin      = "spin"
	helperModeSilent    = "silent"
)

var helperModes = map[string]bool{
	helperModeLongSleep: true,
	helperModeSleepExit: true,
	helperModeExitCode:  true,
	helperModeSigterm:   true,
	helperModeSpin:      true,
	helperModeSilent:    true,
}

// helperReadyLine is the exact line the helpers print to prove readiness.
const helperReadyLine = "hostexec-helper: ready"

// helperExitCodeEnv carries the exit code the "exit-code" helper uses.
const helperExitCodeEnv = "HOSTEXEC_HELPER_EXIT_CODE"

// TestMain intercepts helper re-executions and otherwise runs the tests.
func TestMain(m *testing.M) {
	if os.Getenv(envHelperProcess) == "1" {
		os.Exit(runHelper())
	}
	os.Exit(m.Run())
}

// runHelper behaves according to HOSTEXEC_HELPER_MODE and exits. It never
// returns.
func runHelper() int {
	mode := os.Getenv("HOSTEXEC_HELPER_MODE")
	if !helperModes[mode] {
		fmt.Fprintf(os.Stderr, "unknown helper mode %q\n", mode)
		return 2
	}

	switch mode {
	case helperModeLongSleep:
		// Print ready, then hang until killed.
		fmt.Println(helperReadyLine)
		time.Sleep(30 * time.Second)
	case helperModeSleepExit:
		// Print ready, then exit cleanly: a crash by the contract.
		fmt.Println(helperReadyLine)
		time.Sleep(200 * time.Millisecond)
		return 0
	case helperModeExitCode:
		// Print ready, then exit with the requested non-zero code.
		fmt.Println(helperReadyLine)
		time.Sleep(200 * time.Millisecond)
		code := 0
		fmt.Sscanf(os.Getenv(helperExitCodeEnv), "%d", &code)
		return code
	case helperModeSigterm:
		// Print ready, trap SIGTERM, and exit 0 on SIGTERM: proves the
		// graceful signal reaches the child.
		fmt.Println(helperReadyLine)
		ch := make(chan os.Signal, 1)
		signal.Notify(ch, syscall.SIGTERM)
		<-ch
		return 0
	case helperModeSpin:
		// Ignore SIGTERM: forces Stop's kill escalation.
		fmt.Println(helperReadyLine)
		signal.Ignore(syscall.SIGTERM)
		time.Sleep(30 * time.Second)
	case helperModeSilent:
		// Print nothing and hang: the readiness line never arrives, so the
		// launcher's readiness timeout must crash-stop it.
		time.Sleep(30 * time.Second)
	}
	return 0
}

// ---------------------------------------------------------------------------
// test doubles and small helpers
// ---------------------------------------------------------------------------

// helperConfig returns a Config whose executable is this test binary,
// resolved through the allowlist.
func helperConfig(t *testing.T) Config {
	t.Helper()
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	return Config{
		Allowlist:    map[string]string{"test-helper": exe},
		Executable:   "test-helper",
		SpoolRoot:    t.TempDir(),
		ReadyLine:    helperReadyLine,
		ReadyTimeout: 10 * time.Second,
	}
}

// mustLauncher builds a validated launcher for the test binary helper.
func mustLauncher(t *testing.T) *Launcher {
	t.Helper()
	l, err := NewLauncher(helperConfig(t))
	if err != nil {
		t.Fatal(err)
	}
	return l
}

// testSpec returns an isolated spec under the launcher's spool root.
func testSpec(l *Launcher, generation uint64) server.ProcessSpec {
	return server.ProcessSpec{
		Generation: generation,
		Port:       int(9000 + generation),
		SpoolDir:   fmt.Sprintf("%s/gen-%d", l.spoolRoot, generation),
	}
}

// launchHelper launches the helper in one mode with a per-test spec. The
// helper mode travels by environment: the launcher passes the child its
// environment through unchanged plus the FRAGLANDS_* identity, and the
// parent's own environment already carries the helper variables.
func launchHelper(t *testing.T, l *Launcher, mode string) server.Process {
	t.Helper()
	t.Setenv(envHelperProcess, "1")
	t.Setenv("HOSTEXEC_HELPER_MODE", mode)
	proc, err := l.Launch(context.Background(), testSpec(l, 1))
	if err != nil {
		t.Fatal(err)
	}
	return proc
}

// launchHelperWithEnv launches the helper with extra child environment.
func launchHelperWithEnv(t *testing.T, l *Launcher, mode string, kv ...string) server.Process {
	t.Helper()
	t.Setenv(envHelperProcess, "1")
	t.Setenv("HOSTEXEC_HELPER_MODE", mode)
	for i := 0; i+1 < len(kv); i += 2 {
		t.Setenv(kv[i], kv[i+1])
	}
	proc, err := l.Launch(context.Background(), testSpec(l, 1))
	if err != nil {
		t.Fatal(err)
	}
	return proc
}

// waitReady waits for the readiness fact, failing the test on timeout.
func waitReady(t *testing.T, proc server.Process, d time.Duration) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), d)
	defer cancel()
	for {
		if _, err := proc.Readiness(); err == nil {
			return
		}
		select {
		case <-ctx.Done():
			for _, a := range proc.Artifacts() {
				t.Logf("artifact %s: %q", a.Name, a.Data)
			}
			if reason, err := proc.(interface {
				CrashReason() (server.CrashReason, error)
			}).CrashReason(); err == nil {
				t.Logf("crash reason: %s: %s", reason.Code, reason.Message)
			}
			t.Fatalf("readiness not proven within %s (state %s)", d, proc.State())
		case <-time.After(10 * time.Millisecond):
		}
	}
}

// reapForTest forces a terminal state during cleanup.
func reapForTest(t *testing.T, proc server.Process) {
	t.Helper()
	if proc == nil {
		return
	}
	if proc.State().Terminal() {
		return
	}
	stopper, ok := proc.(server.ProcessStopper)
	if !ok {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := stopper.Stop(ctx); err != nil && !proc.State().Terminal() {
		t.Errorf("cleanup stop failed: %v", err)
	}
}

// ---------------------------------------------------------------------------
// launcher construction
// ---------------------------------------------------------------------------

func TestNewLauncherValidation(t *testing.T) {
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	base := func() Config {
		return Config{
			Allowlist:    map[string]string{"helper": exe},
			Executable:   "helper",
			SpoolRoot:    t.TempDir(),
			ReadyLine:    helperReadyLine,
			ReadyTimeout: 10 * time.Second,
		}
	}
	if _, err := NewLauncher(Config{}); err == nil {
		t.Error("empty config accepted")
	}
	if _, err := NewLauncher(Config{Executable: "helper", SpoolRoot: t.TempDir(), ReadyLine: "x", ReadyTimeout: time.Second}); err == nil {
		t.Error("missing allowlist accepted")
	}
	cfg := base()
	cfg.Allowlist = map[string]string{"other": exe}
	if _, err := NewLauncher(cfg); err == nil {
		t.Error("executable outside the allowlist accepted")
	}
	cfg = base()
	cfg.Allowlist = map[string]string{"helper": "helper"}
	if _, err := NewLauncher(cfg); err == nil {
		t.Error("relative allowlist path accepted")
	}
	cfg = base()
	cfg.Allowlist = map[string]string{"helper": "/nonexistent/hostexec/helper"}
	if _, err := NewLauncher(cfg); err == nil {
		t.Error("nonexistent allowlist path accepted")
	}
	cfg = base()
	cfg.ReadyLine = ""
	if _, err := NewLauncher(cfg); err == nil {
		t.Error("empty readiness line accepted")
	}
	cfg = base()
	cfg.ReadyTimeout = 0
	if _, err := NewLauncher(cfg); err == nil {
		t.Error("zero readiness timeout accepted")
	}
	cfg = base()
	cfg.StopGracePeriod = -1
	if _, err := NewLauncher(cfg); err == nil {
		t.Error("negative stop grace period accepted")
	}
	cfg = base()
	cfg.MaxStdoutBytes = -1
	if _, err := NewLauncher(cfg); err == nil {
		t.Error("negative stream bound accepted")
	}
	if _, err := NewLauncher(base()); err != nil {
		t.Fatalf("valid config refused: %v", err)
	}
}

// ---------------------------------------------------------------------------
// launch and readiness
// ---------------------------------------------------------------------------

func TestLaunchRefusesBadSpec(t *testing.T) {
	l := mustLauncher(t)
	spec := testSpec(l, 1)
	spec.Generation = 0
	if _, err := l.Launch(context.Background(), spec); err == nil {
		t.Fatal("zero generation accepted")
	}
}

func TestLaunchReadinessFromExactLine(t *testing.T) {
	l := mustLauncher(t)
	proc := launchHelper(t, l, helperModeLongSleep)
	defer reapForTest(t, proc)

	if proc.State() != server.ProcessStateLaunching {
		t.Fatalf("expected launching right after start, got %s", proc.State())
	}
	if _, err := proc.Readiness(); !errors.Is(err, server.ErrNoReadinessFact) {
		t.Fatalf("expected ErrNoReadinessFact before proof, got %v", err)
	}
	waitReady(t, proc, 10*time.Second)
	if state := proc.State(); state != server.ProcessStateReady {
		for _, a := range proc.Artifacts() {
			t.Logf("artifact %s: %q", a.Name, a.Data)
		}
		t.Fatalf("expected ready, got %s", state)
	}
	fact, err := proc.Readiness()
	if err != nil {
		t.Fatal(err)
	}
	if fact.Evidence != helperReadyLine {
		t.Fatalf("expected evidence %q, got %q", helperReadyLine, fact.Evidence)
	}
}

func TestLaunchReadinessTimeoutCrashStops(t *testing.T) {
	l := mustLauncher(t)
	l.readyTimeout = 300 * time.Millisecond
	proc := launchHelper(t, l, helperModeSilent)
	defer reapForTest(t, proc)

	state, err := proc.WaitTerminal(waitCtx(5 * time.Second))
	if err != nil {
		t.Fatalf("terminal wait: %v", err)
	}
	if state != server.ProcessStateCrashed {
		t.Fatalf("expected crashed, got %s", state)
	}
	if _, err := proc.Readiness(); !errors.Is(err, server.ErrNoReadinessFact) {
		t.Fatalf("readiness must never be recorded without proof, got %v", err)
	}
	reason, err := proc.(*LaunchedProcess).CrashReason()
	if err != nil {
		t.Fatal(err)
	}
	if reason.Code != CrashReadinessTimeout {
		t.Fatalf("expected %s, got %s", CrashReadinessTimeout, reason.Code)
	}
}

// ---------------------------------------------------------------------------
// crash-stop
// ---------------------------------------------------------------------------

func TestCleanExitWithoutStopIsCrash(t *testing.T) {
	l := mustLauncher(t)
	proc := launchHelper(t, l, helperModeSleepExit)
	defer reapForTest(t, proc)

	state, err := proc.WaitTerminal(waitCtx(5 * time.Second))
	if err != nil {
		t.Fatalf("terminal wait: %v", err)
	}
	if state != server.ProcessStateCrashed {
		t.Fatalf("expected crashed, got %s", state)
	}
	reason, err := proc.(*LaunchedProcess).CrashReason()
	if err != nil {
		t.Fatal(err)
	}
	if reason.Code != CrashExitUnexpected {
		t.Fatalf("expected %s, got %s", CrashExitUnexpected, reason.Code)
	}
}

func TestNonZeroExitIsTypedCrash(t *testing.T) {
	l := mustLauncher(t)
	proc := launchHelperWithEnv(t, l, helperModeExitCode, helperExitCodeEnv, "3")
	defer reapForTest(t, proc)

	state, err := proc.WaitTerminal(waitCtx(5 * time.Second))
	if err != nil {
		t.Fatalf("terminal wait: %v", err)
	}
	if state != server.ProcessStateCrashed {
		t.Fatalf("expected crashed, got %s", state)
	}
	reason, err := proc.(*LaunchedProcess).CrashReason()
	if err != nil {
		t.Fatal(err)
	}
	if reason.Code != CrashExitNonZero {
		t.Fatalf("expected %s, got %s", CrashExitNonZero, reason.Code)
	}
}

// ---------------------------------------------------------------------------
// stop
// ---------------------------------------------------------------------------

func TestStopReachesTerminalStopped(t *testing.T) {
	l := mustLauncher(t)
	proc := launchHelper(t, l, helperModeLongSleep)
	defer reapForTest(t, proc)

	waitReady(t, proc, 5*time.Second)
	if err := proc.(server.ProcessStopper).Stop(context.Background()); err != nil {
		t.Fatalf("stop: %v", err)
	}
	if state := proc.State(); state != server.ProcessStateStopped {
		t.Fatalf("expected stopped, got %s", state)
	}
	// Stop on a stopped process is idempotent.
	if err := proc.(server.ProcessStopper).Stop(context.Background()); err != nil {
		t.Fatalf("stop on stopped process: %v", err)
	}
}

func TestStopDeliversGracefulSignal(t *testing.T) {
	l := mustLauncher(t)
	proc := launchHelper(t, l, helperModeSigterm)
	defer reapForTest(t, proc)

	waitReady(t, proc, 5*time.Second)
	if err := proc.(server.ProcessStopper).Stop(context.Background()); err != nil {
		t.Fatalf("stop: %v", err)
	}
	if state := proc.State(); state != server.ProcessStateStopped {
		t.Fatalf("expected stopped, got %s", state)
	}
}

func TestStopEscalatesToKill(t *testing.T) {
	l := mustLauncher(t)
	l.stopGrace = 300 * time.Millisecond
	proc := launchHelper(t, l, helperModeSpin)
	defer reapForTest(t, proc)

	waitReady(t, proc, 5*time.Second)
	if err := proc.(server.ProcessStopper).Stop(context.Background()); err != nil {
		t.Fatalf("stop: %v", err)
	}
	if state := proc.State(); state != server.ProcessStateStopped {
		t.Fatalf("expected stopped, got %s", state)
	}
}

func TestStopCancelledContextStillRecordsTerminal(t *testing.T) {
	l := mustLauncher(t)
	l.stopGrace = 200 * time.Millisecond
	proc := launchHelper(t, l, helperModeSpin)
	defer reapForTest(t, proc)

	waitReady(t, proc, 5*time.Second)
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	if err := proc.(server.ProcessStopper).Stop(ctx); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("expected context deadline exceeded, got %v", err)
	}
	// The kill escalation fired before returning; the reaper finishes the
	// job. Wait for the terminal state.
	state, err := proc.WaitTerminal(waitCtx(5 * time.Second))
	if err != nil {
		t.Fatalf("terminal wait: %v", err)
	}
	if state != server.ProcessStateStopped {
		t.Fatalf("expected stopped, got %s", state)
	}
}

// ---------------------------------------------------------------------------
// spool isolation
// ---------------------------------------------------------------------------

func TestLaunchCreatesPrivateSpoolDir(t *testing.T) {
	l := mustLauncher(t)
	spec := testSpec(l, 1)
	t.Setenv(envHelperProcess, "1")
	t.Setenv("HOSTEXEC_HELPER_MODE", helperModeLongSleep)
	proc, err := l.Launch(context.Background(), spec)
	if err != nil {
		t.Fatal(err)
	}
	defer reapForTest(t, proc)

	info, err := os.Stat(spec.SpoolDir)
	if err != nil {
		t.Fatal(err)
	}
	if !info.IsDir() {
		t.Fatalf("spool dir is not a directory: %s", spec.SpoolDir)
	}
	waitReady(t, proc, 5*time.Second)
	if err := proc.(server.ProcessStopper).Stop(context.Background()); err != nil {
		t.Fatalf("stop: %v", err)
	}
}

func TestLaunchRefusesTraversalSpool(t *testing.T) {
	l := mustLauncher(t)
	root := l.spoolRoot
	cases := []struct {
		name string
		dir  string
	}{
		{"parent-relative", root + "/../escape"},
		{"relative", "relative/gen-1"},
		{"root itself", root},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			spec := server.ProcessSpec{Generation: 1, Port: 9000, SpoolDir: tc.dir}
			if _, err := l.Launch(context.Background(), spec); err == nil {
				t.Fatal("expected traversal refusal")
			}
		})
	}
}

func TestLaunchRefusesSymlinkSpoolEscape(t *testing.T) {
	l := mustLauncher(t)
	root := l.spoolRoot
	outside := t.TempDir()
	if err := os.Symlink(outside, root+"/link"); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	spec := server.ProcessSpec{Generation: 1, Port: 9000, SpoolDir: root + "/link/gen-1"}
	if _, err := l.Launch(context.Background(), spec); err == nil {
		t.Fatal("expected symlink escape refusal")
	}
}

func TestLaunchRefusesExistingSpoolDir(t *testing.T) {
	l := mustLauncher(t)
	spec := testSpec(l, 1)
	if err := os.MkdirAll(spec.SpoolDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if _, err := l.Launch(context.Background(), spec); err == nil {
		t.Fatal("expected refusal when the spool dir already exists")
	}
}

func TestFailedLaunchLeavesNoSpoolDir(t *testing.T) {
	l := mustLauncher(t)
	spec := testSpec(l, 1)
	// Point the launcher at a directory: the allowlist check ran at
	// construction, so this simulates an executable that vanished or broke
	// between construction and launch.
	l.execPath = t.TempDir()
	if _, err := l.Launch(context.Background(), spec); err == nil {
		t.Fatal("expected launch failure for a broken executable")
	}
	if _, err := os.Stat(spec.SpoolDir); !os.IsNotExist(err) {
		t.Fatalf("spool dir left behind by a failed launch: %v", err)
	}
}

// ---------------------------------------------------------------------------
// artifact capture bounds
// ---------------------------------------------------------------------------

func TestStreamArtifactsDelivered(t *testing.T) {
	l := mustLauncher(t)
	l.maxStdoutBytes = 512
	l.maxStderrBytes = 256
	proc := launchHelper(t, l, helperModeLongSleep)
	defer reapForTest(t, proc)

	waitReady(t, proc, 5*time.Second)
	if err := proc.(server.ProcessStopper).Stop(context.Background()); err != nil {
		t.Fatalf("stop: %v", err)
	}
	arts := proc.Artifacts()
	if len(arts) == 0 {
		t.Fatal("expected stream artifacts after exit")
	}
	byName := map[string][]byte{}
	for _, a := range arts {
		byName[a.Name] = a.Data
	}
	data, ok := byName[stdoutArtifactName]
	if !ok {
		t.Fatalf("missing artifact %q; got %v", stdoutArtifactName, artifactNames(arts))
	}
	if !bytes.Contains(data, []byte(helperReadyLine)) {
		t.Fatalf("artifact %q missing %q: %q", stdoutArtifactName, helperReadyLine, data)
	}
	if len(data) > 512 {
		t.Fatalf("artifact %q exceeds its bound: %d bytes", stdoutArtifactName, len(data))
	}
}

func TestBoundedBufferTruncates(t *testing.T) {
	b := newBoundedBuffer(16)
	for i := 0; i < 100; i++ {
		b.writeLine("0123456789")
	}
	data := b.snapshot()
	if len(data) > 16 {
		t.Fatalf("buffer exceeded its bound: %d bytes", len(data))
	}
	if !b.overflowed {
		t.Fatal("overflow not flagged")
	}
}

// ---------------------------------------------------------------------------
// race tests
// ---------------------------------------------------------------------------

func TestRaceConcurrentStarts(t *testing.T) {
	l := mustLauncher(t)
	t.Setenv(envHelperProcess, "1")
	t.Setenv("HOSTEXEC_HELPER_MODE", helperModeLongSleep)

	const workers = 8
	procs := make([]server.Process, workers)
	var wg sync.WaitGroup
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			spec := testSpec(l, uint64(i+1))
			p, err := l.Launch(context.Background(), spec)
			if err != nil {
				t.Error(err)
				return
			}
			procs[i] = p
		}(i)
	}
	wg.Wait()
	for _, p := range procs {
		if p == nil {
			continue
		}
		waitReady(t, p, 5*time.Second)
		if err := p.(server.ProcessStopper).Stop(context.Background()); err != nil {
			t.Errorf("stop: %v", err)
		}
	}
}

func TestRaceConcurrentReadinessAndStateReads(t *testing.T) {
	l := mustLauncher(t)
	proc := launchHelper(t, l, helperModeLongSleep)
	defer reapForTest(t, proc)

	stop := make(chan struct{})
	var wg sync.WaitGroup
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				select {
				case <-stop:
					return
				default:
				}
				_ = proc.State()
				_, _ = proc.Readiness()
				_ = proc.Artifacts()
			}
		}()
	}
	waitReady(t, proc, 5*time.Second)
	close(stop)
	wg.Wait()
	if err := proc.(server.ProcessStopper).Stop(context.Background()); err != nil {
		t.Fatalf("stop: %v", err)
	}
}

func TestRaceConcurrentStops(t *testing.T) {
	l := mustLauncher(t)
	proc := launchHelper(t, l, helperModeLongSleep)
	defer reapForTest(t, proc)

	waitReady(t, proc, 5*time.Second)
	const stoppers = 4
	var wg sync.WaitGroup
	var mu sync.Mutex
	var effective int
	for i := 0; i < stoppers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			err := proc.(server.ProcessStopper).Stop(context.Background())
			mu.Lock()
			defer mu.Unlock()
			switch {
			case err == nil:
				effective++
			default:
				t.Errorf("unexpected stop error: %v", err)
			}
		}()
	}
	wg.Wait()
	if effective != stoppers {
		t.Fatalf("expected all %d stops to report the stopped outcome, got %d", stoppers, effective)
	}
	if state := proc.State(); state != server.ProcessStateStopped {
		t.Fatalf("expected stopped, got %s", state)
	}
}

func TestRaceCrashDuringStop(t *testing.T) {
	l := mustLauncher(t)
	proc := launchHelper(t, l, helperModeSleepExit)
	defer reapForTest(t, proc)

	waitReady(t, proc, 5*time.Second)
	// The helper exits on its own ~200ms after ready. A stop racing that
	// exit must leave exactly one terminal state: Stop wins (Stopped) or
	// loses cleanly (typed crash refusal).
	err := proc.(server.ProcessStopper).Stop(context.Background())
	state := proc.State()
	switch {
	case err == nil:
		if state != server.ProcessStateStopped {
			t.Fatalf("expected stopped after successful stop, got %s", state)
		}
	case errors.Is(err, server.ErrCrashed):
		if state != server.ProcessStateCrashed {
			t.Fatalf("expected crashed after crash refusal, got %s", state)
		}
	default:
		t.Fatalf("unexpected stop error: %v", err)
	}
}

func TestRaceStopBeforeReadiness(t *testing.T) {
	l := mustLauncher(t)
	proc := launchHelper(t, l, helperModeLongSleep)
	defer reapForTest(t, proc)

	// Stop before the readiness watcher runs: the stop must still reach a
	// terminal state and never record readiness after the stop request.
	if err := proc.(server.ProcessStopper).Stop(context.Background()); err != nil {
		t.Fatalf("stop: %v", err)
	}
	if state := proc.State(); state != server.ProcessStateStopped {
		t.Fatalf("expected stopped, got %s", state)
	}
	if _, err := proc.Readiness(); !errors.Is(err, server.ErrNoReadinessFact) {
		t.Fatalf("readiness must not be recorded for a stopped process, got %v", err)
	}
}

func TestRaceConcurrentWaitTerminal(t *testing.T) {
	l := mustLauncher(t)
	proc := launchHelperWithEnv(t, l, helperModeExitCode, helperExitCodeEnv, "7")
	defer reapForTest(t, proc)

	const waiters = 6
	var wg sync.WaitGroup
	for i := 0; i < waiters; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			state, err := proc.WaitTerminal(waitCtx(5 * time.Second))
			if err != nil {
				t.Error(err)
				return
			}
			if state != server.ProcessStateCrashed {
				t.Errorf("expected crashed, got %s", state)
			}
		}()
	}
	wg.Wait()
}

// ---------------------------------------------------------------------------
// small helpers
// ---------------------------------------------------------------------------

// waitCtx returns a context that expires after d.
func waitCtx(d time.Duration) context.Context {
	ctx, cancel := context.WithTimeout(context.Background(), d)
	_ = cancel // expires on its own; tests are short-lived
	return ctx
}

// artifactNames returns the artifact names for a readable failure message.
func artifactNames(arts []server.Artifact) string {
	out := make([]string, 0, len(arts))
	for _, a := range arts {
		out = append(out, a.Name)
	}
	return strings.Join(out, ",")
}
