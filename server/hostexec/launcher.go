// Package hostexec implements the server.ProcessLauncher contract with real
// local processes. It is the worker-side seam between the Supervisor and a
// host such as a Windows modlock-host deployment:
//
//   - argv-safe exec: the child is started with os/exec, Path set to a
//     validated absolute path, and argv built directly. There is no shell
//     anywhere in the launch path.
//   - explicit allowlist: Launch refuses any executable that is not resolved
//     through the configured allowlist of absolute paths, each validated to
//     exist as a regular executable file at construction time.
//   - isolation passed to the child: the generation, port, and spool dir of
//     the spec are passed as argv flags and as FRAGLANDS_* environment
//     variables; nothing else identifies the process instance.
//   - private spool: Launch creates the spec's spool directory with
//     owner-only permissions and refuses any spool path that escapes the
//     configured spool root, including through symbolic links.
//   - bounded capture: stdout and stderr are captured into artifacts bounded
//     by a configured byte limit; a runaway child cannot exhaust the host.
//   - exact readiness: readiness is proven only when the child's stdout
//     contains the exact configured line. It is never assumed, inferred from
//     uptime, or taken from a successful exec.
//   - crash-stop: an unexpected exit is a typed crash with no partial state
//     and no auto-restart. Stop tears the process group down (process group
//     on Unix, process termination on Windows, as the platform permits) and
//     marks the process Stopped exactly once.
//
// The package never invokes Steam, never serves a public endpoint, and is
// constructed only through an explicit deployment decision: there is no
// default constructor wired into any binary in this repository.
package hostexec

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/paralin/fraglands/server"
)

// Defaults for optional configuration values.
const (
	// DefaultMaxStreamBytes bounds one captured stream when the config
	// leaves the bound at zero. It matches the spool artifact bound.
	DefaultMaxStreamBytes = 64 << 10
	// DefaultStopGracePeriod is the SIGTERM-to-SIGKILL window on Unix.
	DefaultStopGracePeriod = 5 * time.Second
	// stdoutArtifactName and stderrArtifactName are the artifact names the
	// launcher delivers on exit.
	stdoutArtifactName = "stdout.log"
	stderrArtifactName = "stderr.log"
)

// Environment variables through which the child receives its isolation
// identity. The same facts are also passed as argv flags.
const (
	EnvGeneration = "FRAGLANDS_GENERATION"
	EnvPort       = "FRAGLANDS_PORT"
	EnvSpoolDir   = "FRAGLANDS_SPOOL_DIR"
)

// Typed crash reason codes recorded by this launcher. A crashed process
// carries exactly one of these codes.
const (
	// CrashExitNonZero is recorded when the child exited with a non-zero
	// status without being asked to stop.
	CrashExitNonZero = "exit_nonzero"
	// CrashExitUnexpected is recorded when the child exited cleanly without
	// being asked to stop: a server that leaves on its own is a crash-stop,
	// not a success.
	CrashExitUnexpected = "exit_unexpected"
	// CrashSignaled is recorded when the child was killed by a signal this
	// launcher did not send as part of a deliberate stop.
	CrashSignaled = "signaled"
	// CrashReadinessTimeout is recorded when the child failed to print the
	// exact readiness line within the configured timeout.
	CrashReadinessTimeout = "readiness_timeout"
)

// Errors returned by the launcher. Callers match with errors.Is.
var (
	// ErrInvalidConfig is returned when the launcher configuration is
	// incomplete or unsafe.
	ErrInvalidConfig = errors.New("hostexec: invalid launcher config")
	// ErrExecutableNotAllowed is returned when the configured executable is
	// not resolved through the allowlist or fails validation.
	ErrExecutableNotAllowed = errors.New("hostexec: executable not allowed")
	// ErrSpoolTraversal is returned when a spool dir escapes the spool root.
	ErrSpoolTraversal = errors.New("hostexec: spool dir escapes the spool root")
	// ErrSpoolExists is returned when the spool dir for a fresh generation
	// already exists: stale state must never be reused.
	ErrSpoolExists = errors.New("hostexec: spool dir already exists")
	// ErrStopInProgress is returned when a second concurrent Stop is
	// attempted while the first teardown is still running.
	ErrStopInProgress = errors.New("hostexec: stop already in progress")
)

