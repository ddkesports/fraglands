package web

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/paralin/fraglands/core"
	"github.com/paralin/fraglands/orchestrator"
)

// fakePreparer completes preparations immediately as a Preview revision
// carrying one omission, or fails them when failWith is set.
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
		Omissions: []core.Omission{{
			Kind:     core.OmissionUnsupported,
			Subject:  "hero.3.health",
			Required: true,
			Reason:   "runtime cannot apply, observe, verify, and reset this field",
		}},
	})
}

// fakeAllocator allocates one in-memory ready process per revision.
type fakeAllocator struct{}

// Allocate starts one simulated server process.
func (f *fakeAllocator) Allocate(ctx context.Context, rev *core.ScenarioRevision) (*orchestrator.AllocatedProcess, error) {
	proc := &orchestrator.AllocatedProcess{
		Generation:     1,
		ConnectAddress: "10.0.0.1:27015",
	}
	proc.MarkReady(fmt.Sprintf("process ready for %s", rev.ID))
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

// fakeServerAuthority refuses every server credential: the web console tests
// do not exercise server-participant flows.
type fakeServerAuthority struct{}

// AuthenticateServer refuses with an authentication error.
func (f *fakeServerAuthority) AuthenticateServer(ctx context.Context, credential string) (*orchestrator.ServerParticipant, error) {
	return nil, orchestrator.ErrUnauthenticated
}

var (
	ownerAccount = &core.Account{ID: "acct-a", SteamID: 76561198000000001, DisplayName: "Owner"}
	otherAcct    = &core.Account{ID: "acct-b", SteamID: 76561198000000002, DisplayName: "Other"}
)

// newTestWeb wires one console over one orchestrator with test doubles. It
// returns the server and the orchestrator so server-participant steps (join
// intent consumption, result acceptance) can be driven as in production.
func newTestWeb(t *testing.T) (*httptest.Server, *orchestrator.Orchestrator, *Web) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)

	sources := []core.ReplaySource{{
		ID:          "replay-1",
		DisplayName: "Mid Boss Fight",
		FileName:    "mid-boss.dem",
	}}
	orch, err := orchestrator.NewOrchestrator(ctx, sources, &fakePreparer{}, &fakeAllocator{}, &fakeIdentityAuthority{
		accounts: map[string]*core.Account{
			"cred-a": ownerAccount,
			"cred-b": otherAcct,
		},
	}, &fakeServerAuthority{}, testGrantAuthority())
	console, err := NewWeb(orch)
	if err != nil {
		t.Fatal(err.Error())
	}
	server := httptest.NewServer(console.Handler())
	t.Cleanup(server.Close)
	testWebs[server.URL] = console
	t.Cleanup(func() { delete(testWebs, server.URL) })
	return server, orch, console
}

// testSessions maps server URL to credential-to-session-cookie so tests can
// keep using credential names while the console only issues opaque cookies.
var testSessions = map[string]map[string]*http.Cookie{}

// testWebs maps server URL to the console under test, for CSRF token access.
var testWebs = map[string]*Web{}

// testSession returns the recorded session cookie for one credential on one
// server, or nil.
func testSession(server *httptest.Server, credential string) *http.Cookie {
	return testSessions[server.URL][credential]
}

// recordSession stores the session cookie for one credential on one server.
func recordSession(server *httptest.Server, credential string, cookie *http.Cookie) {
	if testSessions[server.URL] == nil {
		testSessions[server.URL] = map[string]*http.Cookie{}
	}
	testSessions[server.URL][credential] = cookie
}

// login posts the credential and records the returned opaque session cookie.
func login(t *testing.T, server *httptest.Server, credential string) *http.Cookie {
	t.Helper()
	form := url.Values{"credential": {credential}}
	resp, err := noRedirectClient().PostForm(server.URL+"/login", form)
	if err != nil {
		t.Fatal(err.Error())
	}
	defer resp.Body.Close()
	for _, cookie := range resp.Cookies() {
		if cookie.Name == sessionCookie {
			recordSession(server, credential, cookie)
			return cookie
		}
	}
	t.Fatalf("no session cookie after login with %q", credential)
	return nil
}

// sessionCookieFor returns the opaque session cookie recorded for the
// credential; tests must have logged in first.
func sessionCookieFor(t *testing.T, server *httptest.Server, credential string) *http.Cookie {
	t.Helper()
	if credential == "" {
		return nil
	}
	cookie := testSession(server, credential)
	if cookie == nil {
		t.Fatalf("no recorded session for %q; call login first", credential)
	}
	return cookie
}

