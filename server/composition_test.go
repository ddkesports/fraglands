package server

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"

	"github.com/paralin/fraglands/core"
)

// ---------------------------------------------------------------------------
// helpers
// ---------------------------------------------------------------------------

// validSummaryJSON returns a canonical, fully valid TerminalSummary artifact
// for attempt generation 3. It matches byte-for-byte the field set of the
// modlock spool writer (8 keys).
func validSummaryJSON() []byte {
	return []byte(`{"version":"runback-attempt/v1","revision":"rev-7","replay_identity":"replay-101514223",` +
		`"attempt_generation":3,"server_process_generation":7,"takeover_tick":63280,` +
		`"ending":"secure","ended_at_seconds":200}`)
}

// artifactNameFor builds the spool artifact name for one generation, exactly
// as the writer does.
func artifactNameFor(generation uint64) string {
	return "runback_summary_gen" + itoa(int(generation)) + ".json"
}

// itoa is a tiny helper for building distinct artifact names in tests.
func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	digits := []byte{}
	for n > 0 {
		digits = append([]byte{byte('0' + n%10)}, digits...)
		n /= 10
	}
	return string(digits)
}

// patchSummaryJSON replaces one literal substring of the valid artifact. It
// fails the test when the needle is missing so a stale fixture is loud.
func patchSummaryJSON(t *testing.T, old, new string) []byte {
	t.Helper()
	s := string(validSummaryJSON())
	if !strings.Contains(s, old) {
		t.Fatalf("patchSummaryJSON: %q not found in artifact", old)
	}
	return []byte(strings.Replace(s, old, new, 1))
}

// ---------------------------------------------------------------------------
// gate test harness
// ---------------------------------------------------------------------------

// errAcceptFailed is the injected acceptance failure.
var errAcceptFailed = fmt.Errorf("test: accept failed")

// testGate is a SummaryIngestionGate wired to in-memory test state.
type testGate struct {
	*SummaryIngestionGate
	participants map[string]*core.ServerParticipant
	accepted     []acceptedResult
	acceptErr    error

	mtx sync.Mutex
}

// acceptedResult is one summary delivered to the accept seam, together with
// the participant it was bound to.
type acceptedResult struct {
	participant *core.ServerParticipant
	summary     *TerminalSummary
}

// newTestGate builds a gate with one authenticated participant bound to
// process generation 7 (matching the valid artifact).
func newTestGate(t *testing.T) (*testGate, *core.ServerParticipant) {
	t.Helper()
	g := &testGate{
		participants: map[string]*core.ServerParticipant{
			"scred-a": {ID: "srv-a", ProcessGeneration: 7},
		},
	}
	gate, err := NewSummaryIngestionGate(
		gateResolverFunc(func(_ context.Context, credential string) (*core.ServerParticipant, error) {
			g.mtx.Lock()
			defer g.mtx.Unlock()
			p, ok := g.participants[credential]
			if !ok {
				return nil, ErrUnauthenticated
			}
			return p, nil
		}),
		func(participant *core.ServerParticipant, summary *TerminalSummary) error {
			g.mtx.Lock()
			defer g.mtx.Unlock()
			if g.acceptErr != nil {
				return g.acceptErr
			}
			g.accepted = append(g.accepted, acceptedResult{participant: participant, summary: summary})
			return nil
		},
	)
	if err != nil {
		t.Fatal(err.Error())
	}
	g.SummaryIngestionGate = gate
	return g, g.participants["scred-a"]
}

// gateResolverFunc adapts a function to the ServerParticipantResolver
// interface.
type gateResolverFunc func(ctx context.Context, credential string) (*core.ServerParticipant, error)

// ResolveParticipant resolves the credential.
func (f gateResolverFunc) ResolveParticipant(ctx context.Context, credential string) (*core.ServerParticipant, error) {
	return f(ctx, credential)
}

// ---------------------------------------------------------------------------
// gate: happy path and crash-stop semantics
// ---------------------------------------------------------------------------

