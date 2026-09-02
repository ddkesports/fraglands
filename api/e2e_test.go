package api

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/paralin/fraglands/core"
	"github.com/paralin/fraglands/orchestrator"
)

// fakePreparer completes preparations immediately, or fails them when
// failWith is set.
type fakePreparer struct {
	failWith *core.FailureReason
}

// Prepare moves the preparation to a terminal state.
func (f *fakePreparer) Prepare(ctx context.Context, prep *core.ScenarioPreparation) {
	if err := prep.MarkRunning(); err != nil {
		return
	}
	if f.failWith != nil {
		prep.MarkFailed(f.failWith)
		return
	}
	prep.MarkReady(&core.ScenarioRevision{
		ID:              "rev-" + prep.ID,
		ReplayID:        prep.ReplayID,
		LeadInStartTick: prep.LeadInStartTick,
		TakeoverTick:    prep.TakeoverTick,
		Fidelity:        core.FidelityPreview,
	})
}

// fakeAllocator allocates one in-memory process per ready revision.
type fakeAllocator struct {
	fail error
}

// Allocate starts one simulated server process.
func (f *fakeAllocator) Allocate(ctx context.Context, rev *core.ScenarioRevision) (*orchestrator.AllocatedProcess, error) {
	if f.fail != nil {
		return nil, f.fail
	}
	proc := &orchestrator.AllocatedProcess{
		Generation:     1,
		ConnectAddress: "10.0.0.1:27015",
	}
	proc.MarkReady(fmt.Sprintf("process ready for %s on port", rev.ID))
	return proc, nil
}

// testServer wires one API over one orchestrator and returns the test
// server plus the orchestrator for direct server-participant simulation.
func testServer(t *testing.T) (*httptest.Server, *orchestrator.Orchestrator) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)

	sources := []core.ReplaySource{{
		ID:          "replay-1",
		DisplayName: "Mid Boss Fight",
		FileName:    "mid-boss.dem",
	}}
	orch := orchestrator.NewOrchestrator(ctx, sources, &fakePreparer{}, &fakeAllocator{})
	server := httptest.NewServer(NewAPI(orch).Handler())
	t.Cleanup(server.Close)
	return server, orch
}

// callJSON performs one JSON request and decodes the response body.
func callJSON(t *testing.T, server *httptest.Server, method, path string, body any) (int, map[string]any) {
	t.Helper()
	var reader io.Reader
	if body != nil {
		buf, err := json.Marshal(body)
		if err != nil {
			t.Fatal(err.Error())
		}
		reader = bytes.NewReader(buf)
	}
	req, err := http.NewRequest(method, server.URL+path, reader)
	if err != nil {
		t.Fatal(err.Error())
	}
	resp, err := server.Client().Do(req)
	if err != nil {
		t.Fatal(err.Error())
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err.Error())
	}
	var out map[string]any
	if len(data) > 0 {
		if err := json.Unmarshal(data, &out); err != nil {
			t.Fatalf("invalid JSON response %q: %v", data, err)
		}
	}
	return resp.StatusCode, out
}