// Config is the explicit, default-off launcher configuration. Every field is
// required unless documented otherwise; NewLauncher refuses an incomplete or
// unsafe config instead of guessing a default executable or readiness line.
type Config struct {
	// Allowlist maps executable names to absolute paths. Launch refuses any
	// executable not resolved through this map. Every path must be absolute
	// and must exist as a regular executable file at construction time.
	Allowlist map[string]string
	// Executable is the allowlist key of the process to launch.
	Executable string
	// SpoolRoot is the root directory under which every spool dir must
	// live. It is created owner-only if missing.
	SpoolRoot string
	// ReadyLine is the exact stdout line that proves readiness. It is
	// matched byte-for-byte (after stripping one trailing carriage return
	// for CRLF writers); a prefix, a substring, or a similar line proves
	// nothing.
	ReadyLine string
	// ReadyTimeout bounds the wait for the readiness line. A child that
	// never prints the line is crash-stopped with
	// CrashReadinessTimeout. Required and must be positive.
	ReadyTimeout time.Duration
	// StopGracePeriod bounds the graceful-signal-to-kill window during
	// Stop. Zero uses DefaultStopGracePeriod; negative is refused. On
	// Windows there is no graceful terminate, so the window only bounds the
	// wait for the killed process.
	StopGracePeriod time.Duration
	// MaxStdoutBytes bounds the captured stdout artifact. Zero uses
	// DefaultMaxStreamBytes; negative is refused. Capture stops at the
	// bound; the child keeps running.
	MaxStdoutBytes int
	// MaxStderrBytes bounds the captured stderr artifact. Zero uses
	// DefaultMaxStreamBytes; negative is refused.
	MaxStderrBytes int
}

// Launcher launches server processes as real local child processes. It
// implements server.ProcessLauncher. It is safe for concurrent use.
type Launcher struct {
	execPath       string
	spoolRoot      string
	readyLine      string
	readyTimeout   time.Duration
	stopGrace      time.Duration
	maxStdoutBytes int
	maxStderrBytes int
}

// Compile-time contract assertions.
var (
	_ server.ProcessLauncher = (*Launcher)(nil)
	_ server.Process         = (*LaunchedProcess)(nil)
	_ server.ProcessStopper  = (*LaunchedProcess)(nil)
)