func TestIngestionGateHappyPath(t *testing.T) {
	g, participant := newTestGate(t)

	req := IngestRequest{
		Credential:   "scred-a",
		ArtifactName: artifactNameFor(3),
		Data:         validSummaryJSON(),
	}
	if err := g.Ingest(req); err != nil {
		t.Fatal(err.Error())
	}

	if len(g.accepted) != 1 {
		t.Fatalf("expected 1 accepted summary, got %d", len(g.accepted))
	}
	accepted := g.accepted[0]
	if accepted.participant != participant {
		t.Fatal("accepted summary must be bound to the authenticated participant")
	}
	if accepted.summary.ServerProcessGeneration != participant.ProcessGeneration {
		t.Fatal("accepted summary must carry the participant's process generation")
	}
	if accepted.summary.AttemptGeneration != 3 {
		t.Fatalf("expected attempt generation 3, got %d", accepted.summary.AttemptGeneration)
	}
	if accepted.summary.Revision != "rev-7" || accepted.summary.ReplayIdentity != "replay-101514223" {
		t.Fatalf("bad decoded identity: %q / %q", accepted.summary.Revision, accepted.summary.ReplayIdentity)
	}
	if accepted.summary.TakeoverTick != 63280 || accepted.summary.EndedAtSeconds != 200 {
		t.Fatalf("bad decoded measurement: %d / %d", accepted.summary.TakeoverTick, accepted.summary.EndedAtSeconds)
	}
}

func TestIngestionGateAcceptFailureIsCrashStop(t *testing.T) {
	g, _ := newTestGate(t)
	g.acceptErr = errAcceptFailed
	req := IngestRequest{
		Credential:   "scred-a",
		ArtifactName: artifactNameFor(3),
		Data:         validSummaryJSON(),
	}
	if err := g.Ingest(req); !errors.Is(err, errAcceptFailed) {
		t.Fatalf("expected accept failure to surface, got %v", err)
	}
	if len(g.accepted) != 0 {
		t.Fatalf("failed accept must not record a result, got %d", len(g.accepted))
	}
	// The digest reservation is rolled back: a later retry with a fixed
	// store is not blocked by the failed attempt.
	g.acceptErr = nil
	if err := g.Ingest(req); err != nil {
		t.Fatalf("retry after failed accept: %v", err)
	}
	if len(g.accepted) != 1 {
		t.Fatalf("expected 1 accepted summary after retry, got %d", len(g.accepted))
	}
}

// ---------------------------------------------------------------------------
// gate: refusals
// ---------------------------------------------------------------------------

func TestIngestionGateRefusals(t *testing.T) {
	t.Run("unauthenticated credential", func(t *testing.T) {
		g, _ := newTestGate(t)
		err := g.Ingest(IngestRequest{Credential: "wrong-cred", ArtifactName: artifactNameFor(3), Data: validSummaryJSON()})
		if err == nil {
			t.Fatal("expected refusal")
		}
		if len(g.accepted) != 0 {
			t.Fatal("refused ingest must not accept")
		}
	})

	t.Run("nil participant from resolver", func(t *testing.T) {
		g, _ := newTestGate(t)
		g.participants["scred-a"] = nil
		if err := g.Ingest(IngestRequest{Credential: "scred-a", ArtifactName: artifactNameFor(3), Data: validSummaryJSON()}); err == nil {
			t.Fatal("expected refusal when resolver yields no participant")
		}
	})

	t.Run("path traversal artifact name", func(t *testing.T) {
		g, _ := newTestGate(t)
		for _, name := range []string{
			"../runback_summary_gen3.json",
			"..\\runback_summary_gen3.json",
			"/etc/passwd",
			"",
			"attempt-3.json",
			"runback_summary_gen3.json.exe",
		} {
			if err := g.Ingest(IngestRequest{Credential: "scred-a", ArtifactName: name, Data: validSummaryJSON()}); err == nil {
				t.Errorf("expected traversal refusal for %q", name)
			}
		}
	})

	t.Run("artifact name of another generation", func(t *testing.T) {
		g, _ := newTestGate(t)
		// The payload decodes fine, but the name says generation 4: the
		// name/payload split must be refused.
		if err := g.Ingest(IngestRequest{Credential: "scred-a", ArtifactName: "runback_summary_gen4.json", Data: validSummaryJSON()}); err == nil {
			t.Fatal("expected refusal for artifact name of another generation")
		}
	})

	t.Run("oversize artifact", func(t *testing.T) {
		g, _ := newTestGate(t)
		err := g.Ingest(IngestRequest{
			Credential:   "scred-a",
			ArtifactName: artifactNameFor(3),
			Data:         make([]byte, MaxTerminalSummaryBytes+1),
		})
		if !errors.Is(err, ErrSummaryOversize) {
			t.Fatalf("expected oversize refusal, got %v", err)
		}
	})

	t.Run("malformed artifact is crash-stop", func(t *testing.T) {
		g, _ := newTestGate(t)
		err := g.Ingest(IngestRequest{
			Credential:   "scred-a",
			ArtifactName: artifactNameFor(3),
			Data:         []byte(`{"version": "runback-attempt/v1", "ending": "secure"}`),
		})
		if err == nil {
			t.Fatal("expected malformed refusal")
		}
		if len(g.accepted) != 0 {
			t.Fatal("malformed artifact must not be accepted")
		}
	})

	t.Run("decoded generation does not match participant", func(t *testing.T) {
		g, _ := newTestGate(t)
		// The artifact claims process generation 7, but this participant is
		// bound to generation 2: identity binding must refuse.
		g.participants["scred-a"] = &core.ServerParticipant{ID: "srv-a", ProcessGeneration: 2}
		if err := g.Ingest(IngestRequest{Credential: "scred-a", ArtifactName: artifactNameFor(3), Data: validSummaryJSON()}); !errors.Is(err, ErrSummaryIdentityMismatch) {
			t.Fatalf("expected identity mismatch refusal, got %v", err)
		}
	})

	t.Run("duplicate artifact refused", func(t *testing.T) {
		g, _ := newTestGate(t)
		req := IngestRequest{Credential: "scred-a", ArtifactName: artifactNameFor(3), Data: validSummaryJSON()}
		if err := g.Ingest(req); err != nil {
			t.Fatal(err.Error())
		}
		if err := g.Ingest(req); !errors.Is(err, ErrSummaryDuplicate) {
			t.Fatalf("expected duplicate refusal, got %v", err)
		}
		if len(g.accepted) != 1 {
			t.Fatalf("expected exactly 1 accepted summary, got %d", len(g.accepted))
		}
	})
}

