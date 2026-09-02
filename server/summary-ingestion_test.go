package server

import (
	"fmt"
	"strings"
	"sync"
	"testing"
)

// ---------------------------------------------------------------------------
// helpers
// ---------------------------------------------------------------------------

// validSummaryJSON returns a canonical, fully valid TerminalSummary artifact.
func validSummaryJSON() []byte {
	return []byte(`{
  "version": "runback-attempt/v1",
  "replay_identity": "replay-101514223",
  "revision": "rev-7",
  "server_process_generation": 3,
  "attempt_generation": 9,
  "takeover_tick": 63280,
  "takeover_at_seconds": 5,
  "ending": "secure",
  "ended_at_seconds": 200,
  "timeline": [
    {"state": "prepared", "at_seconds": 0},
    {"state": "countdown", "at_seconds": 0},
    {"state": "live", "at_seconds": 5},
    {"state": "ended", "at_seconds": 200}
  ],
  "disconnects": [
    {"entity_id": 4, "disconnected_at_seconds": 60, "reclaimed_at_seconds": 90},
    {"entity_id": 7, "disconnected_at_seconds": 120, "reclaimed_at_seconds": null}
  ],
  "facts": [
    {"name": "midboss-health", "source": "replay-derived", "value": "8200", "replay_value": "8200", "unsupported_reason": ""},
    {"name": "hero-pose", "source": "server-observed", "value": "x y z"},
    {"name": "items", "source": "unsupported", "unsupported_reason": "no supported source"}
  ]
}`)
}

// patchSummaryJSON replaces one literal substring of the valid artifact. It
// panics on a missing needle so a broken test fails loudly.
func patchSummaryJSON(t *testing.T, old, new string) []byte {
	t.Helper()
	src := string(validSummaryJSON())
	if !strings.Contains(src, old) {
		t.Fatalf("patchSummaryJSON: %q not found in artifact", old)
	}
	return []byte(strings.Replace(src, old, new, 1))
}

// ---------------------------------------------------------------------------
// decoder: acceptance
// ---------------------------------------------------------------------------

func TestParseTerminalSummaryAcceptsValidArtifact(t *testing.T) {
	s, err := ParseTerminalSummaryArtifact(validSummaryJSON())
	if err != nil {
		t.Fatal(err.Error())
	}
	if s.Version != "runback-attempt/v1" {
		t.Fatalf("bad version %q", s.Version)
	}
	if s.ReplayIdentity != "replay-101514223" || s.Revision != "rev-7" {
		t.Fatalf("bad identity: %q / %q", s.ReplayIdentity, s.Revision)
	}
	if s.ServerProcessGeneration != 3 || s.AttemptGeneration != 9 {
		t.Fatalf("bad generations: %d / %d", s.ServerProcessGeneration, s.AttemptGeneration)
	}
	if s.TakeoverTick != 63280 || s.TakeoverAtSeconds != 5 {
		t.Fatalf("bad takeover: %d / %d", s.TakeoverTick, s.TakeoverAtSeconds)
	}
	if s.Ending != "secure" || s.EndedAtSeconds != 200 {
		t.Fatalf("bad ending: %q / %d", s.Ending, s.EndedAtSeconds)
	}
	if len(s.Timeline) != 4 || s.Timeline[0].State != "prepared" || s.Timeline[3].AtSeconds != 200 {
		t.Fatalf("bad timeline: %+v", s.Timeline)
	}
	if len(s.Disconnects) != 2 || s.Disconnects[0].ReclaimedAtSeconds == nil || *s.Disconnects[0].ReclaimedAtSeconds != 90 {
		t.Fatalf("bad disconnects: %+v", s.Disconnects)
	}
	if s.Disconnects[1].ReclaimedAtSeconds != nil {
		t.Fatal("null reclaimed_at_seconds must decode to nil")
	}
	if len(s.Facts) != 3 || s.Facts[2].Source != "unsupported" || s.Facts[2].UnsupportedReason == "" {
		t.Fatalf("bad facts: %+v", s.Facts)
	}
}

func TestParseTerminalSummaryCopiesValuesOutOfParser(t *testing.T) {
	data := validSummaryJSON()
	s, err := ParseTerminalSummaryArtifact(data)
	if err != nil {
		t.Fatal(err.Error())
	}
	// Scribble over the input; the summary must be unaffected.
	for i := range data {
		data[i] = 'x'
	}
	if s.ReplayIdentity != "replay-101514223" || s.Revision != "rev-7" {
		t.Fatalf("summary aliases parser memory: %q / %q", s.ReplayIdentity, s.Revision)
	}
	if s.Facts[0].Name != "midboss-health" {
		t.Fatalf("fact strings alias parser memory: %q", s.Facts[0].Name)
	}
}

