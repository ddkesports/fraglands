package server

import (
	"fmt"
	"strconv"

	"github.com/aperturerobotics/fastjson"
)

// TerminalSummaryVersion is the only contract version of a modlock runback
// TerminalSummary this decoder accepts. The version string is the wire
// contract: if the spool format ever changes, the version changes, and this
// decoder refuses the old format instead of guessing.
const TerminalSummaryVersion = "runback-attempt/v1"

// MaxTerminalSummaryBytes bounds one artifact accepted for decoding. It is
// generous enough for a large timeline, disconnect, and fact list, but small
// enough that a runaway or hostile artifact cannot exhaust the host.
const MaxTerminalSummaryBytes = 64 << 10

// TerminalSummaryArtifactName is the artifact name bound to the terminal
// summary contract.
const TerminalSummaryArtifactName = "terminal-summary.json"

// Errors for typed refusals. Callers match with errors.Is.
var (
	// ErrSummaryMalformed is returned when an artifact cannot be decoded as
	// the versioned terminal summary contract. No partial result is
	// produced.
	ErrSummaryMalformed = fmt.Errorf("server: terminal summary artifact malformed")
	// ErrSummaryUnknownVersion is returned when the artifact declares a
	// contract version this decoder does not implement.
	ErrSummaryUnknownVersion = fmt.Errorf("server: terminal summary contract version not supported")
	// ErrSummaryOversize is returned when the artifact exceeds
	// MaxTerminalSummaryBytes.
	ErrSummaryOversize = fmt.Errorf("server: terminal summary artifact exceeds size limit")
	// ErrSummaryDuplicate is returned when the same artifact identity was
	// already ingested on this process generation.
	ErrSummaryDuplicate = fmt.Errorf("server: terminal summary artifact already ingested")
	// ErrSummaryTraversal is returned when an artifact name escapes the
	// spool directory.
	ErrSummaryTraversal = fmt.Errorf("server: artifact name escapes the spool directory")
	// ErrUnauthenticated is returned when the presented server credential
	// does not resolve to an authenticated server participant.
	ErrUnauthenticated = fmt.Errorf("server: unauthenticated server participant")
	// ErrSummaryIdentityMismatch is returned when the decoded identity does
	// not match the authenticated participant or the prior admission.
	ErrSummaryIdentityMismatch = fmt.Errorf("server: terminal summary identity mismatch")
)

// summaryField is one known wire field of the TerminalSummary contract.
// Unknown fields are refused: the artifact is an immutable, versioned
// record, so a field this decoder does not know means the artifact was
// produced by a different contract than it claims.
type summaryField int

const (
	fieldVersion summaryField = iota
	fieldReplayIdentity
	fieldRevision
	fieldServerProcessGeneration
	fieldAttemptGeneration
	fieldTakeoverTick
	fieldTakeoverAtSeconds
	fieldEnding
	fieldEndedAtSeconds
	fieldTimeline
	fieldDisconnects
	fieldFacts
	fieldCount
)

// summaryFieldNames lists every known field in wire form, indexed by field
// id.
var summaryFieldNames = [fieldCount]string{
	fieldVersion:                 "version",
	fieldReplayIdentity:          "replay_identity",
	fieldRevision:                "revision",
	fieldServerProcessGeneration: "server_process_generation",
	fieldAttemptGeneration:       "attempt_generation",
	fieldTakeoverTick:            "takeover_tick",
	fieldTakeoverAtSeconds:       "takeover_at_seconds",
	fieldEnding:                  "ending",
	fieldEndedAtSeconds:          "ended_at_seconds",
	fieldTimeline:                "timeline",
	fieldDisconnects:             "disconnects",
	fieldFacts:                   "facts",
}

// summaryFieldIndex maps a wire field name to its field id.
var summaryFieldIndex = func() map[string]summaryField {
	m := make(map[string]summaryField, fieldCount)
	for i, name := range summaryFieldNames {
		m[name] = summaryField(i)
	}
	return m
}()

// TerminalSummary mirrors the modlock runback TerminalSummary contract
// (runback-attempt/v1): the immutable record an attempt publishes exactly
// once when it ends, bound to the attempt's identity: replay identity,
// revision, server process generation, attempt generation, and takeover
// tick. The identity fields are required: a spool artifact that cannot
// prove them is never decoded into a result.
type TerminalSummary struct {
	// Version is the contract version; only TerminalSummaryVersion is
	// accepted.
	Version string
	// ReplayIdentity is the replay the attempt ran against.
	ReplayIdentity string
	// Revision is the immutable revision the attempt ran against.
	Revision string
	// ServerProcessGeneration is the server process generation that hosted
	// the attempt.
	ServerProcessGeneration uint64
	// AttemptGeneration is the attempt's own generation.
	AttemptGeneration uint64
	// TakeoverTick is the timecode measurement started at.
	TakeoverTick uint32
	// TakeoverAtSeconds is the attempt-clock second of takeover.
	TakeoverAtSeconds uint32
	// Ending is the typed ending; one of the contract's ending names.
	Ending string
	// EndedAtSeconds is the attempt-clock second the attempt ended at.
	EndedAtSeconds uint32
	// Timeline is the timestamped phase transitions.
	Timeline []AttemptTransition
	// Disconnects is the disconnect-to-bot conversions.
	Disconnects []DisconnectRecord
	// Facts is the comparison facts.
	Facts []ComparisonFact
}

