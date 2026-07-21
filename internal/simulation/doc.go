// Package simulation is the isolation boundary for Mosaic's interactive
// scenario driver and related orchestration.
//
// # Dependency direction (invariant)
//
// Framework packages are synchronous and timing-blind. They never import any
// path under internal/simulation (or the retired internal/simsession path).
// Simulation packages import framework packages and orchestrate over time.
//
// Framework packages (non-exhaustive; the guard covers all of internal/ except
// this tree):
//
//   - internal/ingestion, internal/eventlog
//   - internal/store
//   - internal/terra, internal/sol, internal/luna
//   - internal/ontology (and gen)
//   - internal/contracts
//   - internal/stream, internal/replay
//   - internal/api (production code)
//   - domain packages under internal/reference/...
//
// All pacing and timing lives only under this tree. The framework has no notion
// of "beat" or "delay" beyond the schedule/session types already defined in
// contracts (SimulationSchedule, ScheduledBeat, SimulationSession, stream
// events). Domain fixture assembly for the frozen demo dataset remains under
// internal/reference/.../simulator (scenario composition, not the interactive
// session controller).
//
// Subpackages:
//
//   - session: domain-agnostic interactive session controller (start/status/
//     end/reset) and schedule-driven beat emission on a session-scoped stream.
//
// Dependency direction is enforced by TestInternalPackagesDoNotImportSimulation
// in this package (deny-by-default over internal/, excluding this tree).
package simulation
