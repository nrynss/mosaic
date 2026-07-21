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
// # BeatExecutor and pacing (C2)
//
// BeatExecutor owns Append-path pacing: for each schedule beat it loads a
// frozen raw payload via an injectable BeatSource, Appends an eventlog
// envelope, and waits according to equal spacing (MOSAIC_SIM_BEAT_SPACING,
// default 2.5s). Fixture scenario delay_ms is presentation metadata and is
// ignored under the default interactive rule; UseScheduleDelays keeps the
// historical relative-to-start model for tests. Burst mode (MOSAIC_SIM_BURST)
// appends with zero inter-beat delay for EventLog stress.
//
// Envelope formation:
//
//   - PartitionKey: ExecutorConfig.PartitionKey, or DefaultPartitionKey
//     ("simulation") when empty. Composition should set the incident id.
//   - IdempotencyKey: raw_event_id (stable source identity; at-least-once safe).
//   - Type: EventTypeRawEvent ("raw.event").
//   - Payload: opaque bytes from BeatSource (serialized raw event).
//
// BeatExecutor does not run Luna/ingestion or advance the COP; a consumer/
// projector on the B3/B5 path does. Terra/Sol advisory at rev 7/9 is C4/C5.
//
// Subpackages:
//
//   - session: domain-agnostic interactive session controller (start/status/
//     end/reset) and schedule-driven SSE beat emission. Optional BeatSpacing
//     enables equal-spacing presentation; default remains schedule Delay for
//     backward-compatible tests.
//
// Dependency direction is enforced by TestInternalPackagesDoNotImportSimulation
// in this package (deny-by-default over internal/, excluding this tree).
package simulation