// ---------------------------------------------------------------------------
// gate: concurrency
// ---------------------------------------------------------------------------

func TestIngestionGateConcurrentDuplicate(t *testing.T) {
	g, _ := newTestGate(t)
	const workers = 16
	errs := make(chan error, workers)
	for i := 0; i < workers; i++ {
		go func() {
			errs <- g.Ingest(IngestRequest{Credential: "scred-a", ArtifactName: artifactNameFor(3), Data: validSummaryJSON()})
		}()
	}
	accepted := 0
	for i := 0; i < workers; i++ {
		if err := <-errs; err == nil {
			accepted++
		}
	}
	if accepted != 1 {
		t.Fatalf("expected exactly 1 accepted summary under race, got %d", accepted)
	}
}

func TestIngestionGateConcurrentDistinct(t *testing.T) {
	g, _ := newTestGate(t)
	const workers = 8
	errs := make(chan error, workers)
	for i := 0; i < workers; i++ {
		go func(n int) {
			gen := uint64(n + 100)
			data := patchSummaryJSON(t, `"attempt_generation":3`, `"attempt_generation":`+itoa(int(gen)))
			errs <- g.Ingest(IngestRequest{Credential: "scred-a", ArtifactName: artifactNameFor(uint64(gen)), Data: data})
		}(i)
	}
	accepted := 0
	for i := 0; i < workers; i++ {
		if err := <-errs; err == nil {
			accepted++
		}
	}
	if accepted != workers {
		t.Fatalf("expected %d accepted summaries, got %d", workers, accepted)
	}
}

// ---------------------------------------------------------------------------
// gate: constructor validation
// ---------------------------------------------------------------------------

func TestNewIngestionGateValidation(t *testing.T) {
	resolver := gateResolverFunc(func(context.Context, string) (*core.ServerParticipant, error) { return nil, ErrUnauthenticated })
	accept := func(*core.ServerParticipant, *TerminalSummary) error { return nil }

	if _, err := NewSummaryIngestionGate(nil, accept); err == nil {
		t.Fatal("expected refusal without a resolver")
	}
	if _, err := NewSummaryIngestionGate(resolver, nil); err == nil {
		t.Fatal("expected refusal without an accept callback")
	}
	if _, err := NewSummaryIngestionGate(resolver, accept); err != nil {
		t.Fatalf("expected success, got %v", err)
	}
}