// AttemptTransition is one timestamped phase transition. Timestamps are on
// the attempt clock in seconds since takeover.
type AttemptTransition struct {
	// State is the phase; one of the contract's state names.
	State string
	// AtSeconds is the attempt-clock second of the transition.
	AtSeconds uint32
}

// DisconnectRecord is one disconnect-to-bot conversion.
type DisconnectRecord struct {
	// EntityID is the entity that disconnected.
	EntityID uint32
	// DisconnectedAtSeconds is the attempt-clock second of the disconnect.
	DisconnectedAtSeconds uint32
	// ReclaimedAtSeconds is set when the identity reclaimed the pawn.
	ReclaimedAtSeconds *uint32
}

// ComparisonFact is one comparison fact in the terminal summary. The
// comparison is the record; there is no score. An unsupported fact carries
// an explicit reason instead of a fabricated value.
type ComparisonFact struct {
	// Name is the compared field.
	Name string
	// Source is the typed source; one of the contract's source names.
	Source string
	// Value is the observed value.
	Value string
	// ReplayValue is the replay-derived value.
	ReplayValue string
	// UnsupportedReason is set when Source is "unsupported".
	UnsupportedReason string
}

// validEndingNames are the only ending values the contract admits.
var validEndingNames = map[string]bool{
	"secure":                 true,
	"abandoned-objective":    true,
	"unresolved":             true,
	"infrastructure-failure": true,
}

// validFactSourceNames are the only fact sources the contract admits.
var validFactSourceNames = map[string]bool{
	"replay-derived":  true,
	"server-observed": true,
	"unsupported":     true,
}

// validTimelineStates are the only timeline states the contract admits.
var validTimelineStates = map[string]bool{
	"prepared":  true,
	"countdown": true,
	"live":      true,
	"ended":     true,
}

// ValidateArtifactName refuses artifact names that could escape the spool
// directory: path separators, parent references, absolute paths, and empty
// names. The artifact name is untrusted worker input.
func ValidateArtifactName(name string) error {
	if name == "" {
		return fmt.Errorf("%w: empty artifact name", ErrSummaryTraversal)
	}
	if name != TerminalSummaryArtifactName {
		// The spool ingestion contract admits exactly one artifact name
		// today; anything else is refused before it is ever opened.
		return fmt.Errorf("%w: unknown artifact %q", ErrSummaryTraversal, name)
	}
	return nil
}

// CheckArtifactSize refuses an artifact over MaxTerminalSummaryBytes before
// any parsing happens.
func CheckArtifactSize(size int) error {
	if size > MaxTerminalSummaryBytes {
		return fmt.Errorf("%w: %d bytes", ErrSummaryOversize, size)
	}
	return nil
}

