package web

import (
	"errors"
	"net/http"
	"strconv"

	"github.com/paralin/fraglands/core"
	"github.com/paralin/fraglands/orchestrator"
)

// handleHome serves the replay selection page: the private catalog of
// replay sources with a timecode form for each.
func (w *Web) handleHome(rw http.ResponseWriter, r *http.Request, p *principal) {
	w.render(rw, http.StatusOK, "index.html", map[string]any{
		"Account":     p.account.DisplayName,
		"Replays":     w.orch.Sources(),
		"CSRFToken":   w.csrfTokenFor(p.sessionID),
		"OmissionsOK": true,
	})
}

// handlePrepare accepts the replay + timecode selection form. The user
// selects only the takeover tick (the moment input unlocks); the lead-in
// window is derived by the reconstruction provider from the replay's own
// proven tick interval.
func (w *Web) handlePrepare(rw http.ResponseWriter, r *http.Request, p *principal) {
	if err := r.ParseForm(); err != nil {
		w.renderError(rw, http.StatusBadRequest, "The form could not be read.")
		return
	}
	replayID := r.PostFormValue("replay_id")
	if replayID == "" {
		w.renderError(rw, http.StatusBadRequest, "No replay was selected.")
		return
	}
	tick, err := strconv.ParseUint(r.PostFormValue("takeover_tick"), 10, 32)
	if err != nil {
		w.renderError(rw, http.StatusBadRequest, "The takeover tick must be a non-negative integer.")
		return
	}

	id, err := w.orch.Prepare(p.account, replayID, uint32(tick), uint32(tick))
	if err != nil {
		w.renderOrchError(rw, err)
		return
	}
	http.Redirect(rw, r, "/preparations/"+id, http.StatusSeeOther)
}

// handlePreparation serves the preparation status page: lifecycle state,
// readiness evidence, lobby occupancy, the principal's own slot, and the
// honest omission list for a Reconstruction Preview revision.
func (w *Web) handlePreparation(rw http.ResponseWriter, r *http.Request, p *principal) {
	status, err := w.orch.Preparation(p.account, r.PathValue("id"))
	if err != nil {
		w.renderOrchError(rw, err)
		return
	}

	data := map[string]any{
		"Account":   p.account.DisplayName,
		"ID":        status.Preparation.ID,
		"Replay":    status.Preparation.ReplayID,
		"State":     status.Preparation.State().String(),
		"Tick":      status.Preparation.TakeoverTick,
		"CSRFToken": w.csrfTokenFor(p.sessionID),
	}

	if rev := status.Preparation.Revision(); rev != nil {
		data["RevisionID"] = rev.ID
		data["Fidelity"] = rev.Fidelity.String()
		data["Omissions"] = rev.Omissions
		data["LeadInStartTick"] = rev.LeadInStartTick
	}
	if failure := status.Preparation.Failure(); failure != nil {
		data["Failure"] = failure
	}
	if status.Lobby != nil {
		lobby := map[string]any{
			"Capacity": status.Lobby.Capacity,
			"Occupied": status.Lobby.Occupied(),
		}
		if slot, ok := status.Lobby.Slot(p.account.ID); ok {
			lobby["MySlot"] = slot
			lobby["HasSlot"] = true
		}
		data["Lobby"] = lobby
	}
	if status.Process != nil {
		data["Process"] = map[string]any{
			"Generation": status.Process.Generation,
			"Address":    status.Process.ConnectAddress,
			"Ready":      status.Process.Ready(),
			"Evidence":   status.Process.Evidence(),
		}
	}
	if status.AllocationFailure != nil {
		data["AllocFailure"] = status.AllocationFailure.Reason
	}
	// Only the preparation owner sees the invite form.
	data["CanInvite"] = w.orch.IsOwner(p.account, r.PathValue("id"))

	w.render(rw, http.StatusOK, "preparation.html", data)
}

