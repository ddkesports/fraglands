package server

import (
	"context"
	"fmt"
	"sync"
)

// Supervisor owns the set of server process generations for one host. It
// allocates isolated specs (unique generation, port, and spool per process),
// launches processes through the injected launcher, and records crash-stop
// transitions. It is safe for concurrent use.
type Supervisor struct {
	// launcher starts each process.
	launcher ProcessLauncher

	// mtx guards the maps and counters below.
	mtx sync.Mutex
	// nextGeneration is the next generation number to allocate.
	nextGeneration uint64
	// basePort is the first port in the allocation range.
	basePort int
	// spoolRoot is the root directory under which spool dirs are allocated.
	spoolRoot string
	// processes maps process ID to its handle.
	processes map[string]Process
	// usedPorts records ports currently allocated to live processes.
	usedPorts map[int]bool
	// usedSpools records spool dirs currently allocated to live processes.
	usedSpools map[string]bool
}

// NewSupervisor constructs a supervisor that allocates generations starting
// at 1, ports starting at basePort, and spool dirs under spoolRoot.
func NewSupervisor(launcher ProcessLauncher, basePort int, spoolRoot string) (*Supervisor, error) {
	if launcher == nil {
		return nil, fmt.Errorf("%w: launcher is required", ErrInvalidSpec)
	}
	if basePort <= 0 || basePort > 65535 {
		return nil, fmt.Errorf("%w: base port %d out of range", ErrInvalidSpec, basePort)
	}
	if spoolRoot == "" {
		return nil, fmt.Errorf("%w: spool root is required", ErrInvalidSpec)
	}
	return &Supervisor{
		launcher:       launcher,
		nextGeneration: 1,
		basePort:       basePort,
		spoolRoot:      spoolRoot,
		processes:      make(map[string]Process),
		usedPorts:      make(map[int]bool),
		usedSpools:     make(map[string]bool),
	}, nil
}

// Start launches one new server process generation with an isolated spec:
// a unique generation, a dedicated port, and a dedicated spool directory.
// The returned process is in the Launching state; readiness is proven
// separately by the worker.
func (s *Supervisor) Start(ctx context.Context) (Process, error) {
	s.mtx.Lock()
	spec, id, err := s.allocateSpecLocked()
	if err != nil {
		s.mtx.Unlock()
		return nil, err
	}
	s.mtx.Unlock()

	proc, err := s.launcher.Launch(ctx, spec)
	if err != nil {
		// Release the allocation: the process never came up.
		s.mtx.Lock()
		delete(s.usedPorts, spec.Port)
		delete(s.usedSpools, spec.SpoolDir)
		delete(s.processes, id)
		s.mtx.Unlock()
		return nil, err
	}
	return proc, nil
}

// allocateSpecLocked builds the next isolated spec and reserves its port and
// spool. Callers must hold s.mtx.
func (s *Supervisor) allocateSpecLocked() (ProcessSpec, string, error) {
	gen := s.nextGeneration
	s.nextGeneration++

	port := s.basePort + int(gen) - 1
	if s.usedPorts[port] {
		return ProcessSpec{}, "", fmt.Errorf("%w: port %d", ErrPortInUse, port)
	}
	spool := fmt.Sprintf("%s/gen-%d", s.spoolRoot, gen)
	if s.usedSpools[spool] {
		return ProcessSpec{}, "", fmt.Errorf("%w: spool %s", ErrSpoolInUse, spool)
	}

	spec := ProcessSpec{Generation: gen, Port: port, SpoolDir: spool}
	if err := spec.Validate(); err != nil {
		return ProcessSpec{}, "", err
	}

	id := fmt.Sprintf("proc-%d", gen)
	s.processes[id] = nil // reserve the ID slot
	s.usedPorts[port] = true
	s.usedSpools[spool] = true
	return spec, id, nil
}

// Get returns the process handle for one ID, or ErrUnknownProcess.
func (s *Supervisor) Get(id string) (Process, error) {
	s.mtx.Lock()
	defer s.mtx.Unlock()
	proc, ok := s.processes[id]
	if !ok || proc == nil {
		return nil, ErrUnknownProcess
	}
	return proc, nil
}

// Live returns the number of processes in a non-terminal state.
func (s *Supervisor) Live() int {
	s.mtx.Lock()
	defer s.mtx.Unlock()
	count := 0
	for _, proc := range s.processes {
		if proc != nil && !proc.State().Terminal() {
			count++
		}
	}
	return count
}
