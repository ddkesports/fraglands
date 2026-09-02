// Package api serves the minimal Fraglands path over stdlib HTTP: private
// replay selection, preparation status, lobby claims, one-use join intents,
// and private debrief retrieval. It is a transport adapter: every rule lives
// in the orchestrator and core packages.
package api

import (
	"net/http"

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

// Handler returns the HTTP routes for the API.
func (a *API) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /replays", a.handleReplays)
	mux.HandleFunc("POST /preparations", a.handlePrepare)
	mux.HandleFunc("GET /preparations/{id}", a.handlePreparation)
	mux.HandleFunc("POST /preparations/{id}/slots", a.handleClaim)
	mux.HandleFunc("DELETE /preparations/{id}/slots", a.handleRelease)
	mux.HandleFunc("POST /preparations/{id}/join-intent", a.handleJoinIntent)
	mux.HandleFunc("GET /debrief", a.handleDebrief)
	return mux
}
