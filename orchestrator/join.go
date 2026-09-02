package orchestrator

import (
	"fmt"

	"github.com/paralin/fraglands/core"
)

// JoinTarget describes where and how the account should join.
type JoinTarget struct {
	// Process is the allocated server process the intent admits to.
	Process *AllocatedProcess
	// Intent is the one-use join intent.
	Intent *core.JoinIntent
}

// IssueJoinIntent validates the preparation is ready and the server process
// ready, then issues a one-use join intent bound to the revision, process
// generation, and Steam identity. The account must hold a lobby slot.
func (o *Orchestrator) IssueJoinIntent(prepID, accountID string, steamID core.SteamID) (*JoinTarget, error) {
	var (
		status PreparationStatus
		found  bool
	)
	o.bcast.HoldLock(func(_ func(), _ func() <-chan struct{}) {
		prep, ok := o.preparations[prepID]
		if !ok {
			return
		}
		found = true
		status = PreparationStatus{
			Preparation:       prep,
			Lobby:             o.lobbies[prepID],
			Process:           o.processes[prepID],
			AllocationFailure: o.allocFailures[prepID],
		}
	})
	if !found {
		return nil, ErrUnknownPreparation
	}

	// A join intent requires a fully prepared scenario.
	if status.Preparation.State() != core.PreparationReady {
		return nil, core.ErrPreparationNotReady
	}

	// An allocation failure is a typed launch failure: surface its reason and
	// never admit a join against a failed allocation.
	if failure := status.AllocationFailure; failure != nil {
		return nil, fmt.Errorf("%s: %s", failure.Reason.Code, failure.Reason.Message)
	}
	if status.Process == nil || !status.Process.Ready() {
		return nil, ErrProcessNotReady
	}

	// The account must hold a lobby slot: no slot, no join.
	if status.Lobby != nil {
		if _, ok := status.Lobby.Slot(accountID); !ok {
			return nil, core.ErrNoSlotClaimed
		}
	}

	// Bind the intent to the revision and process generation.
	revision := status.Preparation.Revision()
	generation := status.Process.Generation
	intentID := o.nextIntentID()
	intent := core.NewJoinIntent(intentID, accountID, steamID, revision.ID, generation)

	o.bcast.HoldLock(func(_ func(), _ func() <-chan struct{}) {
		o.intents[intentID] = intent
	})
	return &JoinTarget{Process: status.Process, Intent: intent}, nil
}

// ConsumeJoinIntent consumes a one-use intent. The server participant calls
// this when the client presents at the server: a consumed, mismatched, or
// stale intent is refused.
func (o *Orchestrator) ConsumeJoinIntent(intentID string, revisionID string, generation uint64, steamID core.SteamID) error {
	intent := o.intentFor(intentID)
	if intent == nil {
		return ErrUnknownIntent
	}
	return intent.Consume(revisionID, generation, steamID)
}

// intentFor returns the intent by ID, or nil.
func (o *Orchestrator) intentFor(id string) *core.JoinIntent {
	var intent *core.JoinIntent
	o.bcast.HoldLock(func(_ func(), _ func() <-chan struct{}) {
		intent = o.intents[id]
	})
	return intent
}

// nextIntentID allocates a unique intent identifier.
func (o *Orchestrator) nextIntentID() string {
	var id string
	o.bcast.HoldLock(func(_ func(), _ func() <-chan struct{}) {
		o.intentSeq++
		id = fmt.Sprintf("intent-%d", o.intentSeq)
	})
	return id
}
