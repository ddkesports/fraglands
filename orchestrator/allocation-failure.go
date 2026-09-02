package orchestrator

import "github.com/paralin/fraglands/core"

// AllocationFailureCode is the typed reason code recorded when a server
// process could not be allocated for a ready revision.
const AllocationFailureCode = "allocation_failed"

// AllocationFailure records the one typed reason a server process could not
// be allocated for a ready revision. It carries no partial process.
type AllocationFailure struct {
	// Reason is the typed failure reason from the responsible authority.
	Reason *core.FailureReason
}