// ParseTerminalSummaryArtifact decodes one spool artifact into a
// TerminalSummary under the versioned contract. It is total: any deviation
// from the contract is a typed refusal and no partial summary is produced.
//
// Refusals cover: oversize artifacts, non-object or unparsable JSON,
// duplicate or unknown fields, missing identity or revision fields, empty
// identity strings, zero generation values, wrong JSON types, unknown enum
// values, and unknown contract versions.
func ParseTerminalSummaryArtifact(data []byte) (*TerminalSummary, error) {
	if err := CheckArtifactSize(len(data)); err != nil {
		return nil, err
	}

	var parser fastjson.Parser
	root, err := parser.ParseBytes(data)
	if err != nil {
		return nil, fmt.Errorf("%w: invalid JSON", ErrSummaryMalformed)
	}
	obj, err := root.Object()
	if err != nil {
		return nil, fmt.Errorf("%w: top-level value is not an object", ErrSummaryMalformed)
	}

	summary := &TerminalSummary{}
	seen := make([]bool, fieldCount)

	// Visit every key exactly once: duplicates and unknowns are refused.
	var decodeErr error
	obj.Visit(func(key []byte, v *fastjson.Value) {
		if decodeErr != nil {
			return
		}
		id, ok := summaryFieldIndex[string(key)]
		if !ok {
			decodeErr = fmt.Errorf("%w: unknown field %q", ErrSummaryMalformed, string(key))
			return
		}
		if seen[id] {
			decodeErr = fmt.Errorf("%w: duplicate field %q", ErrSummaryMalformed, string(key))
			return
		}
		seen[id] = true
		decodeErr = decodeSummaryField(summary, id, v)
	})
	if decodeErr != nil {
		return nil, decodeErr
	}

	// Every identity and revision field is required: a spool artifact that
	// cannot prove what attempt it belongs to is refused whole.
	for _, id := range []summaryField{
		fieldVersion, fieldReplayIdentity, fieldRevision,
		fieldServerProcessGeneration, fieldAttemptGeneration,
		fieldTakeoverTick, fieldEnding, fieldEndedAtSeconds,
	} {
		if !seen[id] {
			return nil, fmt.Errorf("%w: missing field %q", ErrSummaryMalformed, summaryFieldNames[id])
		}
	}

	if summary.Version != TerminalSummaryVersion {
		return nil, fmt.Errorf("%w: %q", ErrSummaryUnknownVersion, summary.Version)
	}
	if summary.ReplayIdentity == "" || summary.Revision == "" {
		return nil, fmt.Errorf("%w: empty identity", ErrSummaryMalformed)
	}
	if summary.ServerProcessGeneration == 0 || summary.AttemptGeneration == 0 {
		return nil, fmt.Errorf("%w: zero generation", ErrSummaryMalformed)
	}
	if !validEndingNames[summary.Ending] {
		return nil, fmt.Errorf("%w: unknown ending %q", ErrSummaryMalformed, summary.Ending)
	}
	return summary, nil
}

// decodeSummaryField decodes one known field into the summary. A wrong type
// or an unknown enum value is a refusal; nothing partial is written.
func decodeSummaryField(summary *TerminalSummary, id summaryField, v *fastjson.Value) error {
	var err error
	switch id {
	case fieldVersion:
		summary.Version, err = decodeString(v)
	case fieldReplayIdentity:
		summary.ReplayIdentity, err = decodeString(v)
	case fieldRevision:
		summary.Revision, err = decodeString(v)
	case fieldEnding:
		summary.Ending, err = decodeString(v)
	case fieldServerProcessGeneration:
		summary.ServerProcessGeneration, err = decodeUint64(v)
	case fieldAttemptGeneration:
		summary.AttemptGeneration, err = decodeUint64(v)
	case fieldTakeoverTick:
		summary.TakeoverTick, err = decodeUint32(v)
	case fieldTakeoverAtSeconds:
		summary.TakeoverAtSeconds, err = decodeUint32(v)
	case fieldEndedAtSeconds:
		summary.EndedAtSeconds, err = decodeUint32(v)
	case fieldTimeline:
		summary.Timeline, err = decodeTimeline(v)
	case fieldDisconnects:
		summary.Disconnects, err = decodeDisconnects(v)
	case fieldFacts:
		summary.Facts, err = decodeFacts(v)
	default:
		err = fmt.Errorf("%w: unhandled field", ErrSummaryMalformed)
	}
	if err != nil {
		return fmt.Errorf("%w: field %q: %v", ErrSummaryMalformed, summaryFieldNames[id], err)
	}
	return nil
}

// decodeString decodes one JSON string.
func decodeString(v *fastjson.Value) (string, error) {
	b, err := v.StringBytes()
	if err != nil {
		return "", err
	}
	return string(b), nil
}

// decodeUint64 decodes one non-negative JSON integer into a uint64. It
// refuses floats, exponents, and out-of-range values.
func decodeUint64(v *fastjson.Value) (uint64, error) {
	return v.Uint64()
}

// decodeUint32 decodes one non-negative JSON integer into a uint32. It
// refuses floats, exponents, and values outside the uint32 range.
func decodeUint32(v *fastjson.Value) (uint32, error) {
	u, err := v.Uint64()
	if err != nil {
		return 0, err
	}
	if u > uint64(^uint32(0)) {
		return 0, fmt.Errorf("value %d exceeds uint32 range", u)
	}
	return uint32(u), nil
}

// decodeTimeline decodes the timeline array.
func decodeTimeline(v *fastjson.Value) ([]AttemptTransition, error) {
	items, err := v.Array()
	if err != nil {
		return nil, err
	}
	out := make([]AttemptTransition, 0, len(items))
	for i, item := range items {
		obj, err := item.Object()
		if err != nil {
			return nil, fmt.Errorf("item %d is not an object", i)
		}
		stateV := obj.Get("state")
		if stateV == nil {
			return nil, fmt.Errorf("item %d missing state", i)
		}
		state, err := decodeString(stateV)
		if err != nil {
			return nil, fmt.Errorf("item %d state: %v", i, err)
		}
		if !validTimelineStates[state] {
			return nil, fmt.Errorf("item %d unknown state %q", i, state)
		}
		atV := obj.Get("at_seconds")
		if atV == nil {
			return nil, fmt.Errorf("item %d missing at_seconds", i)
		}
		at, err := decodeUint32(atV)
		if err != nil {
			return nil, fmt.Errorf("item %d at_seconds: %v", i, err)
		}
		// Timeline objects carry exactly the two contract fields.
		if obj.Len() != 2 {
			return nil, fmt.Errorf("item %d has %d fields, want 2", i, obj.Len())
		}
		out = append(out, AttemptTransition{State: state, AtSeconds: at})
	}
	return out, nil
}

