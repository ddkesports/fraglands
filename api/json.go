package api

import (
	"fmt"
	"net/http"

	"github.com/aperturerobotics/fastjson"
	"github.com/paralin/fraglands/core"
	"github.com/paralin/fraglands/orchestrator"
)

// writeJSON writes one JSON object value as the response body.
func writeJSON(w http.ResponseWriter, status int, v *fastjson.Value) {
	body := v.MarshalTo(nil)
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_, _ = w.Write(body)
}

// writeError writes one typed JSON error response. Internal errors never
// leak their details.
func writeError(w http.ResponseWriter, err error) {
	code, status := statusForError(err)
	message := "internal error"
	if code != ErrorCodeInternal {
		message = err.Error()
	}
	writeTypedError(w, status, code, message)
}

// writeTypedError writes one typed JSON error with an explicit message.
func writeTypedError(w http.ResponseWriter, status int, code ErrorCode, message string) {
	var a fastjson.Arena
	defer a.Reset()
	root := a.NewObject()
	root.Set("error", a.NewString(string(code)))
	root.Set("message", a.NewString(message))
	writeJSON(w, status, root)
}

// encodeReplay encodes one replay source for the selection surface.
func encodeReplay(a *fastjson.Arena, src core.ReplaySource) *fastjson.Value {
	v := a.NewObject()
	v.Set("id", a.NewString(src.ID))
	v.Set("display_name", a.NewString(src.DisplayName))
	v.Set("file_name", a.NewString(src.FileName))
	return v
}

// encodePreparation encodes the explicit preparation status readback for the
// requesting principal. The principal's own slot is read back so the caller
// can show the reservation it holds without a second request.
func encodePreparation(a *fastjson.Arena, status orchestrator.PreparationStatus, principal *core.Account) *fastjson.Value {
	prep := status.Preparation
	v := a.NewObject()
	v.Set("id", a.NewString(prep.ID))
	v.Set("replay_id", a.NewString(prep.ReplayID))
	v.Set("lead_in_start_tick", a.NewString(fmt.Sprintf("%d", prep.LeadInStartTick)))
	v.Set("takeover_tick", a.NewString(fmt.Sprintf("%d", prep.TakeoverTick)))
	v.Set("state", a.NewString(prep.State().String()))

	// Lobby readback: capacity, occupancy, and the principal's own slot.
	lobby := a.NewObject()
	lobby.Set("capacity", a.NewNumberInt(status.Lobby.Capacity))
	lobby.Set("occupied", a.NewNumberInt(status.Lobby.Occupied()))
	if slot, ok := status.Lobby.Slot(principal.ID); ok {
		lobby.Set("slot", a.NewNumberInt(slot))
	} else {
		lobby.Set("slot", a.NewNull())
	}
	v.Set("lobby", lobby)

	// Process readback: generation, connect address, and readiness evidence.
	if status.Process != nil {
		proc := a.NewObject()
		proc.Set("generation", a.NewString(fmt.Sprintf("%d", status.Process.Generation)))
		proc.Set("connect_address", a.NewString(status.Process.ConnectAddress))
		if status.Process.Ready() {
			proc.Set("ready", a.NewTrue())
		} else {
			proc.Set("ready", a.NewFalse())
		}
		proc.Set("readiness_evidence", a.NewString(status.Process.Evidence()))
		v.Set("process", proc)
	} else {
		v.Set("process", a.NewNull())
	}

	// Typed allocation failure readback.
	if status.AllocationFailure != nil {
		v.Set("allocation_failure", encodeFailure(a, status.AllocationFailure.Reason))
	} else {
		v.Set("allocation_failure", a.NewNull())
	}

	// Immutable revision readback once ready.
	if revision := prep.Revision(); revision != nil {
		v.Set("revision", encodeRevision(a, revision))
	} else {
		v.Set("revision", a.NewNull())
	}

	// Typed preparation failure readback.
	if failure := prep.Failure(); failure != nil {
		v.Set("failure", encodeFailure(a, failure))
	} else {
		v.Set("failure", a.NewNull())
	}
	return v
}

// encodeRevision encodes one immutable revision reference.
func encodeRevision(a *fastjson.Arena, revision *core.ScenarioRevision) *fastjson.Value {
	v := a.NewObject()
	v.Set("id", a.NewString(revision.ID))
	v.Set("replay_id", a.NewString(revision.ReplayID))
	v.Set("lead_in_start_tick", a.NewString(fmt.Sprintf("%d", revision.LeadInStartTick)))
	v.Set("takeover_tick", a.NewString(fmt.Sprintf("%d", revision.TakeoverTick)))
	v.Set("fidelity", a.NewString(revision.Fidelity.String()))
	v.Set("omissions", encodeOmissions(a, revision.Omissions))
	return v
}

// encodeFailure encodes one typed failure reason.
func encodeFailure(a *fastjson.Arena, failure *core.FailureReason) *fastjson.Value {
	v := a.NewObject()
	v.Set("code", a.NewString(failure.Code))
	v.Set("message", a.NewString(failure.Message))
	return v
}

// encodeOmission encodes one typed omission from compilation.
func encodeOmission(a *fastjson.Arena, o core.Omission) *fastjson.Value {
	v := a.NewObject()
	v.Set("kind", a.NewString(string(o.Kind)))
	v.Set("subject", a.NewString(o.Subject))
	if o.Required {
		v.Set("required", a.NewTrue())
	} else {
		v.Set("required", a.NewFalse())
	}
	v.Set("reason", a.NewString(o.Reason))
	return v
}

// encodeOmissions encodes the typed omission list; empty for Complete.
func encodeOmissions(a *fastjson.Arena, omissions []core.Omission) *fastjson.Value {
	arr := a.NewArray()
	for i, o := range omissions {
		arr.SetArrayItem(i, encodeOmission(a, o))
	}
	return arr
}

// encodeJoinIntent encodes one join target: the intent facts and the
// process to connect to.
func encodeJoinIntent(a *fastjson.Arena, target *orchestrator.JoinTarget) *fastjson.Value {
	v := a.NewObject()
	v.Set("intent_id", a.NewString(target.Intent.ID))
	v.Set("account_id", a.NewString(target.Intent.AccountID))
	v.Set("steam_id", a.NewString(fmt.Sprintf("%d", uint64(target.Intent.SteamID))))
	v.Set("revision_id", a.NewString(target.Intent.RevisionID))
	v.Set("generation", a.NewString(fmt.Sprintf("%d", target.Intent.Generation)))
	v.Set("connect_address", a.NewString(target.Process.ConnectAddress))
	return v
}

// encodeResult encodes one private result for the debrief surface.
func encodeResult(a *fastjson.Arena, result *core.AttemptResult) *fastjson.Value {
	v := a.NewObject()
	v.Set("id", a.NewString(result.ID))
	v.Set("account_id", a.NewString(result.AccountID))
	v.Set("revision_id", a.NewString(result.RevisionID))
	v.Set("process_generation", a.NewString(fmt.Sprintf("%d", result.ProcessGeneration)))
	v.Set("attempt_generation", a.NewString(fmt.Sprintf("%d", result.AttemptGeneration)))
	v.Set("replay_id", a.NewString(result.ReplayID))
	v.Set("takeover_tick", a.NewString(fmt.Sprintf("%d", result.TakeoverTick)))
	return v
}
