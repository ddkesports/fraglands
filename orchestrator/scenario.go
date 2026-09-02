package orchestrator

import (
	"context"
	"fmt"

	"github.com/paralin/fraglands/core"
)

// Prepare accepts one preparation request for a replay in the selection
// catalog, owned by the authenticated principal. It creates the queued
// preparation, starts the preparer on its own goroutine, and returns the
// preparation ID immediately: status is read through Preparation.
func (o *Orchestrator) Prepare(
	principal *core.Account,
	replayID string,
	leadInStartTick, takeoverTick uint32,
) (string, error) {
	if principal == nil {
		return "", ErrUnauthenticated
	}

	var id string
	var accepted bool
	o.bcast.HoldLock(func(_ func(), _ func() <-chan struct{}) {
		for _, src := range o.sources {
			if src.ID == replayID {
				accepted = true
				break
			}
		}
		if !accepted {
			return
		}
		o.prepSeq++
		id = prepID(o.prepSeq)
		prep := core.NewScenarioPreparation(id, replayID, leadInStartTick, takeoverTick)
		o.preparations[id] = prep
		o.owners[id] = principal.ID
		lobby, _ := core.NewLobby("lobby-"+id, defaultLobbyCapacity)
		o.lobbies[id] = lobby
	})
	if !accepted {
		return "", ErrUnknownReplay
	}

	// Run the preparer off the request path: the caller watches the
	// preparation lifecycle instead of blocking on the provider.
	prep := o.preparation(id)
	go o.runPreparer(o.ctx, prep)
	return id, nil
}

// Sources returns the private replay selection catalog.
func (o *Orchestrator) Sources() []core.ReplaySource {
	var out []core.ReplaySource
	o.bcast.HoldLock(func(_ func(), _ func() <-chan struct{}) {
		out = append(out, o.sources...)
	})
	return out
}

// Preparation returns the explicit status for one preparation when the
// principal is the preparation owner or a claimed participant, or
// ErrUnknownPreparation for an unknown preparation and ErrForbidden
// otherwise. Connect address and readiness facts stay behind this check.
func (o *Orchestrator) Preparation(principal *core.Account, id string) (PreparationStatus, error) {
	if principal == nil {
		return PreparationStatus{}, ErrUnauthenticated
	}

	var (
		status  PreparationStatus
		found   bool
		allowed bool
	)
	o.bcast.HoldLock(func(_ func(), _ func() <-chan struct{}) {
		prep, ok := o.preparations[id]
		if !ok {
			return
		}
		found = true
		allowed = o.canViewLocked(principal.ID, id)
		status = PreparationStatus{
			Preparation:       prep,
			Lobby:             o.lobbies[id],
			Process:           o.processes[id],
			AllocationFailure: o.allocFailures[id],
		}
	})
	if !found {
		return status, ErrUnknownPreparation
	}
	if !allowed {
		return PreparationStatus{}, ErrForbidden
	}
	return status, nil
}

// canViewLocked reports whether the account may view the preparation
// status: the owner or any account holding a lobby slot. Callers must hold
// the bcast lock.
func (o *Orchestrator) canViewLocked(accountID, prepID string) bool {
	if o.owners[prepID] == accountID {
		return true
	}
	lobby := o.lobbies[prepID]
	if lobby == nil {
		return false
	}
	_, ok := lobby.Slot(accountID)
	return ok
}

// runPreparer drives one preparation to a terminal state, then allocates a
// server process on ready. Allocation failures are recorded as one typed
// reason with no partial process.
func (o *Orchestrator) runPreparer(ctx context.Context, prep *core.ScenarioPreparation) {
	o.preparer.Prepare(ctx, prep)

	state, err := prep.WaitReady(ctx)
	if err != nil {
		// The orchestrator is shutting down; the provider owns the
		// preparation state from here.
		return
	}
	if state != core.PreparationReady {
		// Failed and cancelled carry their typed reason on the preparation
		// itself; nothing is allocated.
		return
	}

	// Allocate one server process for the ready revision.
	revision := prep.Revision()
	proc, err := o.allocator.Allocate(ctx, revision)
	o.bcast.HoldLock(func(broadcast func(), _ func() <-chan struct{}) {
		if err != nil {
			o.allocFailures[prep.ID] = &AllocationFailure{
				Reason: &core.FailureReason{
					Code:    AllocationFailureCode,
					Message: err.Error(),
				},
			}
		} else {
			o.processes[prep.ID] = proc
		}
		broadcast()
	})
}

// preparation returns the preparation record by ID, or nil.
func (o *Orchestrator) preparation(id string) *core.ScenarioPreparation {
	var prep *core.ScenarioPreparation
	o.bcast.HoldLock(func(_ func(), _ func() <-chan struct{}) {
		prep = o.preparations[id]
	})
	return prep
}

// defaultLobbyCapacity is the fixed slot capacity of a preparation lobby.
const defaultLobbyCapacity = 12

// prepID formats a preparation identifier.
func prepID(seq int) string {
	return fmt.Sprintf("prep-%d", seq)
}

// AcceptResult accepts one private result for an attempt. The server
// participant calls this after the attempt ends; a failed upload never
// becomes a result because acceptance is the single store operation.
func (o *Orchestrator) AcceptResult(result *core.AttemptResult) error {
	return o.results.Accept(result)
}