// get performs one authenticated GET and returns the status and body.
func get(t *testing.T, server *httptest.Server, path, credential string) (int, string) {
	t.Helper()
	req, err := http.NewRequest(http.MethodGet, server.URL+path, nil)
	if err != nil {
		t.Fatal(err.Error())
	}
	if cookie := sessionCookieFor(t, server, credential); cookie != nil {
		req.AddCookie(cookie)
	}
	resp, err := server.Client().Do(req)
	if err != nil {
		t.Fatal(err.Error())
	}
	defer resp.Body.Close()
	bodyBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err.Error())
	}
	return resp.StatusCode, string(bodyBytes)
}

// postForm performs one authenticated form post following redirects is off;
// it returns the redirect response. The per-session CSRF token is injected
// automatically, mirroring the rendered forms.
func postForm(t *testing.T, server *httptest.Server, path, credential string, form url.Values) *http.Response {
	t.Helper()
	cookie := sessionCookieFor(t, server, credential)
	if cookie != nil && form == nil {
		form = url.Values{}
	}
	if cookie != nil && form.Get("csrf_token") == "" {
		if console := testWebs[server.URL]; console != nil {
			form.Set("csrf_token", console.csrfTokenFor(cookie.Value))
		}
	}
	req, err := http.NewRequest(http.MethodPost, server.URL+path, strings.NewReader(form.Encode()))
	if err != nil {
		t.Fatal(err.Error())
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	if cookie != nil {
		req.AddCookie(cookie)
	}
	resp, err := noRedirectClient().Do(req)
	if err != nil {
		t.Fatal(err.Error())
	}
	t.Cleanup(func() { resp.Body.Close() })
	return resp
}

// noRedirectClient returns a client that never follows redirects, so tests
// can assert on the redirect response itself.
func noRedirectClient() *http.Client {
	return &http.Client{
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
}

// waitReady polls the preparation page until the process is ready.
func waitReady(t *testing.T, server *httptest.Server, credential, prepID string) string {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		code, body := get(t, server, "/preparations/"+prepID, credential)
		if code != http.StatusOK {
			t.Fatalf("expected 200 for preparation page, got %d", code)
		}
		if strings.Contains(body, "ready") && strings.Contains(body, "10.0.0.1:27015") {
			return body
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("timed out waiting for process readiness")
	return ""
}

// TestLoginFlow covers the session cookie lifecycle: refused credential
// never sets a cookie, valid credential sets one, logout clears it.
func TestLoginFlow(t *testing.T) {
	server, _, _ := newTestWeb(t)

	// No session: the home page is the login form with 401.
	code, body := get(t, server, "/", "")
	if code != http.StatusUnauthorized {
		t.Fatalf("expected 401 without session, got %d", code)
	}
	if !strings.Contains(body, "Sign in") {
		t.Fatal("expected the login form")
	}

	// Refused credential: still 401, no cookie.
	resp := postForm(t, server, "/login", "", url.Values{"credential": {"cred-attacker"}})
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("expected 401 for refused credential, got %d", resp.StatusCode)
	}
	for _, cookie := range resp.Cookies() {
		if cookie.Name == sessionCookie {
			t.Fatal("refused credential must not set a session cookie")
		}
	}

	// Valid credential: redirect with the session cookie.
	resp = postForm(t, server, "/login", "", url.Values{"credential": {"cred-a"}})
	if resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("expected 303 after login, got %d", resp.StatusCode)
	}
	found := false
	for _, cookie := range resp.Cookies() {
		if cookie.Name == sessionCookie && cookie.Value != "" && cookie.Value != "cred-a" {
			found = true
		}
	}
	if !found {
		t.Fatal("expected an opaque session cookie after login")
	}

	// Logout invalidates the session server-side and clears the cookie.
	cookie := login(t, server, "cred-a")
	code, home := getWithCookie(t, server, "/", cookie.Value)
	if code != http.StatusOK {
		t.Fatalf("expected 200 on home with session, got %d", code)
	}
	token := extractCSRFToken(t, home)
	resp2 := postFormWithCookie(t, server, "/logout", cookie, url.Values{"csrf_token": {token}})
	for _, c := range resp2.Cookies() {
		if c.Name == sessionCookie && c.MaxAge < 0 {
			return
		}
	}
	t.Fatal("expected a cleared session cookie after logout")
}

// TestReplaySelectionPage covers the private catalog and timecode form.
func TestReplaySelectionPage(t *testing.T) {
	server, _, _ := newTestWeb(t)
	login(t, server, "cred-a")

	code, body := get(t, server, "/", "cred-a")
	if code != http.StatusOK {
		t.Fatalf("expected 200, got %d", code)
	}
	if !strings.Contains(body, "Mid Boss Fight") || !strings.Contains(body, "mid-boss.dem") {
		t.Fatal("expected the replay catalog on the home page")
	}
	if !strings.Contains(body, `name="takeover_tick"`) {
		t.Fatal("expected the takeover tick timecode input")
	}
}

// TestPreparationHonestPreview covers the honest Reconstruction Preview
// display: the omission list is shown with subjects and reasons, and the
// fidelity badge is Preview, never Complete.
func TestPreparationHonestPreview(t *testing.T) {
	server, _, _ := newTestWeb(t)
	login(t, server, "cred-a")

	// Prepare via the form.
	resp := postForm(t, server, "/preparations", "cred-a", url.Values{
		"replay_id":     {"replay-1"},
		"takeover_tick": {"63280"},
	})
	if resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("expected 303 after prepare, got %d", resp.StatusCode)
	}
	prepID := strings.TrimPrefix(resp.Header.Get("Location"), "/preparations/")
	if prepID == "" {
		t.Fatal("expected a preparation redirect")
	}

	body := waitReady(t, server, "cred-a", prepID)

	// Honest lead-in: the takeover tick and the lead-in start tick are
	// shown separately, and the lead-in start is strictly before the
	// takeover tick, never a zero-length window.
	if !strings.Contains(body, "Lead-in start") {
		t.Fatal("expected the lead-in start tick to be shown")
	}
	if !strings.Contains(body, "63280") {
		t.Fatal("expected the selected takeover tick")
	}
	if strings.Contains(body, "Lead-in start: 63280") {
		t.Fatal("lead-in start must differ from the takeover tick")
	}

	// Honest preview: the badge and the typed omission are both shown.
	if !strings.Contains(body, "Reconstruction Preview") {
		t.Fatal("expected the Reconstruction Preview badge on a preview revision")
	}
	if strings.Contains(body, "Complete") {
		t.Fatal("a preview revision must never be labeled Complete")
	}
	if !strings.Contains(body, "hero.3.health") {
		t.Fatal("expected the omitted subject to be listed")
	}
	if !strings.Contains(body, "not reconstructed") {
		t.Fatal("expected the honest omission explanation")
	}
	if !strings.Contains(body, "63280") {
		t.Fatal("expected the selected takeover tick")
	}
}

