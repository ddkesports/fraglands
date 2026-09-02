package core

import (
	"fmt"

	"github.com/paralin/s2replay/analysis"
)

// This file is the pure compiler between s2replay's RunbackFacts (T1) and the
// Fraglands ScenarioRevision consumed by Reconstruction Preview preparation.
// It is a typed contract only: it never parses a replay, never touches a game
// process, and never synthesizes world state. Its sole job is to map an
// already-extracted facts record plus a capability assessment into either an
// immutable revision or typed omissions.

// OmissionKind is a typed reason a field is omitted from a prepared revision.
// The kinds mirror modlock's runback-world OmissionKind so both sides of the
// boundary speak the same vocabulary.
type OmissionKind string

const (
	// OmissionNotObserved means the replay never exposed the field at the tick.
	OmissionNotObserved OmissionKind = "not_observed"
	// OmissionStale means the field was observed but is older than the
	// freshness budget.
	OmissionStale OmissionKind = "stale"
	// OmissionUnsupported means the runtime cannot apply, observe, verify, and
	// reset the field for this entity class.
	OmissionUnsupported OmissionKind = "unsupported"
	// OmissionIneligible means the facts record itself failed the parser's
	// eligibility gate.
	OmissionIneligible OmissionKind = "ineligible"
)

// Omission is one named, typed omission in a compiled revision.
type Omission struct {
	// Kind is the typed reason this field is omitted.
	Kind OmissionKind
	// Subject names the omitted field, e.g. "hero.3.position.x".
	Subject string
	// Required is true when this omission forces fidelity to Preview.
	Required bool
	// Reason carries the responsible component's own explanation.
	Reason string
}

// CapabilityRequirement is the runtime capability row for one field, mirroring
// modlock's runback-world Capability: the provider must support all four
// dimensions and name its evidence for the field to be usable. An empty
// evidence string means the row was never proven.
type CapabilityRequirement struct {
	Apply    bool
	Observe  bool
	Verify   bool
	Reset    bool
	Evidence string
}

// Supported reports whether the runtime fully supports the field.
func (c CapabilityRequirement) Supported() bool {
	return c.Apply && c.Observe && c.Verify && c.Reset && c.Evidence != ""
}

// DefaultRunbackCapabilities returns the capability rows Fraglands currently
// advertises for hero reconstruction. Only the exact kinematic fields the
// replay proves are supported: the runtime can apply, observe, verify, and
// reset pose, facing, and velocity from replay-derived values. Everything not
// listed here (health, shield, level, items, ability state, modifiers, scores)
// stays unsupported and forces Preview. This set is honest today; it must be
// widened only when the provider proves a mechanism per field.
func DefaultRunbackCapabilities() map[string]CapabilityRequirement {
	caps := make(map[string]CapabilityRequirement)
	for _, subject := range []string{
		"position.x", "position.y", "position.z",
		"facing.x", "facing.y", "facing.z",
		"velocity.x", "velocity.y", "velocity.z",
	} {
		caps[subject] = CapabilityRequirement{
			Apply:    true,
			Observe:  true,
			Verify:   true,
			Reset:    true,
			Evidence: "replay-derived kinematic field with parser freshness provenance",
		}
	}
	return caps
}

// CompileRequest binds one RunbackFacts record to the runtime capability rows
// and the freshness budget the provider commits to.
type CompileRequest struct {
	// Facts is the versioned RunbackFacts record extracted by s2replay.
	Facts analysis.RunbackFacts
	// Capabilities maps a field subject (e.g. "position.x") to the runtime
	// capability the provider advertises for it. Absent rows are unsupported.
	Capabilities map[string]CapabilityRequirement
	// MaxFreshnessTicks is the maximum tolerated source age for observed
	// fields. Zero means no budget was declared, which can never yield a
	// Complete grant.
	MaxFreshnessTicks uint32
	// ProvenTickInterval is the exact seconds-per-tick proven from the replay
	// itself (CSVCMsg_ServerInfo tick_interval via the parser clock), passed
	// by the caller. The compiler never assumes a rate: zero or a
	// non-positive interval is a refusal, because the lead-in window cannot
	// be converted from seconds to ticks without provenance.
	ProvenTickInterval float64
	// RevisionID is the caller-supplied immutable revision identifier. The
	// compiler never invents one.
	RevisionID string
}

// ErrCompilationRefused is returned when the facts record cannot be compiled
// into any revision at all. A refusal produces no partial state: no revision,
// no omissions. This is distinct from a Preview revision, which is a valid
// honest outcome that simply lists omissions.
type ErrCompilationRefused struct {
	Reason string
}

func (e *ErrCompilationRefused) Error() string {
	return "core: compilation refused: " + e.Reason
}

// CompileOutcome is the result of one compilation. Either Revision is set
// (with Omissions possibly non-empty for a Preview) or Refusal is set.
type CompileOutcome struct {
	Revision  *ScenarioRevision
	Refusal   *ErrCompilationRefused
	Omissions []Omission
}