// ---------------------------------------------------------------------------
// decoder: adversarial refusals
// ---------------------------------------------------------------------------

func TestParseTerminalSummaryRefusals(t *testing.T) {
	tests := []struct {
		name    string
		data    []byte
		wantErr error
	}{
		{"nil payload", nil, ErrSummaryMalformed},
		{"empty payload", []byte{}, ErrSummaryMalformed},
		{"not json", []byte("this is not json"), ErrSummaryMalformed},
		{"truncated json", []byte(`{"version": "runback-attempt/v1"`), ErrSummaryMalformed},
		{"trailing garbage", []byte(`{"version": "runback-attempt/v1"} oops`), ErrSummaryMalformed},
		{"array payload", []byte(`[]`), ErrSummaryMalformed},
		{"string payload", []byte(`"summary"`), ErrSummaryMalformed},
		{"number payload", []byte(`42`), ErrSummaryMalformed},
		{"unknown field", []byte(`{"version": "runback-attempt/v1", "score": 99}`), ErrSummaryMalformed},
		{"wrong version", patchSummaryJSON(t, `"runback-attempt/v1"`, `"runback-attempt/v2"`), ErrSummaryUnknownVersion},
		{"missing version", []byte(`{"replay_identity": "r", "revision": "x"}`), ErrSummaryMalformed},
		{"missing replay identity", patchSummaryJSON(t, `"replay_identity": "replay-101514223",`, `"replay_identity_x": "replay-101514223",`), ErrSummaryMalformed},
		{"missing revision", patchSummaryJSON(t, `"revision": "rev-7",`, `"revision_x": "rev-7",`), ErrSummaryMalformed},
		{"empty replay identity", patchSummaryJSON(t, `"replay-101514223"`, `""`), ErrSummaryMalformed},
		{"empty revision", patchSummaryJSON(t, `"rev-7"`, `""`), ErrSummaryMalformed},
		{"zero process generation", patchSummaryJSON(t, `"server_process_generation": 3`, `"server_process_generation": 0`), ErrSummaryMalformed},
		{"zero attempt generation", patchSummaryJSON(t, `"attempt_generation": 9`, `"attempt_generation": 0`), ErrSummaryMalformed},
		{"unknown ending", patchSummaryJSON(t, `"ending": "secure"`, `"ending": "victory-royale"`), ErrSummaryMalformed},
		{"string process generation", patchSummaryJSON(t, `"server_process_generation": 3`, `"server_process_generation": "3"`), ErrSummaryMalformed},
		{"float attempt generation", patchSummaryJSON(t, `"attempt_generation": 9`, `"attempt_generation": 9.5`), ErrSummaryMalformed},
		{"string takeover tick", patchSummaryJSON(t, `"takeover_tick": 63280`, `"takeover_tick": "63280"`), ErrSummaryMalformed},
		{"negative takeover tick", patchSummaryJSON(t, `"takeover_tick": 63280`, `"takeover_tick": -1`), ErrSummaryMalformed},
		{"takeover tick over uint32", patchSummaryJSON(t, `"takeover_tick": 63280`, `"takeover_tick": 4294967296`), ErrSummaryMalformed},
		{"ended_at over uint32", patchSummaryJSON(t, `"ended_at_seconds": 200`, `"ended_at_seconds": 4294967296`), ErrSummaryMalformed},
		{"timeline not an array", patchSummaryJSON(t, `"timeline": [`, `"timeline": {`), ErrSummaryMalformed},
		{"timeline item unknown state", patchSummaryJSON(t, `{"state": "live", "at_seconds": 5}`, `{"state": "paused", "at_seconds": 5}`), ErrSummaryMalformed},
		{"timeline item missing state", patchSummaryJSON(t, `{"state": "live", "at_seconds": 5}`, `{"at_seconds": 5}`), ErrSummaryMalformed},
		{"timeline item extra field", patchSummaryJSON(t, `{"state": "live", "at_seconds": 5}`, `{"state": "live", "at_seconds": 5, "extra": 1}`), ErrSummaryMalformed},
		{"disconnects item unknown field", patchSummaryJSON(t, `{"entity_id": 4, "disconnected_at_seconds": 60, "reclaimed_at_seconds": 90}`, `{"entity_id": 4, "disconnected_at_seconds": 60, "reclaimed_at_seconds": 90, "who": "me"}`), ErrSummaryMalformed},
		{"facts item unknown source", patchSummaryJSON(t, `{"name": "hero-pose", "source": "server-observed", "value": "x y z"}`, `{"name": "hero-pose", "source": "psychic", "value": "x y z"}`), ErrSummaryMalformed},
		{"facts item empty name", patchSummaryJSON(t, `"name": "hero-pose"`, `"name": ""`), ErrSummaryMalformed},
		{"facts item missing name", patchSummaryJSON(t, `"name": "hero-pose", "source"`, `"source"`), ErrSummaryMalformed},
		{"facts item value wrong type", patchSummaryJSON(t, `"value": "x y z"`, `"value": 42`), ErrSummaryMalformed},
	}
	for _, tc := range tests {
		s, err := ParseTerminalSummaryArtifact(tc.data)
		if err == nil {
			t.Errorf("%s: expected refusal, got summary %+v", tc.name, s)
			continue
		}
		if s != nil {
			t.Errorf("%s: refusal must not carry a partial summary", tc.name)
		}
		if !errIs(err, tc.wantErr) {
			t.Errorf("%s: expected %v, got %v", tc.name, tc.wantErr, err)
		}
	}
}