// TestLobbyClaimRelease covers slot claim, the slot readback, and release.
func TestLobbyClaimRelease(t *testing.T) {
	server, _, _ := newTestWeb(t)
	login(t, server, "cred-a")

	resp := postForm(t, server, "/preparations", "cred-a", url.Values{
		"replay_id": {"replay-1"}, "takeover_tick": {"63280"},
	})
	prepID := strings.TrimPrefix(resp.Header.Get("Location"), "/preparations/")
	waitReady(t, server, "cred-a", prepID)

	// Claim: the page shows the slot and occupancy.
	resp = postForm(t, server, "/preparations/"+prepID+"/slots", "cred-a", nil)
	if resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("expected 303 after claim, got %d", resp.StatusCode)
	}
	code, body := get(t, server, "/preparations/"+prepID, "cred-a")
	if code != http.StatusOK {
		t.Fatalf("expected 200, got %d", code)
	}
	if !strings.Contains(body, "1 / 12") {
		t.Fatalf("expected occupancy 1/12, got: %s", body)
	}
	if !strings.Contains(body, "your slot: 0") {
		t.Fatal("expected the principal slot readback")
	}

	// Release: the slot is freed and the claim button returns.
	resp = postForm(t, server, "/preparations/"+prepID+"/slots/release", "cred-a", nil)
	if resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("expected 303 after release, got %d", resp.StatusCode)
	}
	code, body = get(t, server, "/preparations/"+prepID, "cred-a")
	if !strings.Contains(body, "0 / 12") {
		t.Fatalf("expected occupancy 0/12 after release")
	}
	if strings.Contains(body, "your slot") {
		t.Fatal("expected no slot readback after release")
	}
}

