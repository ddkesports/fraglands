package core

// ScenarioRevision is an immutable reference to one prepared reconstruction
// of a replay moment. Once created it never changes; a new preparation
// produces a new revision. The core stores the reference and provenance only.
type ScenarioRevision struct {
	// ID is the immutable revision identifier.
	ID string
	// ReplayID references the ReplaySource the revision was built from.
	ReplayID string
	// LeadInStartTick is the first tick of the recorded lead-in (0..10s).
	LeadInStartTick uint32
	// TakeoverTick is the tick where input unlocks on the same pawn.
	TakeoverTick uint32
	// Fidelity is the reconstruction fidelity the provider committed to.
	Fidelity Fidelity
}