func TestParseTerminalSummaryOversize(t *testing.T) {
	if err := CheckArtifactSize(MaxTerminalSummaryBytes + 1); err == nil {
		t.Fatal("expected oversize refusal")
	}
	if err := CheckArtifactSize(MaxTerminalSummaryBytes); err != nil {
		t.Fatalf("exactly-at-limit artifact must pass: %v", err)
	}
	big := make([]byte, MaxTerminalSummaryBytes+1)
	if _, err := ParseTerminalSummaryArtifact(big); err == nil {
		t.Fatal("expected oversize refusal before parsing")
	}
}

func TestValidateArtifactNameTraversal(t *testing.T) {
	for _, name := range []string{
		"",
		"../escape.json",
		"..\\escape.json",
		"/etc/passwd",
		"C:\\spool\\summary.json",
		"sub/dir/summary.json",
		"summary.json\x00.txt",
		"terminal-summary.json.exe",
	} {
		if err := ValidateArtifactName(name); err == nil {
			t.Errorf("expected refusal for %q", name)
		}
	}
	// The one legitimate artifact name is accepted.
	if err := ValidateArtifactName("terminal-summary.json"); err != nil {
		t.Fatalf("expected acceptance, got %v", err)
	}
}

// errIs is a tiny errors.Is wrapper so the table above stays readable.
func errIs(err, target error) bool {
	if err == nil || target == nil {
		return err == target
	}
	for err != nil {
		if err == target {
			return true
		}
		err = unwrappableNext(err)
	}
	return false
}

func unwrappableNext(err error) error {
	type unwrapper interface{ Unwrap() error }
	if u, ok := err.(unwrapper); ok {
		return u.Unwrap()
	}
	return nil
}

// ---------------------------------------------------------------------------
// ingestion gate
// ---------------------------------------------------------------------------

func TestIngestionGateHappyPath(t *testing.T) {
	g, participant := newTestGate(t)

	req := IngestRequest{
		Credential:   "scred-a",
		ArtifactName: "terminal-summary.json",
		Data:         validSummaryJSON(),
		AccountID:    "acct-a",
		RevisionID:   "rev-7",
	}
	if err := g.Ingest(req); err != nil {
		t.Fatal(err.Error())
	}

	if len(g.accepted) != 1 {
		t.Fatalf("expected 1 accepted summary, got %d", len(g.accepted))
	}
	accepted := g.accepted[0]
	if accepted.ServerProcessGeneration != participant.ProcessGeneration {
		t.Fatal("accepted summary must carry the participant's process generation")
	}
	if accepted.AttemptGeneration != 9 {
		t.Fatalf("expected attempt generation 9, got %d", accepted.AttemptGeneration)
	}
	if len(g.lookupCalls) != 1 || g.lookupCalls[0] != [2]any{"acct-a", uint64(3)} {
		t.Fatalf("unexpected admission lookups: %+v", g.lookupCalls)
	}
}

