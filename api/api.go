// Package api serves the minimal Fraglands path over stdlib HTTP: private
// replay selection, preparation status, lobby claims, one-use join intents,
// and private debrief retrieval. It is a transport adapter: every rule lives
// in the orchestrator and core packages. Identities are derived from
// credentials by the injected IdentityAuthority, never from payloads.
package api

import (
	"net/http"

	"github.com/paralin/fraglands/core"
	"github.com/paralin/fraglands/orchestrator"
)

// API serves the minimal Fraglands path over HTTP.
type API struct {
	// orch is the orchestrator the handlers call.
	orch *orchestrator.Orchestrator
}

// NewAPI constructs the HTTP API over one orchestrator.
func NewAPI(orch *orchestrator.Orchestrator) *API {
	return &API{orch: orch}
}

// credentialHeader carries the bearer credential presented by the client.
const credentialHeader = "Authorization"

// Handler returns the HTTP routes for the API. Every route requires an
// authenticated principal: the middleware derives it from the credential
// before any handler runs.
func (a *API) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /replays", a.requirePrincipal(a.handleReplays))
	mux.HandleFunc("POST /preparations", a.requirePrincipal(a.handlePrepare))
	mux.HandleFunc("GET /preparations/{id}", a.requirePrincipal(a.handlePreparation))
	mux.HandleFunc("POST /preparations/{id}/slots", a.requirePrincipal(a.handleClaim))
	mux.HandleFunc("DELETE /preparations/{id}/slots", a.requirePrincipal(a.handleRelease))
	mux.HandleFunc("POST /preparations/{id}/join-intent", a.requirePrincipal(a.handleJoinIntent))
	mux.HandleFunc("GET /debrief", a.requirePrincipal(a.handleDebrief))
	return mux
}

// requirePrincipal authenticates the request credential and stores the
// derived principal on the request context, or refuses with a typed 401.
func (a *API) requirePrincipal(next func(http.ResponseWriter, *http.Request, *core.Account)) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		cred := bearerCredential(r.Header.Get(credentialHeader))
		if cred == "" {
			writeTypedError(w, http.StatusUnauthorized, ErrorCodeUnauthenticated, "missing credential")
			return
		}
		account, err := a.orch.Authenticate(r.Context(), cred)
		if err != nil {
			writeTypedError(w, http.StatusUnauthorized, ErrorCodeUnauthenticated, "invalid credential")
			return
		}
		next(w, r, account)
	}
}

// bearerCredential strips the Bearer prefix from the Authorization header.
func bearerCredential(raw string) string {
	const prefix = "Bearer "
	if len(raw) > len(prefix) && raw[:len(prefix)] == prefix {
		return raw[len(prefix):]
	}
	return raw
}
