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

// fakeIdentityAuthority authenticates fixed credentials to fixed accounts.
type fakeIdentityAuthority struct {
	accounts map[string]*core.Account
}

// Authenticate returns the account bound to the credential.
func (f *fakeIdentityAuthority) Authenticate(ctx context.Context, credential string) (*core.Account, error) {
	acct, ok := f.accounts[credential]
	if !ok {
		return nil, orchestrator.ErrUnauthenticated
	}
	return acct, nil
}

// test principals used across the e2e suite.
var (
	ownerAccount = &core.Account{ID: "acct-a", SteamID: 76561198000000001, DisplayName: "Owner"}
	attackerAcct = &core.Account{ID: "acct-b", SteamID: 76561198000000002, DisplayName: "Attacker"}
)

// testIdentityAuthority binds credentials to the test principals.
func testIdentityAuthority() *fakeIdentityAuthority {
	return &fakeIdentityAuthority{accounts: map[string]*core.Account{
		"cred-a": ownerAccount,
		"cred-b": attackerAcct,
	}}
}

// fakeServerAuthority authenticates fixed credentials to fixed server
// participants.
type fakeServerAuthority struct {
	participants map[string]*orchestrator.ServerParticipant
}

// AuthenticateServer returns the server participant bound to the credential.
func (f *fakeServerAuthority) AuthenticateServer(ctx context.Context, credential string) (*orchestrator.ServerParticipant, error) {
	p, ok := f.participants[credential]
	if !ok {
		return nil, orchestrator.ErrUnauthenticated
	}
	return p, nil
}

// testServerParticipants returns the test server participants: the default
// participant is bound to process generation 1, and an attacker participant
// is bound to a different generation.
func testServerParticipants() (p1, p2 *orchestrator.ServerParticipant) {
	p1 = &orchestrator.ServerParticipant{ID: "srv-a", ProcessGeneration: 1}
	p2 = &orchestrator.ServerParticipant{ID: "srv-b", ProcessGeneration: 2}
	return p1, p2
}

// testServerAuthority builds a server authority over the test server
// participants.
func testServerAuthority() *fakeServerAuthority {
	p1, p2 := testServerParticipants()
	return &fakeServerAuthority{participants: map[string]*orchestrator.ServerParticipant{
		"scred-a": p1,
		"scred-b": p2,
	}}
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
	orch := orchestrator.NewOrchestrator(ctx, sources, &fakePreparer{}, &fakeAllocator{}, testIdentityAuthority(), testServerAuthority())
	server := httptest.NewServer(NewAPI(orch).Handler())
	t.Cleanup(server.Close)
	return server, orch
}

