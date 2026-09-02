package server

// ProcessState is the lifecycle state of one supervised server process.
type ProcessState int

const (
	// ProcessStateNew means the process was created but not launched.
	ProcessStateNew ProcessState = iota
	// ProcessStateLaunching means the launch command was issued but the
	// process has not signalled anything yet.
	ProcessStateLaunching
	// ProcessStateRunning means the process is running but readiness is not
	// yet proven.
	ProcessStateRunning
	// ProcessStateReady means the process is running and readiness was
	// proven with explicit evidence.
	ProcessStateReady
	// ProcessStateCrashed means the process died unexpectedly. It carries a
	// typed crash reason and no partial state.
	ProcessStateCrashed
	// ProcessStateStopped means the process was stopped deliberately.
	ProcessStateStopped
)

// String returns the stable wire name of the process state.
func (s ProcessState) String() string {
	switch s {
	case ProcessStateLaunching:
		return "launching"
	case ProcessStateRunning:
		return "running"
	case ProcessStateReady:
		return "ready"
	case ProcessStateCrashed:
		return "crashed"
	case ProcessStateStopped:
		return "stopped"
	default:
		return "new"
	}
}

// Terminal returns true when the state is a terminal state.
func (s ProcessState) Terminal() bool {
	return s == ProcessStateCrashed || s == ProcessStateStopped
}
