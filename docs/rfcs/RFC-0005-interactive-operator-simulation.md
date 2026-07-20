# RFC-0005: Interactive Operator Simulation and Opt-in Live Models

- **Status:** Proposed
- **Owner:** Mosaic coordinator
- **Decision date:** 2026-07-20
- **Depends on:** RFC-0001, RFC-0002, RFC-0003, RFC-0004

## Decision

Mosaic gains an interactive, operator-driven demo lifecycle on top of the
existing deterministic foundation. Three things are made explicit:

1. **The operator is real; the world is synthetic.** A human operator drives the
   open session — no login, no accounts. Only the incoming call/events,
   environmental data, and the reference scenario are synthetic. The operator
   presses **Analyze**, reviews findings, annotates, and approves or prepares
   recipient handoffs. Operator writes keep the existing open public actor.
2. **Live models are opt-in and server-side.** Luna/Terra/Sol may run against
   real OpenAI models (`gpt-5.6` family) behind their existing structured client
   seams. The live path is opt-in; the deterministic fixture path remains the
   default and the safe fallback. The API key is a server-only runtime secret and
   never enters the browser, Git, image, or committed compose file. Spend is
   bounded by the key's own provider-side limit; the app adds no separate budget
   enforcement.
3. **Recurrence awareness is deterministic and reviewable.** When a later call
   arrives for the same configured area, Mosaic surfaces prior recorded handoffs
   and, after a configurable window, prepares a reviewable note. It never
   autonomously contacts an external party and it is not LLM self-healing.

This increment does not add real operational feeds, real dispatch/maintenance
delivery, autonomous action, login, or identity management. Every operator action
remains an immutable audit record with `executed: false`.

## Operator model

There is no login. The demo is open, and the person using it is the operator: a
human who drives the synthetic session by pressing Analyze, reviewing, annotating,
and recording handoffs. Operator writes use the existing open public actor and
policy defaults (RFC-0002/RFC-0004) unchanged — this increment adds no accounts,
sessions, passwords, PII, or external auth.

## Interactive simulation lifecycle

The primary UI action is **Start simulation**. Its contract:

1. Creates a new synthetic simulation session and clears only the visible
   incident workspace.
2. Replays the profile's declared source events in configured timing order,
   emitting beats on a session-scoped stream.
3. Drives the workspace (intake identity, elapsed counter, current state,
   analysis availability, recipient handoffs, provenance) as each beat arrives.
4. Ends explicitly after the final event; the completed session remains
   reviewable in the provenance/actions tab.
5. A new start creates a new session. It never truncates or rewrites immutable
   event, insight, recommendation, handoff, or audit history.

The session controller is domain-agnostic core; the selected profile supplies
the beat schedule. Timing is configuration, not a hardcoded delay.

## Live model transport

- Luna/Terra/Sol implement their existing structured client seams over the
  OpenAI Responses API. Routing: Luna = lightweight event interpretation,
  Terra = the primary Analyze result, Sol = an explicit operator-triggered deep
  briefing only (never automatic).
- Provider selection is configuration-driven and per-agent. Absent an explicit
  live selection and a present server secret, each agent uses its deterministic
  fixture client. Invalid, refused, or unavailable live output is recorded as a
  ModelRun and produces no state mutation, exactly as today.
- Spend is bounded by the provider-side limit on the supplied key. The app adds
  no separate budget governor. Token usage may be recorded as ModelRun metadata
  for provenance, but it is observability, not enforcement.

## Recurrence awareness

For a later call in the same configured area, Mosaic deterministically surfaces a
prior recorded maintenance/dispatch handoff and, after a configurable window,
prepares a pending, reviewable note linked to the prior immutable records. It
must not imply autonomous external contact, multi-instance delivery, or LLM
self-healing. External delivery remains a separate, policy-governed capability
outside this increment.

## Safety and non-claims

- Actions are **recorded, not sent**. The UI says it recorded a handoff; it never
  claims a real department was contacted.
- Live models **inform**; they never issue an operational action or mutate the
  deterministic projection.
- The API key is server-only; the live path is opt-in; the fixture path is the
  default and fallback.
- Immutable history is append-only; sessions and corrections never rewrite it.

## Acceptance principles

- The deterministic fixture demo continues to pass unchanged with no live secret.
- With a live secret and explicit opt-in, operator-triggered Analyze/briefing run
  against real models; the public read surface and policy defaults are preserved.
- Every operator action is an immutable `executed: false` record; recurrence
  prepares reviewable notes, never autonomous contact.
