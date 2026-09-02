package server

import "fmt"

// ProcessSpec is the immutable identity of one server process generation. It
// carries everything needed to isolate one process from every other process
// on the same host: a unique generation, a dedicated port, and a dedicated
// spool directory. Two live processes must never share any of these.
type ProcessSpec struct {
	// Generation is the monotonically increasing server process generation.
	Generation uint64
	// Port is the dedicated network port for this process.
	Port int
	// SpoolDir is the dedicated spool directory for this process. It is
	// isolated from every other process spool.
	SpoolDir string
}

// Validate returns an error when the spec cannot identify an isolated
// process.
func (s ProcessSpec) Validate() error {
	if s.Generation == 0 {
		return fmt.Errorf("%w: generation must be non-zero", ErrInvalidSpec)
	}
	if s.Port <= 0 || s.Port > 65535 {
		return fmt.Errorf("%w: port %d out of range", ErrInvalidSpec, s.Port)
	}
	if s.SpoolDir == "" {
		return fmt.Errorf("%w: spool dir is required", ErrInvalidSpec)
	}
	return nil
}
