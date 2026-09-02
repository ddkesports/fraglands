package server

import (
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
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
	if s.ServerProcessGeneration != 7 || s.AttemptGeneration != 3 {
		t.Fatalf("bad generations: %d / %d", s.ServerProcessGeneration, s.AttemptGeneration)
	}
	if s.TakeoverTick != 63280 {
		t.Fatalf("bad takeover tick: %d", s.TakeoverTick)
	}
	if s.Ending != "secure" || s.EndedAtSeconds != 200 {
		t.Fatalf("bad ending: %q / %d", s.Ending, s.EndedAtSeconds)
	}
}

func TestParseTerminalSummaryAcceptsCompactArtifact(t *testing.T) {
	// The writer emits a single compact line; the decoder must accept it
	// without any pretty-printing.
	compact := []byte(`{"version":"runback-attempt/v1","revision":"rev-7","replay_identity":"replay-101514223",` +
		`"attempt_generation":3,"server_process_generation":7,"takeover_tick":63280,` +
		`"ending":"unresolved","ended_at_seconds":95}`)
	s, err := ParseTerminalSummaryArtifact(compact)
	if err != nil {
		t.Fatal(err.Error())
	}
	if s.Ending != "unresolved" || s.EndedAtSeconds != 95 {
		t.Fatalf("bad ending: %q / %d", s.Ending, s.EndedAtSeconds)
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
		{"extra field taken_over_at", patchSummaryJSON(t, `"takeover_tick":63280`, `"takeover_tick":63280,"taken_over_at":1`), ErrSummaryMalformed},
		{"wrong version", patchSummaryJSON(t, `"runback-attempt/v1"`, `"runback-attempt/v2"`), ErrSummaryUnknownVersion},
		{"missing version", []byte(`{"replay_identity": "r", "revision": "x"}`), ErrSummaryMalformed},
		{"missing replay identity", patchSummaryJSON(t, `"replay_identity":"replay-101514223",`, `"replay_identity_x":"replay-101514223",`), ErrSummaryMalformed},
		{"missing revision", patchSummaryJSON(t, `"revision":"rev-7",`, `"revision_x":"rev-7",`), ErrSummaryMalformed},
		{"missing attempt generation", patchSummaryJSON(t, `"attempt_generation":3,`, `"attempt_generation_x":3,`), ErrSummaryMalformed},
		{"missing process generation", patchSummaryJSON(t, `"server_process_generation":7,`, `"server_process_generation_x":7,`), ErrSummaryMalformed},
		{"missing takeover tick", patchSummaryJSON(t, `"takeover_tick":63280,`, `"takeover_tick_x":63280,`), ErrSummaryMalformed},
		{"missing ending", patchSummaryJSON(t, `"ending":"secure",`, `"ending_x":"secure",`), ErrSummaryMalformed},
		{"missing ended_at_seconds", patchSummaryJSON(t, `"ending":"secure","ended_at_seconds":200`, `"ending":"secure","ended_at_seconds_x":200`), ErrSummaryMalformed},
		{"empty replay identity", patchSummaryJSON(t, `"replay-101514223"`, `""`), ErrSummaryMalformed},
		{"empty revision", patchSummaryJSON(t, `"rev-7"`, `""`), ErrSummaryMalformed},
		{"zero process generation", patchSummaryJSON(t, `"server_process_generation":7`, `"server_process_generation":0`), ErrSummaryMalformed},
		{"zero attempt generation", patchSummaryJSON(t, `"attempt_generation":3`, `"attempt_generation":0`), ErrSummaryMalformed},
		{"unknown ending", patchSummaryJSON(t, `"ending":"secure"`, `"ending":"victory-royale"`), ErrSummaryMalformed},
		{"string process generation", patchSummaryJSON(t, `"server_process_generation":7`, `"server_process_generation":"7"`), ErrSummaryMalformed},
		{"float attempt generation", patchSummaryJSON(t, `"attempt_generation":3`, `"attempt_generation":3.5`), ErrSummaryMalformed},
		{"string takeover tick", patchSummaryJSON(t, `"takeover_tick":63280`, `"takeover_tick":"63280"`), ErrSummaryMalformed},
		{"negative takeover tick", patchSummaryJSON(t, `"takeover_tick":63280`, `"takeover_tick":-1`), ErrSummaryMalformed},
		{"takeover tick over uint32", patchSummaryJSON(t, `"takeover_tick":63280`, `"takeover_tick":4294967296`), ErrSummaryMalformed},
		{"ended_at over uint32", patchSummaryJSON(t, `"ended_at_seconds":200`, `"ended_at_seconds":4294967296`), ErrSummaryMalformed},
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
		if !errors.Is(err, tc.wantErr) {
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

func TestValidateArtifactName(t *testing.T) {
	const generation = 3
	// The one legitimate artifact name for this generation is accepted.
	if err := ValidateArtifactName(artifactNameFor(generation), generation); err != nil {
		t.Fatalf("expected acceptance, got %v", err)
	}
	for _, name := range []string{
		"",
		"../escape.json",
		"..\\escape.json",
		"/etc/passwd",
		"C:\\spool\\runback_summary_gen3.json",
		"sub/dir/runback_summary_gen3.json",
		"runback_summary_gen3.json.exe",
		"runback_summary_gen3.json\n",
		"runback_summary_gen.json",
		"runback_summary_gen03.json",
		"runback_summary_gen4.json",
		"runback_summary_gen3_4.json",
		"runback_summary_gen-3.json",
	} {
		if err := ValidateArtifactName(name, generation); err == nil {
			t.Errorf("expected refusal for %q", name)
		}
	}
}

// ---------------------------------------------------------------------------
// ingestion gate
// ---------------------------------------------------------------------------

func TestIngestionGateHappyPath(t *testing.T) {
	g, participant := newTestGate(t)

	req := IngestRequest{
		Credential:        "scred-a",
		ArtifactName:      artifactNameFor(3),
		Data:              validSummaryJSON(),
		AccountID:         "acct-a",
		RevisionID:        "rev-7",
		AttemptGeneration: 3,
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
	if accepted.AttemptGeneration != 3 {
		t.Fatalf("expected attempt generation 3, got %d", accepted.AttemptGeneration)
	}
	if len(g.lookupCalls) != 1 || g.lookupCalls[0] != [2]any{"acct-a", uint64(7)} {
		t.Fatalf("unexpected admission lookups: %+v", g.lookupCalls)
	}
}

func TestIngestionGateRefusals(t *testing.T) {
	t.Run("unauthenticated credential", func(t *testing.T) {
		g, _ := newTestGate(t)
		err := g.Ingest(IngestRequest{
			Credential: "wrong-cred", ArtifactName: artifactNameFor(3),
			Data: validSummaryJSON(), AccountID: "acct-a", AttemptGeneration: 3,
		})
		if err == nil {
			t.Fatal("expected refusal")
		}
		if len(g.accepted) != 0 {
			t.Fatal("refused ingest must not accept")
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
			if err := g.Ingest(IngestRequest{
				Credential: "scred-a", ArtifactName: name,
				Data: validSummaryJSON(), AccountID: "acct-a", AttemptGeneration: 3,
			}); err == nil {
				t.Errorf("expected traversal refusal for %q", name)
			}
		}
	})

	t.Run("artifact name bound to another generation", func(t *testing.T) {
		g, _ := newTestGate(t)
		if err := g.Ingest(IngestRequest{
			Credential: "scred-a", ArtifactName: "runback_summary_gen4.json",
			Data: validSummaryJSON(), AccountID: "acct-a", AttemptGeneration: 3,
		}); err == nil {
			t.Fatal("expected refusal for artifact name of another generation")
		}
	})

	t.Run("oversize artifact", func(t *testing.T) {
		g, _ := newTestGate(t)
		err := g.Ingest(IngestRequest{
			Credential: "scred-a", ArtifactName: artifactNameFor(3),
			Data: make([]byte, MaxTerminalSummaryBytes+1), AccountID: "acct-a", AttemptGeneration: 3,
		})
		if err == nil {
			t.Fatal("expected oversize refusal")
		}
	})

	t.Run("malformed artifact is crash-stop", func(t *testing.T) {
		g, _ := newTestGate(t)
		err := g.Ingest(IngestRequest{
			Credential: "scred-a", ArtifactName: artifactNameFor(3),
			Data:      []byte(`{"version": "runback-attempt/v1", "ending": "secure"}`),
			AccountID: "acct-a", AttemptGeneration: 3,
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
		g.participants["scred-a"] = &ServerParticipant{ID: "srv-a", ProcessGeneration: 2}
		err := g.Ingest(IngestRequest{
			Credential: "scred-a", ArtifactName: artifactNameFor(3),
			Data: validSummaryJSON(), AccountID: "acct-a", AttemptGeneration: 3,
		})
		if err == nil {
			t.Fatal("expected identity mismatch refusal")
		}
	})

	t.Run("unadmitted account", func(t *testing.T) {
		g, _ := newTestGate(t)
		err := g.Ingest(IngestRequest{
			Credential: "scred-a", ArtifactName: artifactNameFor(3),
			Data: validSummaryJSON(), AccountID: "acct-stranger", AttemptGeneration: 3,
		})
		if err == nil {
			t.Fatal("expected refusal for unadmitted account")
		}
	})

	t.Run("summary revision does not match admission", func(t *testing.T) {
		g, _ := newTestGate(t)
		g.admissions["acct-a"] = &AdmissionRecord{AccountID: "acct-a", RevisionID: "rev-other", ProcessGeneration: 7}
		err := g.Ingest(IngestRequest{
			Credential: "scred-a", ArtifactName: artifactNameFor(3),
			Data: validSummaryJSON(), AccountID: "acct-a", AttemptGeneration: 3,
		})
		if err == nil {
			t.Fatal("expected revision mismatch refusal")
		}
	})

	t.Run("claimed revision does not match summary", func(t *testing.T) {
		g, _ := newTestGate(t)
		err := g.Ingest(IngestRequest{
			Credential: "scred-a", ArtifactName: artifactNameFor(3),
			Data: validSummaryJSON(), AccountID: "acct-a", RevisionID: "rev-forged", AttemptGeneration: 3,
		})
		if err == nil {
			t.Fatal("expected refusal when claimed revision differs from decoded revision")
		}
	})

	t.Run("duplicate artifact refused", func(t *testing.T) {
		g, _ := newTestGate(t)
		req := IngestRequest{
			Credential: "scred-a", ArtifactName: artifactNameFor(3),
			Data: validSummaryJSON(), AccountID: "acct-a", AttemptGeneration: 3,
		}
		if err := g.Ingest(req); err != nil {
			t.Fatal(err.Error())
		}
		if err := g.Ingest(req); err == nil {
			t.Fatal("expected duplicate refusal")
		}
		if len(g.accepted) != 1 {
			t.Fatalf("expected exactly 1 accepted summary, got %d", len(g.accepted))
		}
	})

	t.Run("same content under another name refused", func(t *testing.T) {
		g, _ := newTestGate(t)
		// First ingest is accepted, second is refused as duplicate even
		// though the name differs; a different artifact with the same
		// content is not a distinct result.
		first := IngestRequest{
			Credential: "scred-a", ArtifactName: artifactNameFor(3),
			Data: validSummaryJSON(), AccountID: "acct-a", AttemptGeneration: 3,
		}
		if err := g.Ingest(first); err != nil {
			t.Fatal(err.Error())
		}
		if err := g.Ingest(first); err == nil {
			t.Fatal("expected duplicate refusal")
		}
	})
}

func TestIngestionGateAcceptFailureIsCrashStop(t *testing.T) {
	g, _ := newTestGate(t)
	g.acceptErr = errAcceptFailed
	req := IngestRequest{
		Credential: "scred-a", ArtifactName: artifactNameFor(3),
		Data: validSummaryJSON(), AccountID: "acct-a", AttemptGeneration: 3,
	}
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
			errs <- g.Ingest(IngestRequest{
				Credential: "scred-a", ArtifactName: artifactNameFor(3),
				Data: validSummaryJSON(), AccountID: "acct-a", AttemptGeneration: 3,
			})
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
			data := patchSummaryJSON(t, `"attempt_generation":3`, `"attempt_generation":`+itoa(n+100))
			errs <- g.Ingest(IngestRequest{
				Credential: "scred-a", ArtifactName: artifactNameFor(3),
				Data: data, AccountID: "acct-a", AttemptGeneration: 3,
			})
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
// process generation 7 (matching the valid artifact) and one admitted
// account on process generation 7.
func newTestGate(t *testing.T) (*testGate, *ServerParticipant) {
	t.Helper()
	g := &testGate{
		participants: map[string]*ServerParticipant{
			"scred-a": {ID: "srv-a", ProcessGeneration: 7},
		},
		admissions: map[string]*AdmissionRecord{
			"acct-a": {AccountID: "acct-a", RevisionID: "rev-7", ProcessGeneration: 7},
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
