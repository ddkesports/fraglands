package api

import (
	"errors"
	"net/http"

	"github.com/paralin/fraglands/core"
	"github.com/paralin/fraglands/orchestrator"
)

// ErrorCode is one stable typed error code carried by every JSON error.
type ErrorCode string

// Typed error codes for the API surface.
const (
	ErrorCodeUnknownReplay       ErrorCode = "unknown_replay"
	ErrorCodeUnknownPreparation  ErrorCode = "unknown_preparation"
	ErrorCodeUnknownIntent       ErrorCode = "unknown_intent"
	ErrorCodePreparationNotReady ErrorCode = "preparation_not_ready"
	ErrorCodeProcessNotReady     ErrorCode = "process_not_ready"
	ErrorCodeNoSlotClaimed       ErrorCode = "no_slot_claimed"
	ErrorCodeLobbyFull           ErrorCode = "lobby_full"
	ErrorCodeIntentAlreadyUsed   ErrorCode = "intent_already_used"
	ErrorCodeRevisionMismatch    ErrorCode = "revision_mismatch"
	ErrorCodeGenerationMismatch  ErrorCode = "generation_mismatch"
	ErrorCodeResultAlreadyTaken  ErrorCode = "result_already_accepted"
	ErrorCodeNoResult            ErrorCode = "no_result"
	ErrorCodeUnauthenticated     ErrorCode = "unauthenticated"
	ErrorCodeForbidden           ErrorCode = "forbidden"
	ErrorCodeAllocationFailed    ErrorCode = "allocation_failed"
	ErrorCodeInvalidRequest      ErrorCode = "invalid_request"
	ErrorCodeInternal            ErrorCode = "internal"
)

// apiError maps one error to its typed code and HTTP status.
type apiError struct {
	code   ErrorCode
	status int
}

// errorMap lists every typed error the API surface returns.
var errorMap = []struct {
	target error
	mapped apiError
}{
	{orchestrator.ErrUnknownReplay, apiError{ErrorCodeUnknownReplay, http.StatusNotFound}},
	{orchestrator.ErrUnknownPreparation, apiError{ErrorCodeUnknownPreparation, http.StatusNotFound}},
	{orchestrator.ErrUnknownIntent, apiError{ErrorCodeUnknownIntent, http.StatusNotFound}},
	{core.ErrPreparationNotReady, apiError{ErrorCodePreparationNotReady, http.StatusConflict}},
	{orchestrator.ErrProcessNotReady, apiError{ErrorCodeProcessNotReady, http.StatusConflict}},
	{core.ErrNoSlotClaimed, apiError{ErrorCodeNoSlotClaimed, http.StatusConflict}},
	{core.ErrLobbyFull, apiError{ErrorCodeLobbyFull, http.StatusConflict}},
	{core.ErrIntentAlreadyUsed, apiError{ErrorCodeIntentAlreadyUsed, http.StatusConflict}},
	{core.ErrRevisionMismatch, apiError{ErrorCodeRevisionMismatch, http.StatusConflict}},
	{core.ErrGenerationMismatch, apiError{ErrorCodeGenerationMismatch, http.StatusConflict}},
	{core.ErrResultAlreadyAccepted, apiError{ErrorCodeResultAlreadyTaken, http.StatusConflict}},
	{core.ErrNoResult, apiError{ErrorCodeNoResult, http.StatusNotFound}},
	{orchestrator.ErrUnauthenticated, apiError{ErrorCodeUnauthenticated, http.StatusUnauthorized}},
	{orchestrator.ErrForbidden, apiError{ErrorCodeForbidden, http.StatusForbidden}},
	{orchestrator.ErrNoSteamIdentity, apiError{ErrorCodeInvalidRequest, http.StatusConflict}},
	{core.ErrInvalidAccount, apiError{ErrorCodeInvalidRequest, http.StatusBadRequest}},
	{core.ErrInvalidLobbyCapacity, apiError{ErrorCodeInvalidRequest, http.StatusBadRequest}},
}

// statusForError maps one error to its typed code and HTTP status. An
// AllocationError preserves its typed reason code. Unknown errors map to
// internal with a generic message: error details never leak.
func statusForError(err error) (ErrorCode, int) {
	var allocErr *orchestrator.AllocationError
	if errors.As(err, &allocErr) {
		return ErrorCodeAllocationFailed, http.StatusConflict
	}
	for _, m := range errorMap {
		if errors.Is(err, m.target) {
			return m.mapped.code, m.mapped.status
		}
	}
	return ErrorCodeInternal, http.StatusInternalServerError
}
