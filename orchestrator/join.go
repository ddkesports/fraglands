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
// ready, then issues a one-use join intent for the authenticated principal,
// bound to the revision, process generation, and the principal's immutable
// Steam identity. The account must hold a lobby slot.
func (o *Orchestrator) IssueJoinIntent(principal *core.Account, prepID string) (*JoinTarget, error) {
	if principal == nil {
		return nil, ErrUnauthenticated
	}
	if principal.SteamID == 0 {
		return nil, ErrNoSteamIdentity
	}

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
		return nil, &AllocationError{Reason: failure.Reason}
	}
	if status.Process == nil || !status.Process.Ready() {
		return nil, ErrProcessNotReady
	}

	// The account must hold a lobby slot: no slot, no join.
	if status.Lobby != nil {
		if _, ok := status.Lobby.Slot(principal.ID); !ok {
			return nil, core.ErrNoSlotClaimed
		}
	}

	// Bind the intent to the revision, process generation, and the
	// principal's immutable Steam identity: never a client-supplied value.
	revision := status.Preparation.Revision()
	generation := status.Process.Generation
	intentID := o.nextIntentID()
	intent := core.NewJoinIntent(intentID, principal.ID, principal.SteamID, revision.ID, generation)

	o.bcast.HoldLock(func(_ func(), _ func() <-chan struct{}) {
		o.intents[intentID] = intent
	})
	return &JoinTarget{Process: status.Process, Intent: intent}, nil
}

// ConsumeJoinIntent consumes a one-use intent for an authenticated server
// participant. The participant presents the intent facts when the client
// presents at the server: the process generation is the participant's own
// bound generation, never a caller-supplied value. A consumed, mismatched,
// stale, or wrong-process intent is refused and never burned. A successful
// consume records the account admission on the process generation so result
// acceptance is later fenced to admitted accounts only.
func (o *Orchestrator) ConsumeJoinIntent(participant *ServerParticipant, intentID string, revisionID string, steamID core.SteamID) error {
	if participant == nil {
		return ErrUnauthenticated
	}

	var (
		intent *core.JoinIntent
		known  bool
	)
	o.bcast.HoldLock(func(_ func(), _ func() <-chan struct{}) {
		intent = o.intents[intentID]
		if intent == nil {
			return
		}
		// The intent must belong to this server process generation: a
		// participant cannot consume an intent issued for another process.
		if intent.Generation != participant.ProcessGeneration {
			return
		}
		known = true
	})
	if intent == nil {
		return ErrUnknownIntent
	}
	if !known {
		return ErrWrongProcessGeneration
	}

	// One-use consumption under the intent owner lock: a refused consume
	// never marks the intent used.
	if err := intent.Consume(revisionID, participant.ProcessGeneration, steamID); err != nil {
		return err
	}

	// Record the admission: this account may present results on this
	// process generation against this revision, and nothing else.
	record := &admission{
		accountID:         intent.AccountID,
		revisionID:        intent.RevisionID,
		processGeneration: intent.Generation,
	}
	o.bcast.HoldLock(func(broadcast func(), _ func() <-chan struct{}) {
		o.admissions[admissionKey{
			accountID:         record.accountID,
			processGeneration: record.processGeneration,
		}] = record
		broadcast()
	})
	return nil
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
