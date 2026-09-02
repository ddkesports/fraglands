package api

import (
	"net/http"

	"github.com/aperturerobotics/fastjson"
	"github.com/paralin/fraglands/core"
)

// handleReplays serves the private replay selection catalog.
func (a *API) handleReplays(w http.ResponseWriter, r *http.Request) {
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

// handlePrepare accepts one preparation request.
func (a *API) handlePrepare(w http.ResponseWriter, r *http.Request) {
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

	id, err := a.orch.Prepare(replayID, uint32(leadInStartTick), uint32(takeoverTick))
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

// handlePreparation serves the explicit preparation status readback.
func (a *API) handlePreparation(w http.ResponseWriter, r *http.Request) {
	status, err := a.orch.Preparation(r.PathValue("id"))
	if err != nil {
		writeError(w, err)
		return
	}
	var arena fastjson.Arena
	defer arena.Reset()
	writeJSON(w, http.StatusOK, encodePreparation(&arena, status))
}

// handleClaim reserves one lobby slot.
func (a *API) handleClaim(w http.ResponseWriter, r *http.Request) {
	obj, err := readRequestJSON(r)
	if err != nil {
		writeTypedError(w, http.StatusBadRequest, ErrorCodeInvalidRequest, err.Error())
		return
	}
	accountID, err := requestString(obj, "account_id")
	if err != nil {
		writeTypedError(w, http.StatusBadRequest, ErrorCodeInvalidRequest, err.Error())
		return
	}

	slot, err := a.orch.Claim(r.PathValue("id"), accountID)
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

// handleRelease frees one lobby slot.
func (a *API) handleRelease(w http.ResponseWriter, r *http.Request) {
	obj, err := readRequestJSON(r)
	if err != nil {
		writeTypedError(w, http.StatusBadRequest, ErrorCodeInvalidRequest, err.Error())
		return
	}
	accountID, err := requestString(obj, "account_id")
	if err != nil {
		writeTypedError(w, http.StatusBadRequest, ErrorCodeInvalidRequest, err.Error())
		return
	}

	if err := a.orch.Release(r.PathValue("id"), accountID); err != nil {
		writeError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// handleJoinIntent issues one one-use join intent.
func (a *API) handleJoinIntent(w http.ResponseWriter, r *http.Request) {
	obj, err := readRequestJSON(r)
	if err != nil {
		writeTypedError(w, http.StatusBadRequest, ErrorCodeInvalidRequest, err.Error())
		return
	}
	accountID, err := requestString(obj, "account_id")
	if err != nil {
		writeTypedError(w, http.StatusBadRequest, ErrorCodeInvalidRequest, err.Error())
		return
	}
	steamRaw, err := requestUint64(obj, "steam_id")
	if err != nil {
		writeTypedError(w, http.StatusBadRequest, ErrorCodeInvalidRequest, err.Error())
		return
	}
	if steamRaw == 0 {
		writeTypedError(w, http.StatusBadRequest, ErrorCodeInvalidRequest, "steam_id must be nonzero")
		return
	}

	target, err := a.orch.IssueJoinIntent(r.PathValue("id"), accountID, core.SteamID(steamRaw))
	if err != nil {
		writeError(w, err)
		return
	}

	var arena fastjson.Arena
	defer arena.Reset()
	writeJSON(w, http.StatusCreated, encodeJoinIntent(&arena, target))
}

// handleDebrief serves one private debrief result.
func (a *API) handleDebrief(w http.ResponseWriter, r *http.Request) {
	// The debrief is private: the query names the owning account and attempt.
	q := r.URL.Query()
	accountID := q.Get("account_id")
	if accountID == "" {
		writeTypedError(w, http.StatusBadRequest, ErrorCodeInvalidRequest, "missing account_id")
		return
	}
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

	result, err := a.orch.Result(accountID, processGeneration, attemptGeneration)
	if err != nil {
		writeError(w, err)
		return
	}

	var arena fastjson.Arena
	defer arena.Reset()
	writeJSON(w, http.StatusOK, encodeResult(&arena, result))
}