// Compile maps RunbackFacts plus a capability assessment into an immutable
// ScenarioRevision, or refuses with a typed reason. The rules are
// fail-closed:
//
//   - a zero tick, an unsupported schema version, a missing revision ID, or
//     an absent/invalid proven tick interval is a refusal: there is no honest
//     revision to build;
//   - an ineligible facts record compiles to a Preview revision carrying an
//     OmissionIneligible entry, never to Complete;
//   - every missing or stale hero kinematic field, and every present field
//     the runtime cannot fully support, is recorded as a required omission;
//   - a Complete grant requires zero required omissions.
//
// The compiler never mutates the input facts and never fabricates a value for
// an omitted field.
func Compile(req CompileRequest) CompileOutcome {
	if req.Facts.Tick == 0 {
		return refusalOutcome("facts tick is zero: no replay moment selected")
	}
	if req.Facts.SchemaVersion != analysis.RunbackFactsSchemaVersion {
		return refusalOutcome(fmt.Sprintf(
			"facts schema version %d unsupported, want %d",
			req.Facts.SchemaVersion, analysis.RunbackFactsSchemaVersion))
	}
	if req.RevisionID == "" {
		return refusalOutcome("revision id is empty: the caller must supply one")
	}
	if req.ProvenTickInterval <= 0 {
		return refusalOutcome(
			"proven tick interval absent or invalid: lead-in seconds cannot be converted to ticks without provenance")
	}

	omissions := collectOmissions(req)

	revision := &ScenarioRevision{
		ID:              req.RevisionID,
		ReplayID:        replayIdentityID(req.Facts.Source),
		LeadInStartTick: leadInStartTick(req.Facts.Tick, req.ProvenTickInterval),
		TakeoverTick:    req.Facts.Tick,
		Fidelity:        grantedFidelity(omissions),
		Omissions:       omissions,
	}

	return CompileOutcome{
		Revision:  revision,
		Omissions: omissions,
	}
}

// CompileOrRefuse returns just the revision or an error, for callers that do
// not need the full omission list.
func CompileOrRefuse(req CompileRequest) (*ScenarioRevision, error) {
	out := Compile(req)
	if out.Refusal != nil {
		return nil, out.Refusal
	}
	return out.Revision, nil
}

func collectOmissions(req CompileRequest) []Omission {
	var omissions []Omission

	if req.Facts.Eligibility == analysis.ReplayEligibilityIneligible {
		reason := "replay facts are ineligible for scenario use"
		if len(req.Facts.EligibilityReasons) > 0 {
			reason = req.Facts.EligibilityReasons[0]
		}
		omissions = append(omissions, Omission{
			Kind:     OmissionIneligible,
			Subject:  "eligibility",
			Required: true,
			Reason:   reason,
		})
	}

	for _, hero := range req.Facts.Heroes {
		omissions = append(omissions, heroOmissions(hero, req.Capabilities, req.MaxFreshnessTicks)...)
	}
	return omissions
}

// heroOmissions returns the typed omissions for one hero row. Missing
// kinematic fields are not_observed; present-but-old fields are stale;
// present fields lacking full runtime capability are unsupported.
func heroOmissions(
	hero analysis.RunbackHero,
	caps map[string]CapabilityRequirement,
	maxFreshness uint32,
) []Omission {
	var out []Omission

	fields := []struct {
		subject string
		value   analysis.RunbackFloat
	}{
		{"position.x", hero.Position[0]},
		{"position.y", hero.Position[1]},
		{"position.z", hero.Position[2]},
		{"facing.x", hero.Facing[0]},
		{"facing.y", hero.Facing[1]},
		{"facing.z", hero.Facing[2]},
		{"velocity.x", hero.Velocity[0]},
		{"velocity.y", hero.Velocity[1]},
		{"velocity.z", hero.Velocity[2]},
	}

	for _, f := range fields {
		subject := fmt.Sprintf("hero.%d.%s", hero.PlayerSlot, f.subject)
		if !f.value.Present {
			out = append(out, Omission{
				Kind:     OmissionNotObserved,
				Subject:  subject,
				Required: true,
				Reason:   f.value.MissingReason,
			})
			continue
		}
		if maxFreshness == 0 || f.value.FreshnessTicks > maxFreshness {
			out = append(out, Omission{
				Kind:     OmissionStale,
				Subject:  subject,
				Required: true,
				Reason:   "parser evidence is older than the freshness limit",
			})
			continue
		}
		if cap, ok := caps[f.subject]; !ok || !cap.Supported() {
			out = append(out, Omission{
				Kind:     OmissionUnsupported,
				Subject:  subject,
				Required: true,
				Reason:   "runtime cannot apply, observe, verify, and reset this field",
			})
		}
	}
	return out
}

// grantedFidelity is Complete only when there are zero required omissions.
func grantedFidelity(omissions []Omission) Fidelity {
	for _, o := range omissions {
		if o.Required {
			return FidelityPreview
		}
	}
	return FidelityComplete
}

// Lead-in window constants in seconds. The plan locks the default at five
// seconds and the maximum at ten; the tick conversion itself always uses the
// caller's proven interval, never a compiled-in rate.
const (
	DefaultLeadInSeconds = 5.0
	MaxLeadInSeconds     = 10.0
)

// leadInStartTick converts the plan's five-second default lead-in into ticks
// using the proven seconds-per-tick interval, clamped to the 0-10 second
// window and to the start of the replay. The caller must have validated the
// interval before calling Compile.
func leadInStartTick(takeoverTick uint32, secondsPerTick float64) uint32 {
	leadInSeconds := DefaultLeadInSeconds
	if leadInSeconds > MaxLeadInSeconds {
		leadInSeconds = MaxLeadInSeconds
	}
	leadInTicks := uint32(leadInSeconds / secondsPerTick)
	if leadInTicks > takeoverTick {
		leadInTicks = takeoverTick
	}
	return takeoverTick - leadInTicks
}

// replayIdentityID derives the stable replay identifier from the facts source
// identity. The SHA-256 is the content address; the match ID is the fallback.
func replayIdentityID(source analysis.ReplaySourceIdentity) string {
	if source.SHA256 != "" {
		return source.SHA256
	}
	if source.MatchID != 0 {
		return fmt.Sprintf("match-%d", source.MatchID)
	}
	return "unknown-replay"
}

func refusalOutcome(reason string) CompileOutcome {
	return CompileOutcome{Refusal: &ErrCompilationRefused{Reason: reason}}
}