// waitStatus polls the preparation until the process is ready.
func waitStatus(t *testing.T, server *httptest.Server, prepID string) map[string]any {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		code, status := callJSON(t, server, http.MethodGet, "/preparations/"+prepID, nil)
		if code != http.StatusOK {
			t.Fatalf("expected 200 for preparation status, got %d: %v", code, status)
		}
		proc, _ := status["process"].(map[string]any)
		failure, _ := status["allocation_failure"].(map[string]any)
		if (proc != nil && proc["ready"] == true) || failure != nil {
			return status
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("timed out waiting for process readiness")
	return nil
}

// requireField asserts one field equals the wanted value.
func requireField(t *testing.T, obj map[string]any, key string, want any) {
	t.Helper()
	got, ok := obj[key]
	if !ok {
		t.Fatalf("expected field %s in %v", key, obj)
	}
	if got != want {
		t.Fatalf("expected %s=%v, got %v", key, want, got)
	}
}

// TestEndToEndJoinFlow runs the full minimal path: selection, preparation,
// status with readiness evidence, lobby claim, one-use join intent, and
// private debrief retrieval.
func TestEndToEndJoinFlow(t *testing.T) {
	server, orch := testServer(t)
	const steam = uint64(76561198000000001)

	// Selection: the catalog lists the replay.
	code, body := callJSON(t, server, http.MethodGet, "/replays", nil)
	if code != http.StatusOK {
		t.Fatalf("expected 200 for replays, got %d", code)
	}
	replays, _ := body["replays"].([]any)
	if len(replays) != 1 {
		t.Fatalf("expected 1 replay, got %v", replays)
	}
	first, _ := replays[0].(map[string]any)
	requireField(t, first, "id", "replay-1")
	requireField(t, first, "display_name", "Mid Boss Fight")

	// Preparation: the request is accepted and returns an ID immediately.
	code, body = callJSON(t, server, http.MethodPost, "/preparations", map[string]any{
		"replay_id":          "replay-1",
		"lead_in_start_tick": 63280,
		"takeover_tick":      63280,
	})
	if code != http.StatusCreated {
		t.Fatalf("expected 201 for prepare, got %d: %v", code, body)
	}
	prepID, _ := body["preparation_id"].(string)
	if prepID == "" {
		t.Fatal("expected preparation_id")
	}

	// Status: ready with explicit readiness evidence.
	status := waitStatus(t, server, prepID)
	requireField(t, status, "state", "ready")
	revision, _ := status["revision"].(map[string]any)
	if revision == nil {
		t.Fatalf("expected revision on ready status: %v", status)
	}
	revisionID, _ := revision["id"].(string)
	if revisionID == "" {
		t.Fatal("expected revision id")
	}
	proc, _ := status["process"].(map[string]any)
	requireField(t, proc, "ready", true)
	if proc["readiness_evidence"] == "" {
		t.Fatal("expected non-empty readiness evidence")
	}
	requireField(t, proc, "connect_address", "10.0.0.1:27015")

	// Lobby claim: the account reserves slot 0.
	code, body = callJSON(t, server, http.MethodPost, "/preparations/"+prepID+"/slots", map[string]any{
		"account_id": "acct-a",
	})
	if code != http.StatusOK {
		t.Fatalf("expected 200 for claim, got %d: %v", code, body)
	}
	requireField(t, body, "slot", float64(0))

	// Join intent: one-use, bound to the revision and process generation.
	code, body = callJSON(t, server, http.MethodPost, "/preparations/"+prepID+"/join-intent", map[string]any{
		"account_id": "acct-a",
		"steam_id":   steam,
	})
	if code != http.StatusCreated {
		t.Fatalf("expected 201 for join intent, got %d: %v", code, body)
	}
	requireField(t, body, "revision_id", revisionID)
	requireField(t, body, "generation", "1")
	requireField(t, body, "connect_address", "10.0.0.1:27015")
	intentID, _ := body["intent_id"].(string)

	// The server participant consumes the intent at the server: the exact
	// bound revision, generation, and Steam identity must be presented.
	if err := orch.ConsumeJoinIntent(intentID, revisionID, 1, core.SteamID(steam)); err != nil {
		t.Fatal(err.Error())
	}
	// One-use: a second presentation is refused.
	if err := orch.ConsumeJoinIntent(intentID, revisionID, 1, core.SteamID(steam)); !errors.Is(err, core.ErrIntentAlreadyUsed) {
		t.Fatalf("expected ErrIntentAlreadyUsed, got %v", err)
	}

	// The server participant accepts one private result for the attempt.
	if err := orch.AcceptResult(&core.AttemptResult{
		ID:                "res-1",
		AccountID:         "acct-a",
		RevisionID:        revisionID,
		ProcessGeneration: 1,
		AttemptGeneration: 7,
		ReplayID:          "replay-1",
		TakeoverTick:      63280,
	}); err != nil {
		t.Fatal(err.Error())
	}

	// Debrief: the private result is retrievable by its owner.
	code, body = callJSON(t, server, http.MethodGet, "/debrief?account_id=acct-a&process_generation=1&attempt_generation=7", nil)
	if code != http.StatusOK {
		t.Fatalf("expected 200 for debrief, got %d: %v", code, body)
	}
	requireField(t, body, "account_id", "acct-a")
	requireField(t, body, "revision_id", revisionID)
	requireField(t, body, "attempt_generation", "7")
	requireField(t, body, "takeover_tick", "63280")

	// Another account cannot retrieve the private result.
	code, body = callJSON(t, server, http.MethodGet, "/debrief?account_id=acct-b&process_generation=1&attempt_generation=7", nil)
	if code != http.StatusNotFound {
		t.Fatalf("expected 404 for another account debrief, got %d: %v", code, body)
	}
	requireField(t, body, "error", "no_result")
}

// TestEndToEndTypedErrors covers the typed error surface.
func TestEndToEndTypedErrors(t *testing.T) {
	server, _ := testServer(t)

	// Unknown replay fails with a typed 404.
	code, body := callJSON(t, server, http.MethodPost, "/preparations", map[string]any{
		"replay_id": "replay-none", "lead_in_start_tick": 0, "takeover_tick": 1,
	})
	if code != http.StatusNotFound {
		t.Fatalf("expected 404 for unknown replay, got %d: %v", code, body)
	}
	requireField(t, body, "error", "unknown_replay")

	// Unknown preparation status fails with a typed 404.
	code, body = callJSON(t, server, http.MethodGet, "/preparations/prep-999", nil)
	if code != http.StatusNotFound {
		t.Fatalf("expected 404 for unknown preparation, got %d: %v", code, body)
	}
	requireField(t, body, "error", "unknown_preparation")

	// Malformed request body fails with a typed 400.
	req, err := http.NewRequest(http.MethodPost, server.URL+"/preparations", strings.NewReader("{not json"))
	if err != nil {
		t.Fatal(err.Error())
	}
	resp, err := server.Client().Do(req)
	if err != nil {
		t.Fatal(err.Error())
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("expected 400 for malformed body, got %d", resp.StatusCode)
	}
}

// TestEndToEndFailedPreparation shows the typed reason with no partial state.
func TestEndToEndFailedPreparation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	sources := []core.ReplaySource{{ID: "replay-1"}}
	orch := orchestrator.NewOrchestrator(ctx, sources, &fakePreparer{
		failWith: &core.FailureReason{Code: "replay_unsupported", Message: "field kHealth unsupported"},
	}, &fakeAllocator{})
	server := httptest.NewServer(NewAPI(orch).Handler())
	defer server.Close()

	code, body := callJSON(t, server, http.MethodPost, "/preparations", map[string]any{
		"replay_id": "replay-1", "lead_in_start_tick": 0, "takeover_tick": 63280,
	})
	if code != http.StatusCreated {
		t.Fatalf("expected 201, got %d: %v", code, body)
	}
	prepID, _ := body["preparation_id"].(string)

	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		code, status := callJSON(t, server, http.MethodGet, "/preparations/"+prepID, nil)
		if code != http.StatusOK {
			t.Fatalf("expected 200, got %d", code)
		}
		if status["state"] == "failed" {
			failure, _ := status["failure"].(map[string]any)
			if failure == nil {
				t.Fatal("expected typed failure on failed status")
			}
			requireField(t, failure, "code", "replay_unsupported")
			requireField(t, failure, "message", "field kHealth unsupported")
			if status["revision"] != nil {
				t.Fatal("failed preparation must not carry a revision")
			}
			if status["process"] != nil {
				t.Fatal("failed preparation must not carry a process")
			}
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("timed out waiting for failed state")
}

// TestEndToEndAllocationFailure shows the typed allocation reason.
func TestEndToEndAllocationFailure(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	sources := []core.ReplaySource{{ID: "replay-1"}}
	orch := orchestrator.NewOrchestrator(ctx, sources, &fakePreparer{}, &fakeAllocator{
		fail: errors.New("no worker capacity in region"),
	})
	server := httptest.NewServer(NewAPI(orch).Handler())
	defer server.Close()

	code, body := callJSON(t, server, http.MethodPost, "/preparations", map[string]any{
		"replay_id": "replay-1", "lead_in_start_tick": 0, "takeover_tick": 63280,
	})
	if code != http.StatusCreated {
		t.Fatalf("expected 201, got %d: %v", code, body)
	}
	prepID, _ := body["preparation_id"].(string)

	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		code, status := callJSON(t, server, http.MethodGet, "/preparations/"+prepID, nil)
		if code != http.StatusOK {
			t.Fatalf("expected 200, got %d", code)
		}
		failure, _ := status["allocation_failure"].(map[string]any)
		if failure != nil {
			requireField(t, failure, "code", "allocation_failed")
			if status["process"] != nil {
				t.Fatal("failed allocation must not carry a process")
			}
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("timed out waiting for allocation failure")
}

// TestEndToEndJoinIntentGuards covers slot, readiness, and duplicate guards.
func TestEndToEndJoinIntentGuards(t *testing.T) {
	server, _ := testServer(t)

	// Prepare without claiming: the join intent is refused.
	code, body := callJSON(t, server, http.MethodPost, "/preparations", map[string]any{
		"replay_id": "replay-1", "lead_in_start_tick": 0, "takeover_tick": 63280,
	})
	if code != http.StatusCreated {
		t.Fatalf("expected 201, got %d: %v", code, body)
	}
	prepID, _ := body["preparation_id"].(string)
	waitStatus(t, server, prepID)

	code, body = callJSON(t, server, http.MethodPost, "/preparations/"+prepID+"/join-intent", map[string]any{
		"account_id": "acct-a", "steam_id": uint64(76561198000000001),
	})
	if code != http.StatusConflict {
		t.Fatalf("expected 409 for join without slot, got %d: %v", code, body)
	}
	requireField(t, body, "error", "no_slot_claimed")

	// Claim, then the intent succeeds.
	code, _ = callJSON(t, server, http.MethodPost, "/preparations/"+prepID+"/slots", map[string]any{
		"account_id": "acct-a",
	})
	if code != http.StatusOK {
		t.Fatalf("expected 200 for claim, got %d", code)
	}
	code, body = callJSON(t, server, http.MethodPost, "/preparations/"+prepID+"/join-intent", map[string]any{
		"account_id": "acct-a", "steam_id": uint64(76561198000000001),
	})
	if code != http.StatusCreated {
		t.Fatalf("expected 201 for join intent, got %d: %v", code, body)
	}

	// A zero Steam identity is refused.
	code, body = callJSON(t, server, http.MethodPost, "/preparations/"+prepID+"/join-intent", map[string]any{
		"account_id": "acct-a", "steam_id": uint64(0),
	})
	if code != http.StatusBadRequest {
		t.Fatalf("expected 400 for zero steam id, got %d: %v", code, body)
	}
	requireField(t, body, "error", "invalid_request")
}
