package core

import (
	"context"

	"github.com/aperturerobotics/util/broadcast"
	"github.com/pkg/errors"
)

// PreparationState is the lifecycle state of one ScenarioPreparation.
type PreparationState int

const (
	// PreparationQueued means the preparation is accepted and waiting to run.
	PreparationQueued PreparationState = iota
	// PreparationRunning means the preparation is executing.
	PreparationRunning
	// PreparationReady means the preparation produced an immutable revision.
	PreparationReady
	// PreparationFailed means the preparation failed with one typed reason.
	PreparationFailed
	// PreparationCancelled means the preparation was cancelled before ready.
	PreparationCancelled
)

// String returns the stable wire name of the preparation state.
func (s PreparationState) String() string {
	switch s {
	case PreparationRunning:
		return "running"
	case PreparationReady:
		return "ready"
	case PreparationFailed:
		return "failed"
	case PreparationCancelled:
		return "cancelled"
	default:
		return "queued"
	}
}

// Terminal returns true when the state is a terminal state.
func (s PreparationState) Terminal() bool {
	return s == PreparationReady || s == PreparationFailed || s == PreparationCancelled
}

// FailureReason is one typed launch or preparation failure reason. A failed
// preparation carries exactly one reason and never partial state.
type FailureReason struct {
	// Code is the stable typed reason code.
	Code string
	// Message is the human-readable detail for the debrief surface.
	Message string
}

// ScenarioPreparation is the lifecycle record for one attempt to build a
// ScenarioRevision from a replay moment. The state owner exposes a wait that
// cannot miss a transition; callers never poll.
type ScenarioPreparation struct {
	// ID is the preparation identifier.
	ID string
	// ReplayID references the ReplaySource being prepared.
	ReplayID string
	// LeadInStartTick and TakeoverTick select the moment within the replay.
	LeadInStartTick uint32
	// TakeoverTick is the tick where input unlocks on the same pawn.
	TakeoverTick uint32

	// grant is the immutable replay authorization grant minted at
	// acceptance. It is private: no accessor exposes the grant or its token
	// except ReplayRequest, which exists solely for the provider's
	// authorized ReplayStore call. It is never serialized by any surface.
	grant *ReplayGrant

	// bcast guards state, revision, and failure below.
	bcast broadcast.Broadcast
	// state is the current lifecycle state.
	state PreparationState
	// revision is set exactly once when the state reaches ready.
	revision *ScenarioRevision
	// failure is set exactly once when the state reaches failed.
	failure *FailureReason
}

// NewScenarioPreparation constructs a queued preparation for one replay
// moment, holding the minted replay grant privately. A nil grant is
// accepted only for callers that never fetch replay bytes (test fakes); the
// provider refuses to fetch without one.
func NewScenarioPreparation(id, replayID string, leadInStartTick, takeoverTick uint32, grant *ReplayGrant) *ScenarioPreparation {
	return &ScenarioPreparation{
		ID:              id,
		ReplayID:        replayID,
		LeadInStartTick: leadInStartTick,
		TakeoverTick:    takeoverTick,
		grant:           grant,
	}
}

// ReplayRequest builds the authorized request for this preparation's replay
// bytes: the preparation ID, the replay ID, and the private grant token,
// bound together. A preparation without a grant yields a nil request: the
// provider refuses to call the store without authorization.
func (p *ScenarioPreparation) ReplayRequest() *ReplayRequest {
	var grant *ReplayGrant
	p.bcast.HoldLock(func(_ func(), _ func() <-chan struct{}) {
		grant = p.grant
	})
	if grant == nil {
		return nil
	}
	return &ReplayRequest{
		PreparationID: p.ID,
		ReplayID:      p.ReplayID,
		Grant:         grant.Token(),
	}
}

// State returns the current lifecycle state.
func (p *ScenarioPreparation) State() PreparationState {
	var state PreparationState
	p.bcast.HoldLock(func(_ func(), _ func() <-chan struct{}) {
		state = p.state
	})
	return state
}

// Revision returns the immutable revision once ready, or nil.
func (p *ScenarioPreparation) Revision() *ScenarioRevision {
	var revision *ScenarioRevision
	p.bcast.HoldLock(func(_ func(), _ func() <-chan struct{}) {
		revision = p.revision
	})
	return revision
}

// Failure returns the typed failure reason once failed, or nil.
func (p *ScenarioPreparation) Failure() *FailureReason {
	var failure *FailureReason
	p.bcast.HoldLock(func(_ func(), _ func() <-chan struct{}) {
		failure = p.failure
	})
	return failure
}

// WaitReady waits until the preparation reaches a terminal state and returns
// the final state. It cannot miss a transition: the state read and the wait
// channel are obtained in the same HoldLock.
func (p *ScenarioPreparation) WaitReady(ctx context.Context) (PreparationState, error) {
	for {
		var state PreparationState
		var waitCh <-chan struct{}
		p.bcast.HoldLock(func(_ func(), getWaitCh func() <-chan struct{}) {
			state = p.state
			waitCh = getWaitCh()
		})
		if state.Terminal() {
			return state, nil
		}
		select {
		case <-ctx.Done():
			return state, context.Canceled
		case <-waitCh:
		}
	}
}

// MarkRunning moves a queued preparation to running.
func (p *ScenarioPreparation) MarkRunning() error {
	return p.transition(PreparationRunning, nil, nil)
}

// MarkReady moves a running preparation to ready with its immutable revision.
func (p *ScenarioPreparation) MarkReady(revision *ScenarioRevision) error {
	if revision == nil {
		return errors.New("core: ready requires a revision")
	}
	return p.transition(PreparationReady, revision, nil)
}

// MarkFailed moves a running preparation to failed with one typed reason.
func (p *ScenarioPreparation) MarkFailed(reason *FailureReason) error {
	if reason == nil {
		return errors.New("core: failed requires a typed reason")
	}
	return p.transition(PreparationFailed, nil, reason)
}

// MarkCancelled moves a non-terminal preparation to cancelled.
func (p *ScenarioPreparation) MarkCancelled() error {
	return p.transition(PreparationCancelled, nil, nil)
}

// transition applies one forward state transition under the state owner lock.
func (p *ScenarioPreparation) transition(
	next PreparationState,
	revision *ScenarioRevision,
	failure *FailureReason,
) error {
	var err error
	p.bcast.HoldLock(func(broadcast func(), _ func() <-chan struct{}) {
		if p.state.Terminal() {
			err = errors.Errorf("core: preparation already terminal in state %s", p.state)
			return
		}
		switch next {
		case PreparationRunning:
			if p.state != PreparationQueued {
				err = errors.Errorf("core: %s preparation cannot transition to running", p.state)
				return
			}
		case PreparationReady:
			if p.state != PreparationRunning {
				err = errors.Errorf("core: %s preparation cannot transition to ready", p.state)
				return
			}
		case PreparationFailed, PreparationCancelled:
			// Any non-terminal state may fail or be cancelled with no partial state.
		default:
			err = errors.Errorf("core: cannot transition to %s", next)
			return
		}
		p.state = next
		p.revision = revision
		p.failure = failure
		broadcast()
	})
	return err
}
