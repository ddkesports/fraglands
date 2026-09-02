package orchestrator

import "errors"

var (
	// ErrUnknownReplay is returned when a preparation is requested for a
	// replay that is not in the selection catalog.
	ErrUnknownReplay = errors.New("orchestrator: replay not in selection catalog")
	// ErrUnknownPreparation is returned when a preparation ID does not exist.
	ErrUnknownPreparation = errors.New("orchestrator: unknown preparation")
	// ErrUnknownIntent is returned when a join intent ID does not exist.
	ErrUnknownIntent = errors.New("orchestrator: unknown join intent")
	// ErrProcessNotReady is returned when a join intent is requested before
	// the server process readiness is proven, or after its allocation failed.
	ErrProcessNotReady = errors.New("orchestrator: server process not ready")
)
