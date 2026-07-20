# Mosaic Hackathon Demo Script

## Positioning

Mosaic is an auditable event-to-state foundation for decision-support tools. The domestic-disturbance call is a synthetic reference implementation, not a police product claim.

The demo must distinguish three things:

- **Runs now:** deterministic synthetic ingestion, state projection/recovery, bounded evidence, historical fixture advisory records, immutable review actions, and the local Docker UI/API.
- **Next interface increment:** an incident-centred workspace, user-triggered analysis presentation, recipient-specific handoff cards, and a provenance/actions tab.
- **Future architecture:** independently operated dispatch and maintenance feeds, durable handoff delivery, and deterministic recurrent-issue awareness. No live connector or external action is claimed in the current demo.

## Opening: one incident, not a systems dashboard

Start with a synthetic 911-style intake:

> A caller reports a domestic disturbance. The call is assigned a synthetic dispatch reference and the incident clock begins.

The main workspace should show the dispatch reference, location, elapsed time, the latest trusted facts, and one clear **Analyze** action. Connection health and API details are supporting developer diagnostics, not the primary user experience.

## Analyze: evidence-backed context

When the operator selects **Analyze**, explain that Mosaic assembles only durable, evidence-resolvable facts:

- the active incident and assigned unit;
- the relevant road and weather conditions;
- availability of supporting resources; and
- the source event and state revision behind every displayed claim.

For the current reference implementation, the advisory history is fixture-composed and intentionally labelled historical/not current after the final road correction. Do not call it a live model response.

## Action cards and human judgment

Present concise, recipient-specific action cards:

1. **Dispatch handoff** — a briefing/note for the dispatch team, with a field for the operator's own observations.
2. **Infrastructure handoff** — a critical-condition note for the maintenance or road-owning team when a bridge or route materially affects the incident.

The operator may acknowledge, reject, annotate, or prepare a handoff. Every interaction becomes an immutable audit record with executed: false in the current demo. The UI must say that it recorded a handoff; it must never claim a real department was contacted.

## Provenance and actions tab

A dedicated tab should make the decision trail legible:

- source event and receipt time;
- canonical event and state revision;
- evidence used by an assessment or handoff;
- the generated/fixture recommendation;
- each operator annotation and decision;
- each recorded recipient, status, and acknowledgement; and
- the explicit boundary between recorded and externally delivered actions.

This is where developers see why Mosaic is useful as a framework: every displayed recommendation has evidence, timing, provenance, and a durable history.

## Recurrent-issue awareness: future, deterministic, and configurable

For a later call in the same configured area, Mosaic can deterministically surface a prior recorded maintenance handoff after a configurable time window. The system should say:

> A prior road-condition handoff exists for this area. A new maintenance note has been prepared for review.

It must not say that it autonomously contacted maintenance. The future implementation may create a pending handoff draft, link it to prior immutable records, and expose it to a separate maintenance-feed instance. An external delivery connector remains a separate, policy-governed capability.

This is not LLM self-healing. It is evidence-backed recurrence awareness and a durable, reviewable workflow.

## Closing

> Mosaic turns an event into a traceable operating picture, lets people make and record judgment calls, and preserves the evidence needed for the next team, the next incident, and the next system instance.

## UI direction

The primary screen becomes an incident command workspace:

1. intake identity, live elapsed counter, and Analyze;
2. current evidence-backed context;
3. findings and action cards, grouped by recipient;
4. an annotation/decision control; and
5. a provenance/actions tab for the full audit trail.

Keep API connection, health, version, stream, and recovery indicators in a compact status drawer or developer view.

## Simulation lifecycle

The primary UI action is **Start simulation**.

1. It creates a new synthetic simulation session and clears only the visible incident workspace.
2. The session replays the declared source events in configured timing order.
3. The UI shows intake, elapsed simulation time, current state, analysis availability, recipient-specific handoffs, and provenance as each beat arrives.
4. The session ends explicitly after the final event and remains reviewable in the provenance/actions tab.
5. A new start creates a new session; it does not truncate or rewrite immutable event, insight, recommendation, handoff, or audit history.

The current startup-only fixture composition is sufficient for Docker proof but is not this interactive lifecycle. Implementing it requires a dedicated synthetic simulation-session API and stream contract, scoped separately from the read/audit API.