// TestDebriefSideBySide covers the private debrief page: side-by-side
// panels with explicit source labels, and exactly one next action.
func TestDebriefSideBySide(t *testing.T) {
	server, orch, _ := newTestWeb(t)
	login(t, server, "cred-a")

	resp := postForm(t, server, "/preparations", "cred-a", url.Values{
		"replay_id": {"replay-1"}, "takeover_tick": {"63280"},
	})
	prepID := strings.TrimPrefix(resp.Header.Get("Location"), "/preparations/")
	waitReady(t, server, "cred-a", prepID)

	// Claim and issue the join intent so the server participant can accept
	// the result.
	resp = postForm(t, server, "/preparations/"+prepID+"/slots", "cred-a", nil)
	if resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("expected 303 for claim, got %d", resp.StatusCode)
	}

	// The server participant consumes the join intent (admitting the
	// account on generation 1), then accepts the result, as in production.
	// The participant is bound to process generation 1, the fake allocator's
	// generation.
	participant := &orchestrator.ServerParticipant{ID: "srv-a", ProcessGeneration: 1}
	target, err := orch.IssueJoinIntent(ownerAccount, prepID)
	if err != nil {
		t.Fatal(err.Error())
	}
	if err := orch.ConsumeJoinIntent(participant, target.Intent.ID, target.Intent.RevisionID, ownerAccount.SteamID); err != nil {
		t.Fatal(err.Error())
	}
	result := &core.AttemptResult{
		ID:                "res-1",
		AccountID:         ownerAccount.ID,
		RevisionID:        target.Intent.RevisionID,
		ProcessGeneration: 1,
		AttemptGeneration: 7,
		ReplayID:          "replay-1",
		TakeoverTick:      63280,
	}
	if err := orch.AcceptResult(participant, result); err != nil {
		t.Fatal(err.Error())
	}

	code, body := get(t, server, "/debrief?process_generation=1&attempt_generation=7", "cred-a")
	if code != http.StatusOK {
		t.Fatalf("expected 200 for debrief, got %d", code)
	}

	// Side-by-side with explicit source labels.
	if !strings.Contains(body, "Source: replay") || !strings.Contains(body, "Source: server attempt") {
		t.Fatal("expected both source labels on the debrief")
	}
	if !strings.Contains(body, "replay-1") || !strings.Contains(body, "63280") {
		t.Fatal("expected the replay moment facts")
	}
	if !strings.Contains(body, "res-1") {
		t.Fatal("expected the attempt facts")
	}

	// Exactly one next action.
	if !strings.Contains(body, "Next action:") {
		t.Fatal("expected the next action block")
	}
	if got := strings.Count(body, "Select next replay"); got != 1 {
		t.Fatalf("expected exactly one next action button, got %d", got)
	}
}

// TestDebriefPrivacy covers the private debrief boundary on the web surface.
func TestDebriefPrivacy(t *testing.T) {
	server, orch, _ := newTestWeb(t)
	login(t, server, "cred-a")
	login(t, server, "cred-b")

	// The owner prepares so the join intent can be issued and consumed,
	// admitting the owner account on generation 1; then the participant
	// accepts the result, as in production.
	resp := postForm(t, server, "/preparations", "cred-a", url.Values{
		"replay_id": {"replay-1"}, "takeover_tick": {"63280"},
	})
	prepID := strings.TrimPrefix(resp.Header.Get("Location"), "/preparations/")
	waitReady(t, server, "cred-a", prepID)
	postForm(t, server, "/preparations/"+prepID+"/slots", "cred-a", nil)

	participant := &orchestrator.ServerParticipant{ID: "srv-a", ProcessGeneration: 1}
	target, err := orch.IssueJoinIntent(ownerAccount, prepID)
	if err != nil {
		t.Fatal(err.Error())
	}
	if err := orch.ConsumeJoinIntent(participant, target.Intent.ID, target.Intent.RevisionID, ownerAccount.SteamID); err != nil {
		t.Fatal(err.Error())
	}
	if err := orch.AcceptResult(participant, &core.AttemptResult{
		ID:                "res-1",
		AccountID:         ownerAccount.ID,
		RevisionID:        target.Intent.RevisionID,
		ProcessGeneration: 1,
		AttemptGeneration: 7,
		ReplayID:          "replay-1",
		TakeoverTick:      63280,
	}); err != nil {
		t.Fatal(err.Error())
	}

	// Another principal never sees the result.
	code, body := get(t, server, "/debrief?process_generation=1&attempt_generation=7", "cred-b")
	if code != http.StatusNotFound {
		t.Fatalf("expected 404 for cross-account debrief, got %d", code)
	}
	if strings.Contains(body, "res-1") {
		t.Fatal("the result must never leak to another principal")
	}
}

