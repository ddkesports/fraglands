// Package server defines the worker process supervision contract for
// Fraglands server participants. It is the server-side seam between the
// orchestrator and a real host: the orchestrator asks for a server process
// generation, and a worker implementation delivers it.
//
// The contract covers exactly five things:
//
//   - process identity: every process carries a unique generation, a port,
//     and a spool directory (see ProcessSpec);
//   - supervision: the Supervisor launches, monitors, and stops worker
//     processes through the ProcessLauncher interface;
//   - readiness: a process is never assumed ready; readiness is an explicit
//     fact with evidence recorded by the worker (see ReadinessFact);
//   - artifact delivery: the worker delivers artifacts explicitly; the
//     supervisor never invents or fabricates them (see ArtifactDelivery);
//   - crash-stop: a crashed process moves to the Crashed state with a typed
//     reason and no partial state; it never auto-restarts.
//
// This package is deliberately free of Steam, real host processes, and any
// game-state interpretation. It is pure contract plus fake-process tests.
package server