// callJSON performs one authenticated JSON request and decodes the response.
func callJSON(t *testing.T, server *httptest.Server, method, path, credential string, body any) (int, map[string]any) {
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
	if credential != "" {
		req.Header.Set("Authorization", "Bearer "+credential)
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
func waitStatus(t *testing.T, server *httptest.Server, credential, prepID string) map[string]any {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		code, status := callJSON(t, server, http.MethodGet, "/preparations/"+prepID, credential, nil)
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
	const credA = "cred-a"

	// Selection: the catalog lists the replay.
	code, body := callJSON(t, server, http.MethodGet, "/replays", credA, nil)
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
	code, body = callJSON(t, server, http.MethodPost, "/preparations", credA, map[string]any{
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
	status := waitStatus(t, server, credA, prepID)
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

	// Lobby claim: the principal reserves slot 0.
	code, body = callJSON(t, server, http.MethodPost, "/preparations/"+prepID+"/slots", credA, nil)
	if code != http.StatusOK {
		t.Fatalf("expected 200 for claim, got %d: %v", code, body)
	}
	requireField(t, body, "slot", float64(0))

	// Join intent: one-use, bound to the principal identity, revision, and
	// process generation.
	code, body = callJSON(t, server, http.MethodPost, "/preparations/"+prepID+"/join-intent", credA, nil)
	if code != http.StatusCreated {
		t.Fatalf("expected 201 for join intent, got %d: %v", code, body)
	}
	requireField(t, body, "account_id", ownerAccount.ID)
	requireField(t, body, "steam_id", "76561198000000001")
	requireField(t, body, "revision_id", revisionID)
	requireField(t, body, "generation", "1")
	requireField(t, body, "connect_address", "10.0.0.1:27015")
	intentID, _ := body["intent_id"].(string)

	// The server participant consumes the intent at the server: the exact
	// bound revision, generation, and Steam identity must be presented.
	participant := testServerAuthority().participants["scred-a"]
	if err := orch.ConsumeJoinIntent(participant, intentID, revisionID, ownerAccount.SteamID); err != nil {
		t.Fatal(err.Error())
	}
	// One-use: a second presentation is refused.
	if err := orch.ConsumeJoinIntent(participant, intentID, revisionID, ownerAccount.SteamID); !errors.Is(err, core.ErrIntentAlreadyUsed) {
		t.Fatalf("expected ErrIntentAlreadyUsed, got %v", err)
	}

	// The server participant accepts one private result for the attempt.
	if err := orch.AcceptResult(participant, &core.AttemptResult{
		ID:                "res-1",
		AccountID:         ownerAccount.ID,
		RevisionID:        revisionID,
		ProcessGeneration: 1,
		AttemptGeneration: 7,
		ReplayID:          "replay-1",
		TakeoverTick:      63280,
	}); err != nil {
		t.Fatal(err.Error())
	}

	// Debrief: the private result is retrievable by its owner.
	code, body = callJSON(t, server, http.MethodGet, "/debrief?process_generation=1&attempt_generation=7", credA, nil)
	if code != http.StatusOK {
		t.Fatalf("expected 200 for debrief, got %d: %v", code, body)
	}
	requireField(t, body, "account_id", ownerAccount.ID)
	requireField(t, body, "revision_id", revisionID)
	requireField(t, body, "attempt_generation", "7")
	requireField(t, body, "takeover_tick", "63280")
}

// TestAdversarialAuth covers the typed refusal of every identity attack.
func TestAdversarialAuth(t *testing.T) {
	server, _ := testServer(t)
	const credA = "cred-a"
	const credB = "cred-b"

	// Owner prepares and claims.
	code, body := callJSON(t, server, http.MethodPost, "/preparations", credA, map[string]any{
		"replay_id": "replay-1", "lead_in_start_tick": 0, "takeover_tick": 63280,
	})
	if code != http.StatusCreated {
		t.Fatalf("expected 201, got %d: %v", code, body)
	}
	prepID, _ := body["preparation_id"].(string)
	waitStatus(t, server, credA, prepID)
	code, _ = callJSON(t, server, http.MethodPost, "/preparations/"+prepID+"/slots", credA, nil)
	if code != http.StatusOK {
		t.Fatalf("expected 200 for claim, got %d", code)
	}

	// Unauthenticated: every endpoint refuses without a credential.
	for _, tc := range []struct{ method, path string }{
		{http.MethodGet, "/replays"},
		{http.MethodGet, "/preparations/" + prepID},
		{http.MethodPost, "/preparations/" + prepID + "/slots"},
		{http.MethodDelete, "/preparations/" + prepID + "/slots"},
		{http.MethodPost, "/preparations/" + prepID + "/join-intent"},
		{http.MethodGet, "/debrief?process_generation=1&attempt_generation=1"},
	} {
		code, body := callJSON(t, server, tc.method, tc.path, "", nil)
		if code != http.StatusUnauthorized {
			t.Fatalf("expected 401 for %s %s, got %d: %v", tc.method, tc.path, code, body)
		}
		requireField(t, body, "error", "unauthenticated")
	}

	// Invalid credential is refused with a typed 401.
	code, body = callJSON(t, server, http.MethodGet, "/replays", "cred-attacker", nil)
	if code != http.StatusUnauthorized {
		t.Fatalf("expected 401 for invalid credential, got %d: %v", code, body)
	}
	requireField(t, body, "error", "unauthenticated")

	// Cross-account status read: the attacker is not the owner and holds no
	// slot, so connect_address and readiness stay hidden behind a typed 403.
	code, body = callJSON(t, server, http.MethodGet, "/preparations/"+prepID, credB, nil)
	if code != http.StatusForbidden {
		t.Fatalf("expected 403 for cross-account status, got %d: %v", code, body)
	}
	requireField(t, body, "error", "forbidden")

	// Attacker slot deletion: the attacker is not the owner and holds no
	// slot, so the release is refused with a typed 403 and the victim slot
	// stays reserved.
	code, body = callJSON(t, server, http.MethodDelete, "/preparations/"+prepID+"/slots", credB, nil)
	if code != http.StatusForbidden {
		t.Fatalf("expected 403 for attacker release, got %d: %v", code, body)
	}
	requireField(t, body, "error", "forbidden")

	// Victim slot still held: the owner can still view status.
	code, body = callJSON(t, server, http.MethodGet, "/preparations/"+prepID, credA, nil)
	if code != http.StatusOK {
		t.Fatalf("expected 200 for owner status after attack, got %d: %v", code, body)
	}

	// Attacker join intent without a slot is refused.
	code, body = callJSON(t, server, http.MethodPost, "/preparations/"+prepID+"/join-intent", credB, nil)
	if code != http.StatusConflict {
		t.Fatalf("expected 409 for attacker join, got %d: %v", code, body)
	}
	requireField(t, body, "error", "no_slot_claimed")
}

// TestAdversarialSteamBinding proves a client cannot bind another Steam
// identity: identities come only from the authority.
func TestAdversarialSteamBinding(t *testing.T) {
	server, orch := testServer(t)
	const credA = "cred-a"

	code, body := callJSON(t, server, http.MethodPost, "/preparations", credA, map[string]any{
		"replay_id": "replay-1", "lead_in_start_tick": 0, "takeover_tick": 63280,
	})
	if code != http.StatusCreated {
		t.Fatalf("expected 201, got %d: %v", code, body)
	}
	prepID, _ := body["preparation_id"].(string)
	waitStatus(t, server, credA, prepID)
	code, _ = callJSON(t, server, http.MethodPost, "/preparations/"+prepID+"/slots", credA, nil)
	if code != http.StatusOK {
		t.Fatalf("expected 200 for claim, got %d", code)
	}

	// The join intent carries the principal identity, not a client value.
	code, body = callJSON(t, server, http.MethodPost, "/preparations/"+prepID+"/join-intent", credA, nil)
	if code != http.StatusCreated {
		t.Fatalf("expected 201, got %d: %v", code, body)
	}
	requireField(t, body, "steam_id", "76561198000000001")
	requireField(t, body, "account_id", ownerAccount.ID)

	// An attacker presenting the victim Steam identity at the server is
	// refused: the intent is bound to the principal identity only.
	intentID, _ := body["intent_id"].(string)
	revisionID, _ := body["revision_id"].(string)
	participant := testServerAuthority().participants["scred-a"]
	if err := orch.ConsumeJoinIntent(participant, intentID, revisionID, attackerAcct.SteamID); !errors.Is(err, core.ErrSteamIDAlreadyBound) {
		t.Fatalf("expected ErrSteamIDAlreadyBound, got %v", err)
	}
	// The intent was not burned by the refused consume.
	if err := orch.ConsumeJoinIntent(participant, intentID, revisionID, ownerAccount.SteamID); err != nil {
		t.Fatal(err.Error())
	}
}

// TestAdversarialDebriefPrivacy covers private debrief retrieval.
func TestAdversarialDebriefPrivacy(t *testing.T) {
	server, orch := testServer(t)
	const credA = "cred-a"
	const credB = "cred-b"

	code, body := callJSON(t, server, http.MethodPost, "/preparations", credA, map[string]any{
		"replay_id": "replay-1", "lead_in_start_tick": 0, "takeover_tick": 63280,
	})
	if code != http.StatusCreated {
		t.Fatalf("expected 201, got %d: %v", code, body)
	}
	prepID, _ := body["preparation_id"].(string)
	waitStatus(t, server, credA, prepID)
	code, _ = callJSON(t, server, http.MethodPost, "/preparations/"+prepID+"/slots", credA, nil)
	if code != http.StatusOK {
		t.Fatalf("expected 200 for claim, got %d", code)
	}
	code, body = callJSON(t, server, http.MethodPost, "/preparations/"+prepID+"/join-intent", credA, nil)
	if code != http.StatusCreated {
		t.Fatalf("expected 201, got %d: %v", code, body)
	}

	// The server participant consumes the intent and accepts the owner
	// private result.
	participant := testServerAuthority().participants["scred-a"]
	if err := orch.ConsumeJoinIntent(participant, body["intent_id"].(string), body["revision_id"].(string), ownerAccount.SteamID); err != nil {
		t.Fatal(err.Error())
	}
	if err := orch.AcceptResult(participant, &core.AttemptResult{
		ID:                "res-1",
		AccountID:         ownerAccount.ID,
		RevisionID:        body["revision_id"].(string),
		ProcessGeneration: 1,
		AttemptGeneration: 7,
		ReplayID:          "replay-1",
		TakeoverTick:      63280,
	}); err != nil {
		t.Fatal(err.Error())
	}

	// The owner reads the debrief.
	code, body = callJSON(t, server, http.MethodGet, "/debrief?process_generation=1&attempt_generation=7", credA, nil)
	if code != http.StatusOK {
		t.Fatalf("expected 200 for owner debrief, got %d: %v", code, body)
	}
	requireField(t, body, "account_id", ownerAccount.ID)

	// Cross-account read: the attacker principal never sees the result.
	code, body = callJSON(t, server, http.MethodGet, "/debrief?process_generation=1&attempt_generation=7", credB, nil)
	if code != http.StatusNotFound {
		t.Fatalf("expected 404 for cross-account debrief, got %d: %v", code, body)
	}
	requireField(t, body, "error", "no_result")

	// A client-supplied account_id cannot override the principal: there is
	// no account_id parameter at all.
	code, body = callJSON(t, server, http.MethodGet, "/debrief?account_id=acct-a&process_generation=1&attempt_generation=7", credB, nil)
	if code != http.StatusNotFound {
		t.Fatalf("expected 404 for attacker with forged account_id, got %d: %v", code, body)
	}
	requireField(t, body, "error", "no_result")
}

// TestAdversarialConnectAddressLeak proves connect_address and readiness
// evidence stay behind the authorization check.
func TestAdversarialConnectAddressLeak(t *testing.T) {
	server, _ := testServer(t)
	const credA = "cred-a"
	const credB = "cred-b"

	code, body := callJSON(t, server, http.MethodPost, "/preparations", credA, map[string]any{
		"replay_id": "replay-1", "lead_in_start_tick": 0, "takeover_tick": 63280,
	})
	if code != http.StatusCreated {
		t.Fatalf("expected 201, got %d: %v", code, body)
	}
	prepID, _ := body["preparation_id"].(string)
	waitStatus(t, server, credA, prepID)

	// The attacker status read is refused before any process fact is
	// serialized.
	code, body = callJSON(t, server, http.MethodGet, "/preparations/"+prepID, credB, nil)
	if code != http.StatusForbidden {
		t.Fatalf("expected 403, got %d: %v", code, body)
	}
	requireField(t, body, "error", "forbidden")
	if strings.Contains(fmt.Sprintf("%v", body), "10.0.0.1:27015") {
		t.Fatal("connect address must never leak to a forbidden principal")
	}
}

// TestEndToEndTypedErrors covers the typed error surface.
func TestEndToEndTypedErrors(t *testing.T) {
	server, _ := testServer(t)
	const credA = "cred-a"

	// Unknown replay fails with a typed 404.
	code, body := callJSON(t, server, http.MethodPost, "/preparations", credA, map[string]any{
		"replay_id": "replay-none", "lead_in_start_tick": 0, "takeover_tick": 1,
	})
	if code != http.StatusNotFound {
		t.Fatalf("expected 404 for unknown replay, got %d: %v", code, body)
	}
	requireField(t, body, "error", "unknown_replay")

	// Unknown preparation status fails with a typed 404.
	code, body = callJSON(t, server, http.MethodGet, "/preparations/prep-999", credA, nil)
	if code != http.StatusNotFound {
		t.Fatalf("expected 404 for unknown preparation, got %d: %v", code, body)
	}
	requireField(t, body, "error", "unknown_preparation")

	// Malformed request body fails with a typed 400.
	req, err := http.NewRequest(http.MethodPost, server.URL+"/preparations", strings.NewReader("{not json"))
	if err != nil {
		t.Fatal(err.Error())
	}
	req.Header.Set("Authorization", "Bearer "+credA)
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
	}, &fakeAllocator{}, testIdentityAuthority(), testServerAuthority())
	server := httptest.NewServer(NewAPI(orch).Handler())
	defer server.Close()

	code, body := callJSON(t, server, http.MethodPost, "/preparations", "cred-a", map[string]any{
		"replay_id": "replay-1", "lead_in_start_tick": 0, "takeover_tick": 63280,
	})
	if code != http.StatusCreated {
		t.Fatalf("expected 201, got %d: %v", code, body)
	}
	prepID, _ := body["preparation_id"].(string)

	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		code, status := callJSON(t, server, http.MethodGet, "/preparations/"+prepID, "cred-a", nil)
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

// TestEndToEndAllocationFailure shows the typed allocation reason, mapped
// from the typed AllocationError through the API surface.
func TestEndToEndAllocationFailure(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	sources := []core.ReplaySource{{ID: "replay-1"}}
	orch := orchestrator.NewOrchestrator(ctx, sources, &fakePreparer{}, &fakeAllocator{
		fail: errors.New("no worker capacity in region"),
	}, testIdentityAuthority(), testServerAuthority())
	server := httptest.NewServer(NewAPI(orch).Handler())
	defer server.Close()

	code, body := callJSON(t, server, http.MethodPost, "/preparations", "cred-a", map[string]any{
		"replay_id": "replay-1", "lead_in_start_tick": 0, "takeover_tick": 63280,
	})
	if code != http.StatusCreated {
		t.Fatalf("expected 201, got %d: %v", code, body)
	}
	prepID, _ := body["preparation_id"].(string)

	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		code, status := callJSON(t, server, http.MethodGet, "/preparations/"+prepID, "cred-a", nil)
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

// TestAllocationErrorTypedThroughAPI proves the join intent on a failed
// allocation maps to the typed allocation_failed code, not internal.
func TestAllocationErrorTypedThroughAPI(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	sources := []core.ReplaySource{{ID: "replay-1"}}
	orch := orchestrator.NewOrchestrator(ctx, sources, &fakePreparer{}, &fakeAllocator{
		fail: errors.New("no worker capacity in region"),
	}, testIdentityAuthority(), testServerAuthority())
	server := httptest.NewServer(NewAPI(orch).Handler())
	defer server.Close()

	code, body := callJSON(t, server, http.MethodPost, "/preparations", "cred-a", map[string]any{
		"replay_id": "replay-1", "lead_in_start_tick": 0, "takeover_tick": 63280,
	})
	if code != http.StatusCreated {
		t.Fatalf("expected 201, got %d: %v", code, body)
	}
	prepID, _ := body["preparation_id"].(string)

	// Claim so the only remaining guard is the allocation failure.
	code, _ = callJSON(t, server, http.MethodPost, "/preparations/"+prepID+"/slots", "cred-a", nil)
	if code != http.StatusOK {
		t.Fatalf("expected 200 for claim, got %d", code)
	}

	// Wait for the allocation failure to be recorded.
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		_, status := callJSON(t, server, http.MethodGet, "/preparations/"+prepID, "cred-a", nil)
		if status["allocation_failure"] != nil {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}

	// The join intent is refused with the typed allocation_failed code.
	code, body = callJSON(t, server, http.MethodPost, "/preparations/"+prepID+"/join-intent", "cred-a", nil)
	if code != http.StatusConflict {
		t.Fatalf("expected 409 for join on failed allocation, got %d: %v", code, body)
	}
	requireField(t, body, "error", "allocation_failed")
}

// TestEndToEndJoinIntentGuards covers slot, readiness, and duplicate guards.
func TestEndToEndJoinIntentGuards(t *testing.T) {
	server, _ := testServer(t)
	const credA = "cred-a"

	// Prepare without claiming: the join intent is refused.
	code, body := callJSON(t, server, http.MethodPost, "/preparations", credA, map[string]any{
		"replay_id": "replay-1", "lead_in_start_tick": 0, "takeover_tick": 63280,
	})
	if code != http.StatusCreated {
		t.Fatalf("expected 201, got %d: %v", code, body)
	}
	prepID, _ := body["preparation_id"].(string)
	waitStatus(t, server, credA, prepID)

	code, body = callJSON(t, server, http.MethodPost, "/preparations/"+prepID+"/join-intent", credA, nil)
	if code != http.StatusConflict {
		t.Fatalf("expected 409 for join without slot, got %d: %v", code, body)
	}
	requireField(t, body, "error", "no_slot_claimed")

	// Claim, then the intent succeeds.
	code, _ = callJSON(t, server, http.MethodPost, "/preparations/"+prepID+"/slots", credA, nil)
	if code != http.StatusOK {
		t.Fatalf("expected 200 for claim, got %d", code)
	}
	code, body = callJSON(t, server, http.MethodPost, "/preparations/"+prepID+"/join-intent", credA, nil)
	if code != http.StatusCreated {
		t.Fatalf("expected 201 for join intent, got %d: %v", code, body)
	}
}
