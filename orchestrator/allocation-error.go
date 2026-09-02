package orchestrator

import (
	"fmt"

	"github.com/paralin/fraglands/core"
)

// AllocationError is returned when an operation requires a server process
// whose allocation failed. It preserves the typed failure reason so callers
// and the API surface can map it without string parsing.
type AllocationError struct {
	// Reason is the typed failure reason from the responsible authority.
	Reason *core.FailureReason
}

// Error returns the typed reason code and message.
func (e *AllocationError) Error() string {
	return fmt.Sprintf("orchestrator: allocation failed: %s: %s", e.Reason.Code, e.Reason.Message)
}
