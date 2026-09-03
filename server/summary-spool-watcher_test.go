package server

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/paralin/fraglands/core"
)

type watcherResolver struct {
	participant *core.ServerParticipant
	mu          sync.Mutex
	accepted    int
}

func (r *watcherResolver) ResolveParticipant(context.Context, string) (*core.ServerParticipant, error) {
	return r.participant, nil
}
func (r *watcherResolver) CommitCredential(string, uint64, commit func() error) error {
	return commit()
}

func watcherArtifact(attempt uint64, process uint64) []byte {
	return []byte(`{"version":"runback-attempt/v1","revision":"r","replay_identity":"p","attempt_generation":` +
		itoa(int(attempt)) + `,"server_process_generation":` + itoa(int(process)) + `,"takeover_tick":1,"ending":"secure","ended_at_seconds":2}`)
}

func TestSummarySpoolWatcherAcceptsMultipleAttemptsAndIgnoresTemps(t *testing.T) {
	dir := t.TempDir()
	resolver := &watcherResolver{participant: &core.ServerParticipant{ID: "s", ProcessGeneration: 7}}
	gate, err := NewSummaryIngestionGate(resolver, func(*core.ServerParticipant, *TerminalSummary) error {
		resolver.mu.Lock()
		resolver.accepted++
		resolver.mu.Unlock()
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	w, err := NewSummarySpoolWatcher(ProcessSpec{Generation: 7, Port: 1000, SpoolDir: dir}, "cred", gate)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "runback_summary_gen3.json.tmp"), watcherArtifact(3, 7), 0600); err != nil {
		t.Fatal(err)
	}
	for _, n := range []uint64{3, 4} {
		tmp := filepath.Join(dir, "tmp")
		final := filepath.Join(dir, "runback_summary_gen"+itoa(int(n))+".json")
		if err := os.WriteFile(tmp, watcherArtifact(n, 7), 0600); err != nil {
			t.Fatal(err)
		}
		if err := os.Rename(tmp, final); err != nil {
			t.Fatal(err)
		}
	}
	if err := w.PollOnce(); err != nil {
		t.Fatal(err)
	}
	if err := w.PollOnce(); err != nil {
		t.Fatal(err)
	}
	resolver.mu.Lock()
	got := resolver.accepted
	resolver.mu.Unlock()
	if got != 2 {
		t.Fatalf("accepted %d attempts, want 2", got)
	}
}

func TestSummarySpoolWatcherQuarantinesMalformedCanonicalAndStops(t *testing.T) {
	dir := t.TempDir()
	resolver := &watcherResolver{participant: &core.ServerParticipant{ID: "s", ProcessGeneration: 7}}
	gate, err := NewSummaryIngestionGate(resolver, func(*core.ServerParticipant, *TerminalSummary) error { return nil })
	if err != nil {
		t.Fatal(err)
	}
	w, err := NewSummarySpoolWatcher(ProcessSpec{Generation: 7, Port: 1000, SpoolDir: dir}, "cred", gate)
	if err != nil {
		t.Fatal(err)
	}
	name := "runback_summary_gen3.json"
	if err := os.WriteFile(filepath.Join(dir, name), []byte(`not-json`), 0600); err != nil {
		t.Fatal(err)
	}
	err = w.PollOnce()
	if !errors.Is(err, ErrSummaryMalformed) || !errors.Is(err, ErrSpoolWatcherTerminal) {
		t.Fatalf("got %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, ".quarantine", name)); err != nil {
		t.Fatalf("malformed artifact not quarantined: %v", err)
	}
	if !errors.Is(w.PollOnce(), ErrSpoolWatcherTerminal) {
		t.Fatal("terminal watcher accepted another poll")
	}
}

func TestSummarySpoolWatcherStopLinearizesWithIngest(t *testing.T) {
	dir := t.TempDir()
	resolver := &watcherResolver{participant: &core.ServerParticipant{ID: "s", ProcessGeneration: 7}}
	started := make(chan struct{})
	release := make(chan struct{})
	gate, err := NewSummaryIngestionGate(resolver, func(*core.ServerParticipant, *TerminalSummary) error { close(started); <-release; return nil })
	if err != nil {
		t.Fatal(err)
	}
	w, err := NewSummarySpoolWatcher(ProcessSpec{Generation: 7, Port: 1000, SpoolDir: dir}, "cred", gate)
	if err != nil {
		t.Fatal(err)
	}
	name := filepath.Join(dir, "runback_summary_gen3.json")
	if err := os.WriteFile(name, watcherArtifact(3, 7), 0600); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- w.PollOnce() }()
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("ingest did not start")
	}
	stopped := make(chan struct{})
	go func() { w.Stop(); close(stopped) }()
	select {
	case <-stopped:
		t.Fatal("stop overtook in-flight ingest")
	case <-time.After(20 * time.Millisecond):
	}
	close(release)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	<-stopped
	if !errors.Is(w.PollOnce(), ErrSpoolWatcherStopped) {
		t.Fatal("poll after stop was not refused")
	}
}
