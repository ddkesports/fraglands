package server

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
)

var (
	// ErrSpoolWatcherStopped means the watcher was stopped and will not
	// perform another ingest.
	ErrSpoolWatcherStopped = errors.New("server: summary spool watcher stopped")
	// ErrSpoolWatcherTerminal means a canonical artifact was refused. The
	// watcher is terminal and will not retry that refusal.
	ErrSpoolWatcherTerminal = errors.New("server: summary spool watcher terminal refusal")
	// ErrSpoolWatcherUnsupported means the host cannot provide the required
	// no-follow regular-file read primitive.
	ErrSpoolWatcherUnsupported = errors.New("server: summary spool watcher unsupported on this platform")
	// ErrSpoolWatcherQuarantine means a refused artifact could not be moved
	// out of the canonical namespace.
	ErrSpoolWatcherQuarantine = errors.New("server: summary spool watcher quarantine failed")
)

// SummarySpoolWatcher is an opt-in, synchronous poller for one process
// generation's private summary spool. The caller owns its polling loop and
// cancellation. A ProcessSpec owns the watcher; attempt generations live in
// canonical file names and therefore multiple attempts can share one server
// generation.
type SummarySpoolWatcher struct {
	spec       ProcessSpec
	credential string
	gate       *SummaryIngestionGate
	quarantine string

	// mu makes stop and PollOnce linearizable. PollOnce holds it through the
	// bounded read and gate commit: a stop that wins prevents ingest, while a
	// poll that wins completes before stop becomes terminal.
	mu        sync.Mutex
	stopped   bool
	terminal  bool
	processed map[string]struct{}
}

// SummarySpoolWatcherOption configures the injected watcher.
type SummarySpoolWatcherOption func(*SummarySpoolWatcher) error

// WithSummarySpoolQuarantineDir overrides the private quarantine directory.
// The directory must be under the process spool directory.
func WithSummarySpoolQuarantineDir(dir string) SummarySpoolWatcherOption {
	return func(w *SummarySpoolWatcher) error {
		if dir == "" {
			return fmt.Errorf("%w: quarantine directory is required", ErrInvalidSpec)
		}
		clean := filepath.Clean(dir)
		if err := underDir(w.spec.SpoolDir, clean); err != nil {
			return err
		}
		w.quarantine = clean
		return nil
	}
}

// NewSummarySpoolWatcher constructs a default-off watcher. Construction does
// not start a goroutine or expose a network endpoint.
func NewSummarySpoolWatcher(spec ProcessSpec, credential string, gate *SummaryIngestionGate, opts ...SummarySpoolWatcherOption) (*SummarySpoolWatcher, error) {
	if err := spec.Validate(); err != nil {
		return nil, err
	}
	if credential == "" {
		return nil, fmt.Errorf("%w: credential is required", ErrInvalidSpec)
	}
	if gate == nil {
		return nil, fmt.Errorf("%w: ingestion gate is required", ErrInvalidSpec)
	}
	if !summarySpoolWatcherSupported() {
		return nil, ErrSpoolWatcherUnsupported
	}
	if !filepath.IsAbs(spec.SpoolDir) {
		return nil, fmt.Errorf("%w: spool dir must be absolute", ErrInvalidSpec)
	}
	w := &SummarySpoolWatcher{
		spec: spec, credential: credential, gate: gate,
		quarantine: filepath.Join(spec.SpoolDir, ".quarantine"),
		processed:  make(map[string]struct{}),
	}
	for _, opt := range opts {
		if opt != nil {
			if err := opt(w); err != nil {
				return nil, err
			}
		}
	}
	if err := os.MkdirAll(w.quarantine, 0700); err != nil {
		return nil, fmt.Errorf("%w: quarantine unavailable", ErrSpoolWatcherQuarantine)
	}
	return w, nil
}

// Spec returns the immutable process identity owned by this watcher.
func (w *SummarySpoolWatcher) Spec() ProcessSpec { return w.spec }

// Stop makes the watcher terminal. It is safe to call concurrently with
// PollOnce; an ingest already in progress linearizes before Stop returns.
func (w *SummarySpoolWatcher) Stop() {
	w.mu.Lock()
	w.stopped = true
	w.mu.Unlock()
}