func TestIngestionGateRefusals(t *testing.T) {
	t.Run("unauthenticated credential", func(t *testing.T) {
		g, _ := newTestGate(t)
		err := g.Ingest(IngestRequest{Credential: "wrong-cred", ArtifactName: "terminal-summary.json", Data: validSummaryJSON(), AccountID: "acct-a"})
		if err == nil {
			t.Fatal("expected refusal")
		}
		if len(g.accepted) != 0 {
			t.Fatal("refused ingest must not accept")
		}
	})

	t.Run("path traversal artifact name", func(t *testing.T) {
		g, _ := newTestGate(t)
		if err := g.Ingest(IngestRequest{Credential: "scred-a", ArtifactName: "../terminal-summary.json", Data: validSummaryJSON(), AccountID: "acct-a"}); err == nil {
			t.Fatal("expected traversal refusal")
		}
		if err := g.Ingest(IngestRequest{Credential: "scred-a", ArtifactName: "", Data: validSummaryJSON(), AccountID: "acct-a"}); err == nil {
			t.Fatal("expected empty-name refusal")
		}
	})

	t.Run("oversize artifact", func(t *testing.T) {
		g, _ := newTestGate(t)
		err := g.Ingest(IngestRequest{Credential: "scred-a", ArtifactName: "terminal-summary.json", Data: make([]byte, MaxTerminalSummaryBytes+1), AccountID: "acct-a"})
		if err == nil {
			t.Fatal("expected oversize refusal")
		}
	})

	t.Run("malformed artifact is crash-stop", func(t *testing.T) {
		g, _ := newTestGate(t)
		err := g.Ingest(IngestRequest{Credential: "scred-a", ArtifactName: "terminal-summary.json", Data: []byte(`{"version": "runback-attempt/v1", "ending": "secure"}`), AccountID: "acct-a"})
		if err == nil {
			t.Fatal("expected malformed refusal")
		}
		if len(g.accepted) != 0 {
			t.Fatal("malformed artifact must not be accepted")
		}
	})

	t.Run("decoded generation does not match participant", func(t *testing.T) {
		g, _ := newTestGate(t)
		// The artifact claims process generation 3, but this participant is
		// bound to generation 2: identity binding must refuse.
		g.participants["scred-a"] = &ServerParticipant{ID: "srv-a", ProcessGeneration: 2}
		err := g.Ingest(IngestRequest{Credential: "scred-a", ArtifactName: "terminal-summary.json", Data: validSummaryJSON(), AccountID: "acct-a"})
		if err == nil {
			t.Fatal("expected identity mismatch refusal")
		}
	})

	t.Run("unadmitted account", func(t *testing.T) {
		g, _ := newTestGate(t)
		err := g.Ingest(IngestRequest{Credential: "scred-a", ArtifactName: "terminal-summary.json", Data: validSummaryJSON(), AccountID: "acct-stranger"})
		if err == nil {
			t.Fatal("expected refusal for unadmitted account")
		}
	})

	t.Run("summary revision does not match admission", func(t *testing.T) {
		g, _ := newTestGate(t)
		g.admissions["acct-a"] = &AdmissionRecord{AccountID: "acct-a", RevisionID: "rev-other", ProcessGeneration: 3}
		err := g.Ingest(IngestRequest{Credential: "scred-a", ArtifactName: "terminal-summary.json", Data: validSummaryJSON(), AccountID: "acct-a"})
		if err == nil {
			t.Fatal("expected revision mismatch refusal")
		}
	})

	t.Run("claimed revision does not match summary", func(t *testing.T) {
		g, _ := newTestGate(t)
		err := g.Ingest(IngestRequest{Credential: "scred-a", ArtifactName: "terminal-summary.json", Data: validSummaryJSON(), AccountID: "acct-a", RevisionID: "rev-forged"})
		if err == nil {
			t.Fatal("expected refusal when claimed revision differs from decoded revision")
		}
	})
}

func TestIngestionGateDuplicateArtifact(t *testing.T) {
	g, _ := newTestGate(t)
	req := IngestRequest{Credential: "scred-a", ArtifactName: "terminal-summary.json", Data: validSummaryJSON(), AccountID: "acct-a"}
	if err := g.Ingest(req); err != nil {
		t.Fatal(err.Error())
	}
	// The very same artifact, re-delivered from the spool, is refused.
	if err := g.Ingest(req); err == nil {
		t.Fatal("expected duplicate refusal")
	}
	if len(g.accepted) != 1 {
		t.Fatalf("expected exactly 1 accepted summary, got %d", len(g.accepted))
	}
}

