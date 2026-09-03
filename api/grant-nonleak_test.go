package api

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
)

// The API surface must never serialize a grant: no preparation readback
// carries a grant field or token, at any preparation state.
func TestAPINeverLeaksGrant(t *testing.T) {
	server, orch := testServer(t)
	owner := ownerAccount

	id, err := orch.Prepare(owner, "replay-1", 0, 63280)
	if err != nil {
		t.Fatal(err.Error())
	}

	// Readback while queued (before the preparer ran) and again after the
	// fake preparer completed it: neither state may carry grant material.
	for _, attempt := range []string{"queued", "ready"} {
		code, body := callJSON(t, server, http.MethodGet, "/preparations/"+id, "cred-a", nil)
		if code != http.StatusOK {
			t.Fatalf("expected 200, got %d: %v", code, body)
		}
		raw, err := json.Marshal(body)
		if err != nil {
			t.Fatal(err.Error())
		}
		encoded := strings.ToLower(string(raw))
		for _, forbidden := range []string{"grant", "token"} {
			if strings.Contains(encoded, forbidden) {
				t.Fatalf("%s readback must never contain %q: %s", attempt, forbidden, encoded)
			}
		}
	}
}
