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
	// ErrUnauthenticated is returned when no authenticated principal is
	// presented to an operation that requires one.
	ErrUnauthenticated = errors.New("orchestrator: unauthenticated")
	// ErrForbidden is returned when the authenticated principal is not the
	// preparation owner or a claimed participant.
	ErrForbidden = errors.New("orchestrator: forbidden")
	// ErrNoSteamIdentity is returned when an operation needs the principal's
	// bound Steam identity and the account has none.
	ErrNoSteamIdentity = errors.New("orchestrator: account has no bound steam identity")
	// ErrWrongProcessGeneration is returned when a server participant tries
	// to consume an intent issued for a different process generation.
	ErrWrongProcessGeneration = errors.New("orchestrator: intent bound to different process generation")
	// ErrUnadmittedAccount is returned when a result is submitted for an
	// account that never consumed an intent on this process generation.
	ErrUnadmittedAccount = errors.New("orchestrator: account not admitted on this process generation")
)