// TestPreparationAuthorization covers the web authorization boundary.
func TestPreparationAuthorization(t *testing.T) {
	server, _, _ := newTestWeb(t)
	login(t, server, "cred-a")
	login(t, server, "cred-b")

	resp := postForm(t, server, "/preparations", "cred-a", url.Values{
		"replay_id": {"replay-1"}, "takeover_tick": {"63280"},
	})
	prepID := strings.TrimPrefix(resp.Header.Get("Location"), "/preparations/")

	// A principal with no relationship sees a typed forbidden page.
	code, body := get(t, server, "/preparations/"+prepID, "cred-b")
	if code != http.StatusForbidden {
		t.Fatalf("expected 403, got %d", code)
	}
	if !strings.Contains(body, "do not have access") {
		t.Fatal("expected the friendly forbidden message")
	}
	if strings.Contains(body, "10.0.0.1:27015") {
		t.Fatal("connect address must never leak to a forbidden principal")
	}
}

// TestPrepareFormValidation covers the typed form errors.
func TestPrepareFormValidation(t *testing.T) {
	server, _, _ := newTestWeb(t)
	login(t, server, "cred-a")

	// Invalid tick.
	resp := postForm(t, server, "/preparations", "cred-a", url.Values{
		"replay_id": {"replay-1"}, "takeover_tick": {"not-a-tick"},
	})
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("expected 400 for invalid tick, got %d", resp.StatusCode)
	}

	// Unknown replay.
	resp = postForm(t, server, "/preparations", "cred-a", url.Values{
		"replay_id": {"replay-none"}, "takeover_tick": {"63280"},
	})
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("expected 404 for unknown replay, got %d", resp.StatusCode)
	}
}

// TestSessionCookieOpaqueAndSecure covers blockers 1 and 2: the cookie is an
// opaque random ID (never the raw credential), and the value is
// cryptographically random rather than derivable from the credential.
func TestSessionCookieOpaqueAndSecure(t *testing.T) {
	server, _, _ := newTestWeb(t)

	resp := postForm(t, server, "/login", "", url.Values{"credential": {"cred-a"}})
	defer resp.Body.Close()
	cookie := respCookie(t, resp, sessionCookie)
	if cookie.Value == "cred-a" || strings.Contains(cookie.Value, "cred") {
		t.Fatalf("cookie value must be opaque, got %q", cookie.Value)
	}
	if len(cookie.Value) < 32 {
		t.Fatalf("opaque session ID too short: %q", cookie.Value)
	}
	if !cookie.HttpOnly {
		t.Fatal("HttpOnly must be set on the session cookie")
	}
	if cookie.SameSite != http.SameSiteLaxMode {
		t.Fatal("SameSite=Lax expected on the session cookie")
	}
}

// respCookie fetches one named cookie from a response.
func respCookie(t *testing.T, resp *http.Response, name string) *http.Cookie {
	t.Helper()
	for _, c := range resp.Cookies() {
		if c.Name == name {
			return c
		}
	}
	t.Fatalf("cookie %q not found in response", name)
	return nil
}

// getWithCookie performs one GET with a session cookie value.
func getWithCookie(t *testing.T, server *httptest.Server, path, cookieValue string) (int, string) {
	t.Helper()
	req, err := http.NewRequest(http.MethodGet, server.URL+path, nil)
	if err != nil {
		t.Fatal(err.Error())
	}
	req.AddCookie(&http.Cookie{Name: sessionCookie, Value: cookieValue})
	resp, err := server.Client().Do(req)
	if err != nil {
		t.Fatal(err.Error())
	}
	defer resp.Body.Close()
	bodyBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err.Error())
	}
	return resp.StatusCode, string(bodyBytes)
}