// NewLauncher validates the configuration and constructs the launcher. It is
// default-off: nothing in this repository constructs it, and a deployment
// must pass an explicit allowlist, readiness line, and timeout.
func NewLauncher(cfg Config) (*Launcher, error) {
	if len(cfg.Allowlist) == 0 {
		return nil, fmt.Errorf("%w: allowlist is required", ErrInvalidConfig)
	}
	if cfg.Executable == "" {
		return nil, fmt.Errorf("%w: executable is required", ErrInvalidConfig)
	}
	path, ok := cfg.Allowlist[cfg.Executable]
	if !ok {
		return nil, fmt.Errorf("%w: %q is not in the allowlist", ErrExecutableNotAllowed, cfg.Executable)
	}
	if !filepath.IsAbs(path) {
		return nil, fmt.Errorf("%w: %q is not an absolute path", ErrExecutableNotAllowed, path)
	}
	clean := filepath.Clean(path)
	info, err := os.Stat(clean)
	if err != nil {
		return nil, fmt.Errorf("%w: %q is not accessible: %v", ErrExecutableNotAllowed, clean, err)
	}
	if !info.Mode().IsRegular() {
		return nil, fmt.Errorf("%w: %q is not a regular file", ErrExecutableNotAllowed, clean)
	}
	if err := validateExecBit(info); err != nil {
		return nil, fmt.Errorf("%w: %q: %v", ErrExecutableNotAllowed, clean, err)
	}
	if cfg.SpoolRoot == "" {
		return nil, fmt.Errorf("%w: spool root is required", ErrInvalidConfig)
	}
	if !filepath.IsAbs(cfg.SpoolRoot) {
		return nil, fmt.Errorf("%w: spool root %q is not absolute", ErrInvalidConfig, cfg.SpoolRoot)
	}
	if err := os.MkdirAll(filepath.Clean(cfg.SpoolRoot), 0o700); err != nil {
		return nil, fmt.Errorf("%w: cannot create spool root: %v", ErrInvalidConfig, err)
	}
	if cfg.ReadyLine == "" {
		return nil, fmt.Errorf("%w: readiness line is required", ErrInvalidConfig)
	}
	if cfg.ReadyTimeout <= 0 {
		return nil, fmt.Errorf("%w: readiness timeout must be positive", ErrInvalidConfig)
	}
	if cfg.StopGracePeriod < 0 {
		return nil, fmt.Errorf("%w: stop grace period must not be negative", ErrInvalidConfig)
	}
	grace := cfg.StopGracePeriod
	if grace == 0 {
		grace = DefaultStopGracePeriod
	}
	if cfg.MaxStdoutBytes < 0 || cfg.MaxStderrBytes < 0 {
		return nil, fmt.Errorf("%w: stream bounds must not be negative", ErrInvalidConfig)
	}
	maxOut := cfg.MaxStdoutBytes
	if maxOut == 0 {
		maxOut = DefaultMaxStreamBytes
	}
	maxErr := cfg.MaxStderrBytes
	if maxErr == 0 {
		maxErr = DefaultMaxStreamBytes
	}
	return &Launcher{
		execPath:       clean,
		spoolRoot:      filepath.Clean(cfg.SpoolRoot),
		readyLine:      cfg.ReadyLine,
		readyTimeout:   cfg.ReadyTimeout,
		stopGrace:      grace,
		maxStdoutBytes: maxOut,
		maxStderrBytes: maxErr,
	}, nil
}

// Launch starts one server process for the spec and returns it in the
// Launching state. Readiness is proven separately by the launcher's watcher
// when the child prints the exact configured line. A failed launch leaves no
// spool directory behind.
func (l *Launcher) Launch(ctx context.Context, spec server.ProcessSpec) (server.Process, error) {
	if err := spec.Validate(); err != nil {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("hostexec: launch context expired: %w", err)
	}

	spoolDir, err := l.createSpoolDir(spec.SpoolDir)
	if err != nil {
		return nil, err
	}

	lp := newLaunchedProcess(spec, l)
	if err := lp.start(spoolDir); err != nil {
		// The child never came up: remove the spool dir we created so the
		// failed launch leaves no trace.
		_ = os.Remove(spoolDir)
		return nil, err
	}
	lp.runWatchers()
	return lp, nil
}

// createSpoolDir validates the spec's spool dir against the configured root,
// refuses traversal (including through symbolic links), and creates the
// directory owner-only. A fresh generation's spool dir must not already
// exist: stale state is never reused.
func (l *Launcher) createSpoolDir(spoolDir string) (string, error) {
	if !filepath.IsAbs(spoolDir) {
		return "", fmt.Errorf("%w: %q is not absolute", ErrSpoolTraversal, spoolDir)
	}
	dir := filepath.Clean(spoolDir)
	root := l.spoolRoot
	if err := relUnderRoot(root, dir); err != nil {
		return "", err
	}
	if _, err := os.Stat(dir); err == nil {
		return "", fmt.Errorf("%w: %s", ErrSpoolExists, dir)
	} else if !os.IsNotExist(err) {
		return "", fmt.Errorf("hostexec: cannot inspect spool dir: %w", err)
	}
	if err := os.Mkdir(dir, 0o700); err != nil {
		return "", fmt.Errorf("hostexec: cannot create spool dir: %w", err)
	}
	// Re-check after creation: a symbolic link planted between the string
	// check and the mkdir must not survive. Resolve both ends and require
	// the resolved dir to stay under the resolved root.
	resolvedDir, err := filepath.EvalSymlinks(dir)
	if err != nil {
		_ = os.Remove(dir)
		return "", fmt.Errorf("hostexec: cannot resolve spool dir: %w", err)
	}
	resolvedRoot, err := filepath.EvalSymlinks(root)
	if err != nil {
		_ = os.Remove(dir)
		return "", fmt.Errorf("hostexec: cannot resolve spool root: %w", err)
	}
	if err := relUnderRoot(resolvedRoot, resolvedDir); err != nil {
		_ = os.Remove(dir)
		return "", err
	}
	return dir, nil
}

