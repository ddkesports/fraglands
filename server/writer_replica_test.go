package server

import (
	"strings"
	"testing"
)

var cases = map[string]string{
	"clean": `{"version":"runback-attempt/v1","revision":"rev-2026-09-01","replay_identity":"match-1234-replay","attempt_generation":7,"server_process_generation":7,"takeover_tick":8850,"ending":"secure","ended_at_seconds":213}
`,
	"quoted": `{"version":"runback-attempt/v1","revision":"rev-2026-09-01","replay_identity":"match-"quoted"-replay","attempt_generation":7,"server_process_generation":7,"takeover_tick":8850,"ending":"secure","ended_at_seconds":213}
`,
	"infrafail": `{"version":"runback-attempt/v1","revision":"rev-2026-09-01","replay_identity":"match-1234-replay","attempt_generation":7,"server_process_generation":7,"takeover_tick":8850,"ending":"infrastructure-failure","ended_at_seconds":99}
`,
}

func TestWriterReplicaDecodes(t *testing.T) {
	for name, data := range cases {
		t.Run(name, func(t *testing.T) {
			s, err := ParseTerminalSummaryArtifact([]byte(data))
			if name == "quoted" {
				// The writer does not escape quotes inside string values;
				// those bytes are invalid JSON and must be refused, not
				// salvaged.
				if err == nil {
					t.Fatalf("malformed writer bytes (unescaped quotes) unexpectedly accepted: %+v", s)
				}
				t.Logf("malformed writer bytes refused: %v", err)
				return
			}
			if err != nil {
				t.Fatalf("actual-writer bytes refused: %v", err)
			}
			t.Logf("decoded: version=%s revision=%s replay=%s sgen=%d agen=%d tick=%d ending=%s ended=%d",
				s.Version, s.Revision, s.ReplayIdentity, s.ServerProcessGeneration, s.AttemptGeneration,
				s.TakeoverTick, s.Ending, s.EndedAtSeconds)
		})
	}
}

func TestWriterArtifactNameAccepted(t *testing.T) {
	err := ValidateArtifactName("runback_summary_gen7.json", 7)
	t.Logf("ValidateArtifactName(runback_summary_gen7.json, 7) = %v", err)
	if err != nil {
		t.Errorf("writer artifact name unexpectedly refused: %v", err)
	}
}

func TestAppendModeTwoLines(t *testing.T) {
	two := strings.Repeat(strings.TrimRight(cases["clean"], "\n"), 2)
	_, err := ParseTerminalSummaryArtifact([]byte(two))
	t.Logf("two-line artifact refused: %v", err)
	if err == nil {
		t.Errorf("two-line append artifact unexpectedly accepted")
	}
}
