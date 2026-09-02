package server

import (
	"errors"
	"testing"
)

// The shared fixture helpers validSummaryJSON, artifactNameFor and
// patchSummaryJSON live in composition_test.go.

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

func TestParseTerminalSummaryDuplicateFields(t *testing.T) {
	// Every wire field, duplicated in the artifact. The contract is an
	// immutable versioned record: a repeated key means the artifact was
	// tampered with or written by a different writer, so every duplicate
	// is a refusal with no partial summary, regardless of the field's
	// security relevance.
	dupes := []struct {
		name    string
		literal string
	}{
		{"version", `"version":"runback-attempt/v1"`},
		{"replay_identity", `"replay_identity":"replay-101514223"`},
		{"revision", `"revision":"rev-7"`},
		{"server_process_generation", `"server_process_generation":7`},
		{"attempt_generation", `"attempt_generation":3`},
		{"takeover_tick", `"takeover_tick":63280`},
		{"ending", `"ending":"secure"`},
		{"ended_at_seconds", `"ended_at_seconds":200`},
	}
	for _, tc := range dupes {
		data := patchSummaryJSON(t, `"ended_at_seconds":200}`, `"ended_at_seconds":200,`+tc.literal+`}`)
		s, err := ParseTerminalSummaryArtifact(data)
		if err == nil {
			t.Errorf("duplicate %s: expected refusal, got summary %+v", tc.name, s)
			continue
		}
		if s != nil {
			t.Errorf("duplicate %s: refusal must not carry a partial summary", tc.name)
		}
		if !errors.Is(err, ErrSummaryMalformed) {
			t.Errorf("duplicate %s: expected ErrSummaryMalformed, got %v", tc.name, err)
		}
	}

	// A duplicate adjacent to its original is refused the same way: key
	// position inside the object must not matter.
	adjacent := patchSummaryJSON(t, `"revision":"rev-7",`, `"revision":"rev-7","revision":"rev-7",`)
	s, err := ParseTerminalSummaryArtifact(adjacent)
	if err == nil {
		t.Fatalf("adjacent duplicate revision: expected refusal, got summary %+v", s)
	}
	if s != nil {
		t.Fatal("adjacent duplicate revision: refusal must not carry a partial summary")
	}
	if !errors.Is(err, ErrSummaryMalformed) {
		t.Fatalf("adjacent duplicate revision: expected ErrSummaryMalformed, got %v", err)
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