// relUnderRoot refuses any dir that is not strictly under root.
func relUnderRoot(root, dir string) error {
	rel, err := filepath.Rel(root, dir)
	if err != nil {
		return fmt.Errorf("%w: %q is not under %q: %v", ErrSpoolTraversal, dir, root, err)
	}
	if rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return fmt.Errorf("%w: %q escapes %q", ErrSpoolTraversal, dir, root)
	}
	if rel == "." {
		return fmt.Errorf("%w: spool dir must not be the spool root itself", ErrSpoolTraversal)
	}
	return nil
}

// LaunchedProcess is one launched child process. It implements
// server.Process and server.ProcessStopper. The launcher's watcher and
// reaper goroutines drive the state machine; Stop requests a deliberate
// teardown. It is safe for concurrent use.
type LaunchedProcess struct {
	spec server.ProcessSpec

	// launcher configuration snapshots.
	execPath       string
	readyLine      string
	readyTimeout   time.Duration
	stopGrace      time.Duration
	maxStdoutBytes int
	maxStderrBytes int

	// mtx guards the state machine fields below.
	mtx sync.Mutex
	// state is the current lifecycle state.
	state server.ProcessState
	// fact is the recorded readiness fact, set exactly once.
	fact *server.ReadinessFact
	// reason is the recorded crash reason, set exactly once.
	reason *server.CrashReason
	// artifacts are the artifacts delivered by the reaper.
	artifacts []server.Artifact
	// stopRequested records that a deliberate stop was requested.
	stopRequested bool
	// pendingCrash is the typed crash reason the readiness watcher decided
	// on (e.g. readiness timeout) before the reaper observed the exit.
	pendingCrash *server.CrashReason

	// waitCh is closed exactly once when the state reaches a terminal
	// state.
	waitCh chan struct{}
	// done is closed by the reaper after the child exited and the terminal
	// state was recorded.
	done chan struct{}
	// stopOnce guards the graceful-signal step of Stop.
	stopOnce sync.Once

	// cmd is the child command. Set at start, read-only afterwards.
	cmd *exec.Cmd
	// stdoutPR, stdoutPW and stderrPR, stderrPW are the pipe halves the
	// child's output is copied through. The reaper closes the writers; the
	// capture watchers own the readers.
	stdoutPR *io.PipeReader
	stdoutPW *io.PipeWriter
	stderrPR *io.PipeReader
	stderrPW *io.PipeWriter
	// stdoutBuf and stderrBuf are the bounded capture buffers.
	stdoutBuf *boundedBuffer
	stderrBuf *boundedBuffer
	// streamWG tracks the capture watchers.
	streamWG sync.WaitGroup
}

// newLaunchedProcess builds the process handle in the Launching state.
func newLaunchedProcess(spec server.ProcessSpec, l *Launcher) *LaunchedProcess {
	return &LaunchedProcess{
		spec:           spec,
		execPath:       l.execPath,
		readyLine:      l.readyLine,
		readyTimeout:   l.readyTimeout,
		stopGrace:      l.stopGrace,
		maxStdoutBytes: l.maxStdoutBytes,
		maxStderrBytes: l.maxStderrBytes,
		state:          server.ProcessStateLaunching,
		waitCh:         make(chan struct{}),
		done:           make(chan struct{}),
	}
}