func TestIngestionGateAcceptFailureIsCrashStop(t *testing.T) {
	g, _ := newTestGate(t)
	g.acceptErr = errAcceptFailed
	req := IngestRequest{Credential: "scred-a", ArtifactName: "terminal-summary.json", Data: validSummaryJSON(), AccountID: "acct-a"}
	if err := g.Ingest(req); err == nil {
		t.Fatal("expected accept failure to surface")
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

func TestIngestionGateConcurrentDuplicate(t *testing.T) {
	g, _ := newTestGate(t)
	const workers = 16
	errs := make(chan error, workers)
	for i := 0; i < workers; i++ {
		go func() {
			errs <- g.Ingest(IngestRequest{Credential: "scred-a", ArtifactName: "terminal-summary.json", Data: validSummaryJSON(), AccountID: "acct-a"})
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
			data := patchSummaryJSON(t, `"attempt_generation": 9`, `"attempt_generation": `+itoa(n+100))
			errs <- g.Ingest(IngestRequest{Credential: "scred-a", ArtifactName: "terminal-summary.json", Data: data, AccountID: "acct-a"})
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

// itoa is a tiny helper for building distinct artifact payloads in tests.
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

// ---------------------------------------------------------------------------
// ingestion gate test harness
// ---------------------------------------------------------------------------

// errAcceptFailed is the injected acceptance failure.
var errAcceptFailed = fmt.Errorf("test: accept failed")

// testGate is a SummaryIngestionGate wired to in-memory test state.
type testGate struct {
	*SummaryIngestionGate
	participants map[string]*ServerParticipant
	admissions   map[string]*AdmissionRecord
	lookupCalls  [][2]any
	accepted     []*TerminalSummary
	acceptErr    error

	mtx sync.Mutex
}

// newTestGate builds a gate with one authenticated participant bound to
// process generation 3 (matching the valid artifact) and one admitted
// account.
func newTestGate(t *testing.T) (*testGate, *ServerParticipant) {
	t.Helper()
	g := &testGate{
		participants: map[string]*ServerParticipant{
			"scred-a": {ID: "srv-a", ProcessGeneration: 3},
		},
		admissions: map[string]*AdmissionRecord{
			"acct-a": {AccountID: "acct-a", RevisionID: "rev-7", ProcessGeneration: 3},
		},
	}
	gate, err := NewSummaryIngestionGate(
		gateResolverFunc(func(credential string) (*ServerParticipant, error) {
			g.mtx.Lock()
			defer g.mtx.Unlock()
			p, ok := g.participants[credential]
			if !ok {
				return nil, ErrUnauthenticated
			}
			return p, nil
		}),
		func(accountID string, processGeneration uint64) *AdmissionRecord {
			g.mtx.Lock()
			defer g.mtx.Unlock()
			g.lookupCalls = append(g.lookupCalls, [2]any{accountID, processGeneration})
			adm, ok := g.admissions[accountID]
			if !ok || adm.ProcessGeneration != processGeneration {
				return nil
			}
			return adm
		},
		func(summary *TerminalSummary) error {
			g.mtx.Lock()
			defer g.mtx.Unlock()
			if g.acceptErr != nil {
				return g.acceptErr
			}
			g.accepted = append(g.accepted, summary)
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
type gateResolverFunc func(credential string) (*ServerParticipant, error)

// ResolveParticipant resolves the credential.
func (f gateResolverFunc) ResolveParticipant(credential string) (*ServerParticipant, error) {
	return f(credential)
}

// TestNewIngestionGateValidation covers constructor refusals.
func TestNewIngestionGateValidation(t *testing.T) {
	resolver := gateResolverFunc(func(string) (*ServerParticipant, error) { return nil, ErrUnauthenticated })
	lookup := func(string, uint64) *AdmissionRecord { return nil }
	accept := func(*TerminalSummary) error { return nil }

	if _, err := NewSummaryIngestionGate(nil, lookup, accept); err == nil {
		t.Fatal("expected refusal without a resolver")
	}
	if _, err := NewSummaryIngestionGate(resolver, nil, accept); err == nil {
		t.Fatal("expected refusal without an admission lookup")
	}
	if _, err := NewSummaryIngestionGate(resolver, lookup, nil); err == nil {
		t.Fatal("expected refusal without an accept callback")
	}
	if _, err := NewSummaryIngestionGate(resolver, lookup, accept); err != nil {
		t.Fatalf("expected success, got %v", err)
	}
}
