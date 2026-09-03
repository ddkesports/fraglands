package web

import (
	"net/http"
	"net/url"
	"strings"
	"testing"
)

// The web console must never render a grant or its token: the preparation
// page stays free of grant material for the owner and for any other viewer.
func TestWebNeverLeaksGrant(t *testing.T) {
	server, _, _ := newTestWeb(t)

	// Log in as the owner and prepare a replay.
	login(t, server, "cred-a")
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
	waitReady(t, server, "cred-a", prepID)

	// Fetch the preparation page as the owner and as an unrelated account.
	_, bodyOwner := get(t, server, "/preparations/"+prepID, "cred-a")
	login(t, server, "cred-b")
	_, bodyOther := get(t, server, "/preparations/"+prepID, "cred-b")

	for name, body := range map[string]string{"owner": bodyOwner, "other": bodyOther} {
		encoded := strings.ToLower(body)
		for _, forbidden := range []string{"grant", "token-"} {
			if strings.Contains(encoded, forbidden) {
				t.Fatalf("%s page must never contain %q", name, forbidden)
			}
		}
	}
}
