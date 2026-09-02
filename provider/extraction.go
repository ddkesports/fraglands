package provider

import (
	"fmt"

	"github.com/paralin/s2replay"
)

// ProveTickInterval advances a parser over the demo until the clock reports
// the exact CSVCMsg_ServerInfo tick interval. NextMessage decodes the inner
// packet messages, which triggers the parser's ServerInfo handling and marks
// the clock interval as exact.
//
// It returns the proven seconds-per-tick, or ErrTickIntervalNotProven when
// ServerInfo never appeared before the stream ended: the compiler refuses to
// convert the lead-in window to ticks without provenance, so the provider
// refuses before compilation.
func ProveTickInterval(demo []byte) (float64, error) {
	parser, err := s2replay.NewParser(demo)
	if err != nil {
		return 0, fmt.Errorf("%w: %v", ErrReplayUnreadable, err)
	}
	clock := parser.Clock()
	for !clock.TickIntervalKnown() {
		if _, err := parser.NextMessage(); err != nil {
			return 0, ErrTickIntervalNotProven
		}
	}
	interval := clock.TickInterval()
	if interval <= 0 {
		return 0, ErrTickIntervalNotProven
	}
	return interval, nil
}