// Close is an alias for Stop for caller-owned lifecycle code.
func (w *SummarySpoolWatcher) Close() { w.Stop() }

// Stopped reports whether polling has been stopped or a terminal refusal has
// occurred.
func (w *SummarySpoolWatcher) Stopped() bool {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.stopped || w.terminal
}

// PollOnce scans the spool once and synchronously ingests every newly
// published canonical artifact. Temp and noncanonical names are ignored.
// Canonical refusal is terminal and the file is quarantined.
func (w *SummarySpoolWatcher) PollOnce() error { return w.pollOnce(context.Background()) }

// PollOnceContext is PollOnce with caller cancellation. Cancellation is
// checked before each bounded file operation; no background work remains.
func (w *SummarySpoolWatcher) PollOnceContext(ctx context.Context) error {
	if ctx == nil {
		ctx = context.Background()
	}
	return w.pollOnce(ctx)
}

func (w *SummarySpoolWatcher) pollOnce(ctx context.Context) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.stopped {
		return ErrSpoolWatcherStopped
	}
	if w.terminal {
		return ErrSpoolWatcherTerminal
	}
	if err := ctx.Err(); err != nil {
		w.stopped = true
		return err
	}

	entries, err := os.ReadDir(w.spec.SpoolDir)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil
		}
		return err
	}
	for _, entry := range entries {
		name := entry.Name()
		_, canonical := canonicalSummaryName(name)
		if !canonical {
			continue
		}
		if _, done := w.processed[name]; done {
			continue
		}
		if err := ctx.Err(); err != nil {
			w.stopped = true
			return err
		}
		data, err := readSummarySpoolFile(filepath.Join(w.spec.SpoolDir, name), MaxTerminalSummaryBytes)
		if err != nil {
			if errors.Is(err, fs.ErrNotExist) {
				// A publisher may remove or replace a path after ReadDir. The
				// next poll observes the replacement without trusting a partial
				// write.
				continue
			}
			return w.refuse(name, err)
		}
		if err := ctx.Err(); err != nil {
			w.stopped = true
			return err
		}
		if err := w.gate.Ingest(IngestRequest{Credential: w.credential, ArtifactName: name, Data: data}); err != nil {
			if errors.Is(err, ErrSummaryDuplicate) {
				w.processed[name] = struct{}{}
				continue
			}
			return w.refuse(name, err)
		}
		// The name is the attempt identity. Once accepted, a same-name
		// replacement is never accepted a second time.
		w.processed[name] = struct{}{}
	}
	return nil
}

func (w *SummarySpoolWatcher) refuse(name string, cause error) error {
	w.terminal = true
	quarantineErr := quarantineSummaryFile(filepath.Join(w.spec.SpoolDir, name), w.quarantine, name)
	if quarantineErr != nil {
		return errors.Join(ErrSpoolWatcherTerminal, cause, ErrSpoolWatcherQuarantine, quarantineErr)
	}
	return errors.Join(ErrSpoolWatcherTerminal, cause)
}

// canonicalSummaryName recognizes names in the writer namespace only. The
// payload remains authoritative for schema and attempt/server identity; this
// helper only decides which directory entries are eligible for opening.
func canonicalSummaryName(name string) (uint64, bool) {
	const prefix = TerminalSummaryArtifactName
	if !strings.HasPrefix(name, prefix) || !strings.HasSuffix(name, ".json") {
		return 0, false
	}
	digits := name[len(prefix) : len(name)-len(".json")]
	if digits == "" {
		return 0, false
	}
	for _, c := range digits {
		if c < '0' || c > '9' {
			return 0, false
		}
	}
	generation, err := strconv.ParseUint(digits, 10, 64)
	if err != nil {
		return 0, false
	}
	return generation, true
}

func quarantineSummaryFile(path, dir, name string) error {
	if err := os.Rename(path, filepath.Join(dir, name)); err != nil {
		return err
	}
	return nil
}

func underDir(root, child string) error {
	rel, err := filepath.Rel(filepath.Clean(root), child)
	if err != nil || rel == "." || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return fmt.Errorf("%w: quarantine directory escapes spool", ErrInvalidSpec)
	}
	return nil
}