// start builds and starts the child command. On failure the handle is left
// in the Launching state and the caller removes the spool dir.
func (p *LaunchedProcess) start(spoolDir string) error {
	// argv-safe: Path is the validated absolute path, args are passed
	// directly as argv, and no shell is involved anywhere.
	args := []string{
		fmt.Sprintf("--generation=%d", p.spec.Generation),
		fmt.Sprintf("--port=%d", p.spec.Port),
		"--spool-dir=" + spoolDir,
	}
	cmd := exec.Command(p.execPath, args...)
	// The isolation identity is also passed in the environment: a child
	// that does not parse flags can still read its spec facts.
	cmd.Env = append(os.Environ(),
		EnvGeneration+"="+fmt.Sprintf("%d", p.spec.Generation),
		EnvPort+"="+fmt.Sprintf("%d", p.spec.Port),
		EnvSpoolDir+"="+spoolDir,
	)
	cmd.SysProcAttr = sysProcAttr()
	// Bound the wait for unresponsive pipe copies after the child dies.
	cmd.WaitDelay = p.stopGrace

	stdoutPR, stdoutPW := io.Pipe()
	stderrPR, stderrPW := io.Pipe()
	cmd.Stdout = stdoutPW
	cmd.Stderr = stderrPW

	if err := cmd.Start(); err != nil {
		stdoutPR.Close()
		stdoutPW.Close()
		stderrPR.Close()
		stderrPW.Close()
		return fmt.Errorf("hostexec: cannot start %q: %w", p.execPath, err)
	}
	p.cmd = cmd
	p.stdoutPR = stdoutPR
	p.stdoutPW = stdoutPW
	p.stderrPR = stderrPR
	p.stderrPW = stderrPW
	p.stdoutBuf = newBoundedBuffer(p.maxStdoutBytes)
	p.stderrBuf = newBoundedBuffer(p.maxStderrBytes)
	return nil
}

// runWatchers starts the capture watchers, the readiness watcher, and the
// reaper. It is called exactly once after a successful start.
func (p *LaunchedProcess) runWatchers() {
	readyCh := make(chan string, 1)
	p.streamWG.Add(2)
	go p.capture(p.stdoutPR, p.stdoutBuf, readyCh)
	go p.capture(p.stderrPR, p.stderrBuf, nil)

	go func() {
		// Readiness: only an exact line proves it. Nothing else does.
		timer := time.NewTimer(p.readyTimeout)
		defer timer.Stop()
		select {
		case line := <-readyCh:
			p.markReady(line)
		case <-timer.C:
			p.readinessTimeout()
		case <-p.done:
		}
	}()

	go p.reap()
}

// capture reads one child stream line by line into the bounded buffer and,
// for stdout, reports exact readiness-line matches. After the scanner ends
// it drains the pipe so the child's copy goroutine can never block.
func (p *LaunchedProcess) capture(pr *io.PipeReader, buf *boundedBuffer, readyCh chan<- string) {
	defer p.streamWG.Done()
	defer io.Copy(io.Discard, pr)

	scanner := bufio.NewScanner(pr)
	scanner.Buffer(make([]byte, 64<<10), buf.limit+4096)
	for scanner.Scan() {
		line := scanner.Text()
		line = strings.TrimSuffix(line, "\r")
		buf.writeLine(line)
		if readyCh != nil && line == p.readyLine {
			// Report the match exactly once.
			select {
			case readyCh <- line:
			default:
			}
		}
	}
	if err := scanner.Err(); err != nil {
		// A line longer than the bound (or a read error) must not stop the
		// drain; the deferred io.Copy above keeps the pipe empty.
		buf.markOverflow()
	}
}

