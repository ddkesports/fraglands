package orchestrator

import "github.com/paralin/fraglands/core"

// PreparationStatus is the explicit status of one preparation, its lobby,
// and its server process allocation. It is the readback the selection and
// debrief surfaces consume.
type PreparationStatus struct {
	// Preparation is the lifecycle record.
	Preparation *core.ScenarioPreparation
	// Lobby is the preparation's lobby.
	Lobby *core.Lobby
	// Process is the allocated server process, or nil before allocation.
	Process *AllocatedProcess
	// AllocationFailure is the typed reason allocation failed, or nil.
	AllocationFailure *AllocationFailure
}
