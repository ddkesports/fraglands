package core

// ReplaySource identifies one replay file offered for scenario preparation.
// The core treats it as opaque provenance; parsing happens in the provider.
type ReplaySource struct {
	// ID is the durable replay identifier.
	ID string
	// DisplayName is the human-readable replay name shown in selection UI.
	DisplayName string
	// FileName is the source replay file name.
	FileName string
}

// MatchRecord is the imported match metadata bound to a ReplaySource. Only
// fields the selection UI needs are carried; game-state facts stay in the
// provider-owned replay facts, never here.
type MatchRecord struct {
	// ReplayID references the ReplaySource this record was imported from.
	ReplayID string
	// MatchID is the provider-assigned match identifier.
	MatchID string
	// DurationTicks is the total recorded length in engine ticks.
	DurationTicks uint32
}