// postFormWithCookie performs one POST with a session cookie; redirects are
// not followed so tests can assert on the redirect response itself.
func postFormWithCookie(t *testing.T, server *httptest.Server, path string, cookie *http.Cookie, form url.Values) *http.Response {
	t.Helper()
	req, err := http.NewRequest(http.MethodPost, server.URL+path, strings.NewReader(form.Encode()))
	if err != nil {
		t.Fatal(err.Error())
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.AddCookie(cookie)
	resp, err := noRedirectClient().Do(req)
	if err != nil {
		t.Fatal(err.Error())
	}
	t.Cleanup(func() { resp.Body.Close() })
	return resp
}

// extractCSRFToken pulls the CSRF token out of a rendered page.
func extractCSRFToken(t *testing.T, body string) string {
	t.Helper()
	marker := `name="csrf_token" value="`
	idx := strings.Index(body, marker)
	if idx == -1 {
		t.Fatal("expected a CSRF token in the page")
	}
	rest := body[idx+len(marker):]
	end := strings.Index(rest, `"`)
	if end == -1 {
		t.Fatal("malformed CSRF token in page")
	}
	return rest[:end]
}

// TestInviteFlow covers the web invite surface: only the owner sees the
// invite form, an invited account claims with the token, a stranger
// without a token is refused, and the token is single-use.
func TestInviteFlow(t *testing.T) {
	server, _, _ := newTestWeb(t)
	login(t, server, "cred-a")
	login(t, server, "cred-b")

	// Owner prepares.
	resp := postForm(t, server, "/preparations", "cred-a", url.Values{
		"replay_id": {"replay-1"}, "takeover_tick": {"63280"},
	})
	prepID := strings.TrimPrefix(resp.Header.Get("Location"), "/preparations/")
	waitReady(t, server, "cred-a", prepID)

	// Owner sees the invite form.
	code, body := get(t, server, "/preparations/"+prepID, "cred-a")
	if code != http.StatusOK || !strings.Contains(body, "Invite a teammate") {
		t.Fatalf("expected the invite form for the owner, got %d", code)
	}

	// A non-owner does not see the invite form and cannot claim without a
	// token.
	code, body = get(t, server, "/preparations/"+prepID, "cred-b")
	if code != http.StatusForbidden {
		t.Fatalf("expected 403 for non-owner view, got %d", code)
	}
	if strings.Contains(body, "Invite a teammate") {
		t.Fatal("invite form must not be visible to a non-owner")
	}

	// Owner issues an invite for cred-b via the form.
	resp = postForm(t, server, "/preparations/"+prepID+"/invite", "cred-a", url.Values{
		"account_id": {otherAcct.ID},
	})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 after invite, got %d", resp.StatusCode)
	}
	invitedBody, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err.Error())
	}
	token := extractInviteToken(t, string(invitedBody))
	if token == "" {
		t.Fatal("expected an invite token in the response")
	}

	// The invited account claims with the token.
	resp = postForm(t, server, "/preparations/"+prepID+"/slots", "cred-b", url.Values{
		"invite_token": {token},
	})
	if resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("expected 303 for invited claim, got %d", resp.StatusCode)
	}

	// The invited participant now views the preparation.
	code, body = get(t, server, "/preparations/"+prepID, "cred-b")
	if code != http.StatusOK {
		t.Fatalf("expected 200 for invited participant view, got %d", code)
	}

	// The token is single-use: cred-b releases, then a fresh claim with the
	// same token is refused.
	resp = postForm(t, server, "/preparations/"+prepID+"/slots/release", "cred-b", nil)
	if resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("expected 303 for release, got %d", resp.StatusCode)
	}
	resp = postForm(t, server, "/preparations/"+prepID+"/slots", "cred-b", url.Values{
		"invite_token": {token},
	})
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("expected 403 for replayed invite token, got %d", resp.StatusCode)
	}
}

// extractInviteToken pulls the invite token out of the preparation page
// after an invite is issued.
func extractInviteToken(t *testing.T, body string) string {
	t.Helper()
	start := strings.Index(body, "<code>")
	end := strings.Index(body, "</code>")
	if start == -1 || end == -1 || end <= start {
		return ""
	}
	return body[start+len("<code>") : end]
}

// testGrantAuthority returns a fresh in-memory grant authority for tests.
func testGrantAuthority() core.GrantAuthority {
	a, err := core.NewHMACGrantAuthority(core.GrantAuthorityConfig{
		Clock: time.Now,
		TTL:   time.Hour,
	})
	if err != nil {
		panic(err.Error())
	}
	return a
}