// decodeDisconnects decodes the disconnects array.
func decodeDisconnects(v *fastjson.Value) ([]DisconnectRecord, error) {
	items, err := v.Array()
	if err != nil {
		return nil, err
	}
	out := make([]DisconnectRecord, 0, len(items))
	for i, item := range items {
		obj, err := item.Object()
		if err != nil {
			return nil, fmt.Errorf("item %d is not an object", i)
		}
		entityV := obj.Get("entity_id")
		if entityV == nil {
			return nil, fmt.Errorf("item %d missing entity_id", i)
		}
		entity, err := decodeUint32(entityV)
		if err != nil {
			return nil, fmt.Errorf("item %d entity_id: %v", i, err)
		}
		disV := obj.Get("disconnected_at_seconds")
		if disV == nil {
			return nil, fmt.Errorf("item %d missing disconnected_at_seconds", i)
		}
		disconnectedAt, err := decodeUint32(disV)
		if err != nil {
			return nil, fmt.Errorf("item %d disconnected_at_seconds: %v", i, err)
		}
		var reclaimed *uint32
		if recV := obj.Get("reclaimed_at_seconds"); recV != nil {
			if recV.Type() == fastjson.TypeNull {
				reclaimed = nil
			} else {
				rec, err := decodeUint32(recV)
				if err != nil {
					return nil, fmt.Errorf("item %d reclaimed_at_seconds: %v", i, err)
				}
				reclaimed = &rec
			}
		}
		// Disconnect objects carry exactly the three contract fields.
		if obj.Len() != 3 {
			return nil, fmt.Errorf("item %d has %d fields, want 3", i, obj.Len())
		}
		out = append(out, DisconnectRecord{
			EntityID:              entity,
			DisconnectedAtSeconds: disconnectedAt,
			ReclaimedAtSeconds:    reclaimed,
		})
	}
	return out, nil
}

// decodeFacts decodes the comparison facts array.
func decodeFacts(v *fastjson.Value) ([]ComparisonFact, error) {
	items, err := v.Array()
	if err != nil {
		return nil, err
	}
	out := make([]ComparisonFact, 0, len(items))
	for i, item := range items {
		obj, err := item.Object()
		if err != nil {
			return nil, fmt.Errorf("item %d is not an object", i)
		}
		fact := ComparisonFact{}
		nameV := obj.Get("name")
		if nameV == nil {
			return nil, fmt.Errorf("item %d missing name", i)
		}
		fact.Name, err = decodeString(nameV)
		if err != nil {
			return nil, fmt.Errorf("item %d name: %v", i, err)
		}
		if fact.Name == "" {
			return nil, fmt.Errorf("item %d empty name", i)
		}
		sourceV := obj.Get("source")
		if sourceV == nil {
			return nil, fmt.Errorf("item %d missing source", i)
		}
		fact.Source, err = decodeString(sourceV)
		if err != nil {
			return nil, fmt.Errorf("item %d source: %v", i, err)
		}
		if !validFactSourceNames[fact.Source] {
			return nil, fmt.Errorf("item %d unknown source %q", i, fact.Source)
		}
		if valV := obj.Get("value"); valV != nil {
			if fact.Value, err = decodeString(valV); err != nil {
				return nil, fmt.Errorf("item %d value: %v", i, err)
			}
		}
		if repV := obj.Get("replay_value"); repV != nil {
			if fact.ReplayValue, err = decodeString(repV); err != nil {
				return nil, fmt.Errorf("item %d replay_value: %v", i, err)
			}
		}
		if unsV := obj.Get("unsupported_reason"); unsV != nil {
			if fact.UnsupportedReason, err = decodeString(unsV); err != nil {
				return nil, fmt.Errorf("item %d unsupported_reason: %v", i, err)
			}
		}
		// Fact objects carry at most the five contract fields, and name and
		// source are required.
		if obj.Len() > 5 {
			return nil, fmt.Errorf("item %d has %d fields, want at most 5", i, obj.Len())
		}
		out = append(out, fact)
	}
	return out, nil
}

// unused keeps strconv imported only when needed by future field decoders.
var _ = strconv.Itoa
