package server

import "errors"

var (
	// ErrInvalidSpec is returned when a ProcessSpec is incomplete.
	ErrInvalidSpec = errors.New("server: invalid process spec")
	// ErrPortInUse is returned when the requested port is already allocated.
	ErrPortInUse = errors.New("server: port already in use")
	// ErrSpoolInUse is returned when the requested spool is already allocated.
	ErrSpoolInUse = errors.New("server: spool already in use")
	// ErrNotRunning is returned when an operation requires a running process.
	ErrNotRunning = errors.New("server: process not running")
	// ErrNotReady is returned when an operation requires proven readiness.
	ErrNotReady = errors.New("server: process not ready")
	// ErrCrashed is returned when an operation requires a process that did
	// not crash.
	ErrCrashed = errors.New("server: process crashed")
	// ErrAlreadyStopped is returned when a stop is attempted on a terminal
	// process.
	ErrAlreadyStopped = errors.New("server: process already stopped")
	// ErrNoReadinessFact is returned when readiness was never proven.
	ErrNoReadinessFact = errors.New("server: no readiness fact recorded")
	// ErrArtifactNotDelivered is returned when an artifact was not delivered
	// by the worker.
	ErrArtifactNotDelivered = errors.New("server: artifact not delivered")
	// ErrUnknownProcess is returned when a process ID does not exist.
	ErrUnknownProcess = errors.New("server: unknown process")
)
