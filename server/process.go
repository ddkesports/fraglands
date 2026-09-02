package server

import (
	"context"
	"time"
)

// Process is the supervision handle for one server process generation. The
// supervisor owns the state transitions; a worker implementation drives them
// through this interface.
type Process interface {
	// Spec returns the immutable identity of this process.
	Spec() ProcessSpec
	// State returns the current lifecycle state.
	State() ProcessState
	// Readiness returns the recorded readiness fact, or ErrNoReadinessFact.
	Readiness() (ReadinessFact, error)
	// MarkRunning moves the process from Launching to Running.
	MarkRunning() error
	// MarkReady records the readiness fact and moves the process to Ready.
	// A second readiness fact for one generation is refused with
	// ErrReadinessAlreadyRecorded.
	MarkReady(fact ReadinessFact) error
	// MarkCrashed records a crash with a typed reason and moves the process
	// to the terminal Crashed state. It refuses on a terminal process.
	MarkCrashed(reason CrashReason) error
	// MarkStopped moves the process to the terminal Stopped state. It
	// refuses on a terminal process.
	MarkStopped() error
	// WaitTerminal waits until the process reaches a terminal state. It
	// cannot miss the transition.
	WaitTerminal(ctx context.Context) (ProcessState, error)
	// DeliverArtifact records one artifact delivered by the worker.
	// Delivery is only accepted on the bound process handle: there is no
	// supervisor-level delivery by process ID string. It refuses when the
	// process is not accepting artifacts.
	DeliverArtifact(artifact Artifact) error
	// Artifacts returns the artifacts delivered so far.
	Artifacts() []Artifact
}

// CrashReason is one typed reason a process crashed. A crashed process
// carries exactly one reason and never partial state.
type CrashReason struct {
	// Code is the stable typed reason code.
	Code string
	// Message is the human-readable detail.
	Message string
	// DetectedAt is the moment the crash was detected.
	DetectedAt time.Time
}
