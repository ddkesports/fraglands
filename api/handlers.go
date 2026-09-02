package api

import (
	"net/http"

	"github.com/aperturerobotics/fastjson"
	"github.com/paralin/fraglands/core"
)

// handleReplays serves the private replay selection catalog.
func (a *API) handleReplays(w http.ResponseWriter, r *http.Request, _ *core.Account) {
	sources := a.orch.Sources()
	var arena fastjson.Arena
	defer arena.Reset()
	arr := arena.NewArray()
	for i, src := range sources {
		arr.SetArrayItem(i, encodeReplay(&arena, src))
	}
	root := arena.NewObject()
	root.Set("replays", arr)
	writeJSON(w, http.StatusOK, root)
}

// handlePrepare accepts one preparation request owned by the principal.
func (a *API) handlePrepare(w http.ResponseWriter, r *http.Request, principal *core.Account) {
	obj, err := readRequestJSON(r)
	if err != nil {
		writeTypedError(w, http.StatusBadRequest, ErrorCodeInvalidRequest, err.Error())
		return
	}

	// Parse the selection request: one replay and the moment within it.
	replayID, err := requestString(obj, "replay_id")
	if err != nil {
		writeTypedError(w, http.StatusBadRequest, ErrorCodeInvalidRequest, err.Error())
		return
	}
	leadInStartTick, err := requestUint64(obj, "lead_in_start_tick")
	if err != nil {
		writeTypedError(w, http.StatusBadRequest, ErrorCodeInvalidRequest, err.Error())
		return
	}
	takeoverTick, err := requestUint64(obj, "takeover_tick")
	if err != nil {
		writeTypedError(w, http.StatusBadRequest, ErrorCodeInvalidRequest, err.Error())
		return
	}
	if leadInStartTick > uint64(^uint32(0)) || takeoverTick > uint64(^uint32(0)) {
		writeTypedError(w, http.StatusBadRequest, ErrorCodeInvalidRequest, "ticks exceed uint32 range")
		return
	}

	id, err := a.orch.Prepare(principal, replayID, uint32(leadInStartTick), uint32(takeoverTick))
	if err != nil {
		writeError(w, err)
		return
	}

	var arena fastjson.Arena
	defer arena.Reset()
	root := arena.NewObject()
	root.Set("preparation_id", arena.NewString(id))
	writeJSON(w, http.StatusCreated, root)
}

// handlePreparation serves the explicit preparation status readback to the
// preparation owner and claimed participants only.
func (a *API) handlePreparation(w http.ResponseWriter, r *http.Request, principal *core.Account) {
	status, err := a.orch.Preparation(principal, r.PathValue("id"))
	if err != nil {
		writeError(w, err)
		return
	}
	var arena fastjson.Arena
	defer arena.Reset()
	writeJSON(w, http.StatusOK, encodePreparation(&arena, status, principal))
}

// handleClaim reserves one lobby slot for the principal.
func (a *API) handleClaim(w http.ResponseWriter, r *http.Request, principal *core.Account) {
	if _, err := readRequestJSON(r); err != nil {
		writeTypedError(w, http.StatusBadRequest, ErrorCodeInvalidRequest, err.Error())
		return
	}

	slot, err := a.orch.Claim(principal, r.PathValue("id"))
	if err != nil {
		writeError(w, err)
		return
	}

	var arena fastjson.Arena
	defer arena.Reset()
	root := arena.NewObject()
	root.Set("slot", arena.NewNumberInt(slot))
	writeJSON(w, http.StatusOK, root)
}

// handleRelease frees the principal lobby slot.
func (a *API) handleRelease(w http.ResponseWriter, r *http.Request, principal *core.Account) {
	if _, err := readRequestJSON(r); err != nil {
		writeTypedError(w, http.StatusBadRequest, ErrorCodeInvalidRequest, err.Error())
		return
	}

	if err := a.orch.Release(principal, r.PathValue("id")); err != nil {
		writeError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// handleJoinIntent issues one one-use join intent for the principal,
// bound to the principal immutable Steam identity.
func (a *API) handleJoinIntent(w http.ResponseWriter, r *http.Request, principal *core.Account) {
	if _, err := readRequestJSON(r); err != nil {
		writeTypedError(w, http.StatusBadRequest, ErrorCodeInvalidRequest, err.Error())
		return
	}

	target, err := a.orch.IssueJoinIntent(principal, r.PathValue("id"))
	if err != nil {
		writeError(w, err)
		return
	}

	var arena fastjson.Arena
	defer arena.Reset()
	writeJSON(w, http.StatusCreated, encodeJoinIntent(&arena, target))
}

// handleDebrief serves one private debrief result to the principal the
// result belongs to. The account is the authenticated principal, never a
// query parameter.
func (a *API) handleDebrief(w http.ResponseWriter, r *http.Request, principal *core.Account) {
	q := r.URL.Query()
	processGeneration, err := pathUint64(q.Get("process_generation"))
	if err != nil {
		writeTypedError(w, http.StatusBadRequest, ErrorCodeInvalidRequest, "invalid process_generation")
		return
	}
	attemptGeneration, err := pathUint64(q.Get("attempt_generation"))
	if err != nil {
		writeTypedError(w, http.StatusBadRequest, ErrorCodeInvalidRequest, "invalid attempt_generation")
		return
	}

	result, err := a.orch.Result(principal, processGeneration, attemptGeneration)
	if err != nil {
		writeError(w, err)
		return
	}

	var arena fastjson.Arena
	defer arena.Reset()
	writeJSON(w, http.StatusOK, encodeResult(&arena, result))
}
