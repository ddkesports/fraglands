package server

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/aperturerobotics/fastjson"
)

// TerminalSummaryVersion is the only contract version of a modlock runback
// TerminalSummary this decoder accepts. The version string is the wire
// contract: if the spool format ever changes, the version changes, and this
// decoder refuses the old format instead of guessing.
const TerminalSummaryVersion = "runback-attempt/v1"

// MaxTerminalSummaryBytes bounds one artifact accepted for decoding. The
// real spool record is one short JSON line; the bound exists so a runaway
// or hostile artifact cannot exhaust the host.
const MaxTerminalSummaryBytes = 64 << 10

// TerminalSummaryArtifactName is the artifact name bound to the terminal
// summary contract, matching the writer: "runback_summary_gen" plus the
// attempt generation plus ".json" (see modlock runback_attempt_feed.cc).
const TerminalSummaryArtifactName = "runback_summary_gen"

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
	// ErrSummaryIdentityMismatch is returned when the decoded identity does
	// not match the authenticated server participant.
	ErrSummaryIdentityMismatch = fmt.Errorf("server: terminal summary identity mismatch")
	// ErrUnauthenticated is returned when the presented server credential
	// does not resolve to an authenticated server participant.
	ErrUnauthenticated = fmt.Errorf("server: unauthenticated server participant")
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
	fieldEnding
	fieldEndedAtSeconds
	fieldCount
)

// summaryFieldNames lists every known field in wire form, indexed by field
// id. It mirrors exactly the eight keys emitted by the modlock spool writer
// for runback-attempt/v1: no timeline, disconnects, facts, or
// takeover_at_seconds.
var summaryFieldNames = [fieldCount]string{
	fieldVersion:                 "version",
	fieldReplayIdentity:          "replay_identity",
	fieldRevision:                "revision",
	fieldServerProcessGeneration: "server_process_generation",
	fieldAttemptGeneration:       "attempt_generation",
	fieldTakeoverTick:            "takeover_tick",
	fieldEnding:                  "ending",
	fieldEndedAtSeconds:          "ended_at_seconds",
}

// summaryFieldIndex maps a wire field name to its field id.
var summaryFieldIndex = func() map[string]summaryField {
	m := make(map[string]summaryField, fieldCount)
	for i, name := range summaryFieldNames {
		m[name] = summaryField(i)
	}
	return m
}()

// TerminalSummary mirrors the exact spool record the modlock host writes
// for runback-attempt/v1: eight keys, one JSON line per attempt generation.
// It is narrower than the in-process C++ TerminalSummary: the writer omits
// timeline, disconnects, facts, and takeover_at_seconds, so those are not
// part of the wire contract and their presence is refused.
type TerminalSummary struct {
	// Version is the contract version; only TerminalSummaryVersion is
	// accepted.
	Version string
	// Revision is the immutable revision the attempt ran against.
	Revision string
	// ReplayIdentity is the replay the attempt ran against.
	ReplayIdentity string
	// AttemptGeneration is the attempt's own generation.
	AttemptGeneration uint64
	// ServerProcessGeneration is the server process generation that hosted
	// the attempt.
	ServerProcessGeneration uint64
	// TakeoverTick is the timecode measurement started at.
	TakeoverTick uint32
	// Ending is the typed ending; one of the contract's ending names.
	Ending string
	// EndedAtSeconds is the attempt-clock second the attempt ended at.
	EndedAtSeconds uint32
}

// validEndingNames are the only ending values the contract admits.
var validEndingNames = map[string]bool{
	"secure":                 true,
	"abandoned-objective":    true,
	"unresolved":             true,
	"infrastructure-failure": true,
}

// ValidateArtifactName refuses artifact names that could escape the spool
// directory or that do not match the writer's naming scheme
// ("runback_summary_gen<generation>.json"). The artifact name is untrusted
// worker input; generation must be the attempt generation the record
// carries.
func ValidateArtifactName(name string, attemptGeneration uint64) error {
	if name == "" {
		return fmt.Errorf("%w: empty artifact name", ErrSummaryTraversal)
	}
	prefix := "runback_summary_gen"
	if !strings.HasPrefix(name, prefix) || !strings.HasSuffix(name, ".json") {
		return fmt.Errorf("%w: unknown artifact %q", ErrSummaryTraversal, name)
	}
	genPart := name[len(prefix) : len(name)-len(".json")]
	if genPart == "" {
		return fmt.Errorf("%w: missing generation in %q", ErrSummaryTraversal, name)
	}
	// The digits must exactly match the decimal form of the generation:
	// leading zeros, extra digits, or a different generation are refused.
	if genPart != strconv.FormatUint(attemptGeneration, 10) {
		return fmt.Errorf("%w: artifact name %q does not match generation %d", ErrSummaryTraversal, name, attemptGeneration)
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
	case fieldEndedAtSeconds:
		summary.EndedAtSeconds, err = decodeUint32(v)
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

// unused keeps strconv imported only when needed by future field decoders.
var _ = strconv.Itoa
