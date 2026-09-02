package core

// Fidelity is the reconstruction fidelity of a ScenarioRevision. It mirrors
// the provider's runback-world contract: Preview lists omissions, Complete
// refuses any required omission.
type Fidelity int

const (
	// FidelityPreview marks a reconstruction with listed omissions. It is a
	// private unranked preview, never labeled Runback.
	FidelityPreview Fidelity = iota
	// FidelityComplete marks a reconstruction with zero required omissions.
	FidelityComplete
)

// String returns the stable wire name of the fidelity.
func (f Fidelity) String() string {
	switch f {
	case FidelityComplete:
		return "complete"
	default:
		return "preview"
	}
}