// handleInvite issues an opaque single-use invitation for one account to
// claim into this preparation's lobby. Only the owner may invite; the token
// is shown once to the owner to pass to the invitee out of band.
func (w *Web) handleInvite(rw http.ResponseWriter, r *http.Request, p *principal) {
	if err := r.ParseForm(); err != nil {
		w.renderError(rw, http.StatusBadRequest, "The form could not be read.")
		return
	}
	accountID := r.PostFormValue("account_id")
	if accountID == "" {
		w.renderError(rw, http.StatusBadRequest, "Enter the account to invite.")
		return
	}
	invite, err := w.orch.Invite(p.account, r.PathValue("id"), accountID)
	if err != nil {
		w.renderOrchError(rw, err)
		return
	}

	// Re-render the preparation page with the token shown once.
	status, err := w.orch.Preparation(p.account, r.PathValue("id"))
	if err != nil {
		w.renderOrchError(rw, err)
		return
	}
	data := map[string]any{
		"Account":     p.account.DisplayName,
		"ID":          status.Preparation.ID,
		"Replay":      status.Preparation.ReplayID,
		"State":       status.Preparation.State().String(),
		"Tick":        status.Preparation.TakeoverTick,
		"CSRFToken":   w.csrfTokenFor(p.sessionID),
		"CanInvite":   true,
		"InviteToken": invite.Token,
		"InviteFor":   accountID,
	}
	if rev := status.Preparation.Revision(); rev != nil {
		data["RevisionID"] = rev.ID
		data["Fidelity"] = rev.Fidelity.String()
		data["Omissions"] = rev.Omissions
		data["LeadInStartTick"] = rev.LeadInStartTick
	}
	if failure := status.Preparation.Failure(); failure != nil {
		data["Failure"] = failure
	}
	if status.Lobby != nil {
		lobby := map[string]any{
			"Capacity": status.Lobby.Capacity,
			"Occupied": status.Lobby.Occupied(),
		}
		if slot, ok := status.Lobby.Slot(p.account.ID); ok {
			lobby["MySlot"] = slot
			lobby["HasSlot"] = true
		}
		data["Lobby"] = lobby
	}
	if status.Process != nil {
		data["Process"] = map[string]any{
			"Generation": status.Process.Generation,
			"Address":    status.Process.ConnectAddress,
			"Ready":      status.Process.Ready(),
			"Evidence":   status.Process.Evidence(),
		}
	}
	if status.AllocationFailure != nil {
		data["AllocFailure"] = status.AllocationFailure.Reason
	}
	w.render(rw, http.StatusOK, "preparation.html", data)
}

// handleClaim reserves the principal's slot in the preparation lobby. A
// presented invite token authorizes a non-owner to claim; the token is
// single-use and bound to the principal's account.
func (w *Web) handleClaim(rw http.ResponseWriter, r *http.Request, p *principal) {
	_ = r.ParseForm()
	_, err := w.orch.ClaimAuthorized(p.account, r.PathValue("id"), r.PostFormValue("invite_token"))
	if err != nil {
		w.renderOrchError(rw, err)
		return
	}
	http.Redirect(rw, r, "/preparations/"+r.PathValue("id"), http.StatusSeeOther)
}

// handleRelease frees the principal's slot in the preparation lobby.
func (w *Web) handleRelease(rw http.ResponseWriter, r *http.Request, p *principal) {
	if err := w.orch.Release(p.account, r.PathValue("id")); err != nil {
		w.renderOrchError(rw, err)
		return
	}
	http.Redirect(rw, r, "/preparations/"+r.PathValue("id"), http.StatusSeeOther)
}

// handleDebrief serves the private debrief page: the result is looked up for
// the authenticated principal only, and is shown side-by-side with explicit
// source labels (the replay moment versus the server attempt) and exactly
// one next action.
func (w *Web) handleDebrief(rw http.ResponseWriter, r *http.Request, p *principal) {
	procGen, err := strconv.ParseUint(r.URL.Query().Get("process_generation"), 10, 64)
	if err != nil {
		w.renderError(rw, http.StatusBadRequest, "The process generation is invalid.")
		return
	}
	attemptGen, err := strconv.ParseUint(r.URL.Query().Get("attempt_generation"), 10, 64)
	if err != nil {
		w.renderError(rw, http.StatusBadRequest, "The attempt generation is invalid.")
		return
	}

	result, err := w.orch.Result(p.account, procGen, attemptGen)
	if err != nil {
		w.renderOrchError(rw, err)
		return
	}

	w.render(rw, http.StatusOK, "debrief.html", map[string]any{
		"Account": p.account.DisplayName,
		"Result":  result,
	})
}

// renderOrchError maps one orchestrator or core error to a friendly page
// without leaking internal details.
func (w *Web) renderOrchError(rw http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, orchestrator.ErrForbidden):
		w.renderError(rw, http.StatusForbidden, "You do not have access to this preparation.")
	case errors.Is(err, orchestrator.ErrUnknownPreparation):
		w.renderError(rw, http.StatusNotFound, "Preparation not found.")
	case errors.Is(err, orchestrator.ErrUnknownReplay):
		w.renderError(rw, http.StatusNotFound, "Replay not found in the selection catalog.")
	case errors.Is(err, core.ErrNoResult):
		w.renderError(rw, http.StatusNotFound, "No result was found for this attempt.")
	case errors.Is(err, core.ErrLobbyFull):
		w.renderError(rw, http.StatusConflict, "The lobby is full.")
	case errors.Is(err, core.ErrNoSlotClaimed):
		w.renderError(rw, http.StatusConflict, "You do not hold a slot in this lobby.")
	default:
		w.renderError(rw, http.StatusInternalServerError, "Something went wrong.")
	}
}