// reap waits for the child to exit, delivers the captured artifacts, and
// records the terminal state exactly once. It owns every post-exit
// transition, so no other goroutine races the state machine.
func (p *LaunchedProcess) reap() {
	err := p.cmd.Wait()
	p.stdoutPW.Close()
	p.stderrPW.Close()
	p.streamWG.Wait()

	// Move Launching to Running so the process accepts the artifacts it
	// produced: a process that died before proving readiness still
	// delivered its output, and the terminal crash reason carries that
	// fact.
	_ = p.MarkRunning()

	// Deliver the captured streams as artifacts while the process still
	// accepts them (non-terminal). A process with no output delivers
	// nothing: the launcher never fabricates an empty artifact.
	if data := p.stdoutBuf.snapshot(); len(data) > 0 {
		_ = p.DeliverArtifact(server.Artifact{
			Name:        stdoutArtifactName,
			Data:        data,
			DeliveredAt: time.Now(),
		})
	}
	if data := p.stderrBuf.snapshot(); len(data) > 0 {
		_ = p.DeliverArtifact(server.Artifact{
			Name:        stderrArtifactName,
			Data:        data,
			DeliveredAt: time.Now(),
		})
	}

	p.mtx.Lock()
	defer p.mtx.Unlock()
	switch {
	case p.pendingCrash != nil:
		p.terminalLocked(server.ProcessStateCrashed, p.pendingCrash)
	case p.stopRequested:
		p.terminalLocked(server.ProcessStateStopped, nil)
	default:
		p.terminalLocked(server.ProcessStateCrashed, p.exitReasonLocked(err))
	}
	close(p.done)
}

// exitReasonLocked derives the typed crash reason from the child's exit.
func (p *LaunchedProcess) exitReasonLocked(err error) *server.CrashReason {
	now := time.Now()
	var ee *exec.ExitError
	if errors.As(err, &ee) {
		if code := ee.ExitCode(); code >= 0 {
			return &server.CrashReason{
				Code:       CrashExitNonZero,
				Message:    fmt.Sprintf("process exited with code %d", code),
				DetectedAt: now,
			}
		}
		return &server.CrashReason{
			Code:       CrashSignaled,
			Message:    "process was killed by a signal",
			DetectedAt: now,
		}
	}
	if err != nil {
		return &server.CrashReason{
			Code:       CrashExitUnexpected,
			Message:    fmt.Sprintf("process wait failed: %v", err),
			DetectedAt: now,
		}
	}
	return &server.CrashReason{
		Code:       CrashExitUnexpected,
		Message:    "process exited cleanly without being asked to stop",
		DetectedAt: now,
	}
}

// markReady records the readiness fact from the exact matched line. It is
// called by the readiness watcher only.
func (p *LaunchedProcess) markReady(line string) {
	p.mtx.Lock()
	defer p.mtx.Unlock()
	if p.fact != nil || p.state.Terminal() {
		return
	}
	if p.state == server.ProcessStateLaunching {
		p.state = server.ProcessStateRunning
	}
	if p.state != server.ProcessStateRunning {
		return
	}
	p.fact = &server.ReadinessFact{Evidence: line, RecordedAt: time.Now()}
	p.state = server.ProcessStateReady
}

// readinessTimeout crash-stops a child that never proved readiness. The
// reaper records the typed reason when the exit is observed.
func (p *LaunchedProcess) readinessTimeout() {
	p.mtx.Lock()
	if p.state.Terminal() {
		p.mtx.Unlock()
		return
	}
	p.pendingCrash = &server.CrashReason{
		Code:       CrashReadinessTimeout,
		Message:    fmt.Sprintf("readiness line %q not observed within %s", p.readyLine, p.readyTimeout),
		DetectedAt: time.Now(),
	}
	p.stopRequested = true
	p.mtx.Unlock()
	p.killGroup()
}

// Stop tears the process down deliberately: it signals the process group
// gracefully where the platform permits, waits through the grace period,
// escalates to a hard kill, and waits for the terminal state. It never
// restarts the process. A process already in a terminal state refuses with a
// typed error; concurrent stops collapse onto the first teardown and return
// its outcome.
func (p *LaunchedProcess) Stop(ctx context.Context) error {
	p.mtx.Lock()
	switch {
	case p.state == server.ProcessStateStopped:
		p.mtx.Unlock()
		return nil
	case p.state == server.ProcessStateCrashed:
		reason := p.reason
		p.mtx.Unlock()
		return fmt.Errorf("%w: %s", server.ErrCrashed, reason.Code)
	}
	first := false
	p.stopOnce.Do(func() {
		first = true
		p.stopRequested = true
	})
	if !first {
		p.mtx.Unlock()
		// A concurrent stop: wait for the first teardown's outcome.
		select {
		case <-p.done:
			p.mtx.Lock()
			defer p.mtx.Unlock()
			if p.state == server.ProcessStateStopped {
				return nil
			}
			return fmt.Errorf("%w: %s", server.ErrCrashed, p.reason.Code)
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	p.mtx.Unlock()

	p.signalGroup()

	// Wait for the reaper to record the terminal state, escalating to a
	// hard kill when the grace period expires.
	timer := time.NewTimer(p.stopGrace)
	defer timer.Stop()
	for {
		select {
		case <-p.done:
			p.mtx.Lock()
			defer p.mtx.Unlock()
			if p.state == server.ProcessStateStopped {
				return nil
			}
			// The child crashed while the stop was in flight; the crash is
			// the terminal truth.
			return fmt.Errorf("%w: %s", server.ErrCrashed, p.reason.Code)
		case <-ctx.Done():
			p.killGroup()
			return ctx.Err()
		case <-timer.C:
			p.killGroup()
			timer.Reset(p.stopGrace)
		}
	}
}

// signalGroup sends the graceful teardown signal to the whole process group
// where the platform permits.
func (p *LaunchedProcess) signalGroup() {
	if p.cmd == nil || p.cmd.Process == nil {
		return
	}
	_ = signalGroupProc(p.cmd.Process)
}

// killGroup hard-kills the process group. It is idempotent: killing an
// exited process is not an error.
func (p *LaunchedProcess) killGroup() {
	if p.cmd == nil || p.cmd.Process == nil {
		return
	}
	_ = killGroupProc(p.cmd.Process)
}

// ---------------------------------------------------------------------------
// server.Process implementation
// ---------------------------------------------------------------------------

// Spec returns the immutable identity of this process.
func (p *LaunchedProcess) Spec() server.ProcessSpec { return p.spec }

// State returns the current lifecycle state.
func (p *LaunchedProcess) State() server.ProcessState {
	p.mtx.Lock()
	defer p.mtx.Unlock()
	return p.state
}

// Readiness returns the recorded readiness fact, or ErrNoReadinessFact.
func (p *LaunchedProcess) Readiness() (server.ReadinessFact, error) {
	p.mtx.Lock()
	defer p.mtx.Unlock()
	if p.fact == nil {
		return server.ReadinessFact{}, server.ErrNoReadinessFact
	}
	return *p.fact, nil
}

// MarkRunning moves the process from Launching to Running. The launcher's
// reaper calls it before delivering artifacts; it is not a stop request and
// never restarts anything.
func (p *LaunchedProcess) MarkRunning() error {
	p.mtx.Lock()
	defer p.mtx.Unlock()
	if p.state != server.ProcessStateLaunching {
		return server.ErrNotRunning
	}
	p.state = server.ProcessStateRunning
	return nil
}

// MarkReady records the readiness fact and moves the process to Ready. The
// readiness watcher is the only caller in this package.
func (p *LaunchedProcess) MarkReady(fact server.ReadinessFact) error {
	p.mtx.Lock()
	defer p.mtx.Unlock()
	if p.fact != nil {
		return server.ErrReadinessAlreadyRecorded
	}
	if p.state != server.ProcessStateRunning {
		return server.ErrNotReady
	}
	p.fact = &fact
	p.state = server.ProcessStateReady
	return nil
}

// MarkCrashed records a crash with a typed reason and moves the process to
// the terminal Crashed state. It refuses on a terminal process.
func (p *LaunchedProcess) MarkCrashed(reason server.CrashReason) error {
	p.mtx.Lock()
	defer p.mtx.Unlock()
	if p.state.Terminal() {
		return server.ErrAlreadyStopped
	}
	p.terminalLocked(server.ProcessStateCrashed, &reason)
	return nil
}

// MarkStopped moves the process to the terminal Stopped state. It refuses on
// a terminal process.
func (p *LaunchedProcess) MarkStopped() error {
	p.mtx.Lock()
	defer p.mtx.Unlock()
	if p.state.Terminal() {
		return server.ErrAlreadyStopped
	}
	p.terminalLocked(server.ProcessStateStopped, nil)
	return nil
}

// WaitTerminal waits until the process reaches a terminal state. It cannot
// miss the transition.
func (p *LaunchedProcess) WaitTerminal(ctx context.Context) (server.ProcessState, error) {
	select {
	case <-p.waitCh:
		return p.State(), nil
	case <-ctx.Done():
		return p.State(), ctx.Err()
	}
}

// DeliverArtifact records one artifact delivered by the launcher's reaper.
// It refuses when the process is not accepting artifacts.
func (p *LaunchedProcess) DeliverArtifact(artifact server.Artifact) error {
	p.mtx.Lock()
	defer p.mtx.Unlock()
	if p.state != server.ProcessStateRunning && p.state != server.ProcessStateReady {
		return server.ErrNotRunning
	}
	p.artifacts = append(p.artifacts, artifact)
	return nil
}

// Artifacts returns the artifacts delivered so far.
func (p *LaunchedProcess) Artifacts() []server.Artifact {
	p.mtx.Lock()
	defer p.mtx.Unlock()
	return append([]server.Artifact(nil), p.artifacts...)
}

// CrashReason returns the typed crash reason once the process is Crashed, or
// ErrCrashed for a process that did not crash.
func (p *LaunchedProcess) CrashReason() (server.CrashReason, error) {
	p.mtx.Lock()
	defer p.mtx.Unlock()
	if p.state != server.ProcessStateCrashed || p.reason == nil {
		return server.CrashReason{}, server.ErrCrashed
	}
	return *p.reason, nil
}

// terminalLocked records the terminal state, the crash reason when present,
// and wakes every waiter. Callers must hold p.mtx.
func (p *LaunchedProcess) terminalLocked(state server.ProcessState, reason *server.CrashReason) {
	p.state = state
	p.reason = reason
	select {
	case <-p.waitCh:
	default:
		close(p.waitCh)
	}
}

// boundedBuffer captures one child stream up to a hard byte bound. Writes
// past the bound are discarded and flagged; the writer never blocks and
// never fails, so a runaway child cannot exhaust the host or deadlock the
// output copy.
type boundedBuffer struct {
	limit int

	mtx        sync.Mutex
	buf        []byte
	overflowed bool
}

// newBoundedBuffer builds a buffer with the given byte limit.
func newBoundedBuffer(limit int) *boundedBuffer {
	return &boundedBuffer{limit: limit}
}

// writeLine appends one line (with a normalized newline) up to the bound.
func (b *boundedBuffer) writeLine(line string) {
	b.mtx.Lock()
	defer b.mtx.Unlock()
	if b.overflowed {
		return
	}
	room := b.limit - len(b.buf)
	if room <= 0 {
		b.overflowed = true
		return
	}
	if len(line)+1 > room {
		b.buf = append(b.buf, line[:room-1]...)
		b.buf = append(b.buf, '\n')
		b.overflowed = true
		return
	}
	b.buf = append(b.buf, line...)
	b.buf = append(b.buf, '\n')
}

// markOverflow flags the buffer as truncated.
func (b *boundedBuffer) markOverflow() {
	b.mtx.Lock()
	defer b.mtx.Unlock()
	b.overflowed = true
}

// snapshot returns a copy of the captured bytes.
func (b *boundedBuffer) snapshot() []byte {
	b.mtx.Lock()
	defer b.mtx.Unlock()
	return append([]byte(nil), b.buf...)
}
