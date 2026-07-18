
# Mosaic
# AI Information Fusion Platform
## Story + Technical Specification

**Tagline:** From fragmented signals to operational clarity.

**Core Principle:** AI informs. Humans decide.

---

# Executive Summary

Modern public safety organizations do not suffer from a lack of information—they suffer from fragmented information.

Mosaic continuously ingests operational signals, correlates them using AI, and maintains a live Common Operational Picture (COP) for dispatchers and supervisors. The system never makes operational decisions. Instead, it continuously answers one question:

> Given everything happening right now, what should the human know?

Public safety is the demonstration domain. The architecture is applicable to fire, EMS, disaster response, utilities, airports, and public health.

---

# Product Philosophy

Mosaic is **not**:

- Predictive policing
- Autonomous policing
- Officer replacement
- Surveillance AI

Mosaic **is**:

- Information fusion
- Situational awareness
- Operational intelligence
- Human decision support

Every operational decision remains with a human.

---

# Demonstration Scenario

A dispatcher receives a domestic disturbance call.

As the incident evolves:

- Previous welfare checks are discovered.
- A road becomes blocked.
- Weather deteriorates.
- EMS availability changes.
- A Crisis Intervention Team becomes available.
- Officers submit radio updates.

Mosaic continuously updates the operational picture without requiring prompts.

---

# High-Level Architecture

Operational Systems

→ Ingestion

→ Raw Event Log

→ Luna (Validate + Normalize)

→ Canonical Event Bus

→ Operational State Projector

→ Terra (Information Fusion)

→ Insight Store

→ Sol (Strategic Briefing, when triggered)

→ Dashboard

→ Human Decision + Audit Record

---

# Technology Stack

## Backend

- Go
- Single binary
- Goroutines
- Server-Sent Events

## Frontend

- Svelte
- Vite

## Storage

- SQLite for local and rehearsal demos
- Cloud SQL for PostgreSQL for a hosted Cloud Run demo
- Append-only event and audit records
- In-memory materialized operational state

## Deployment

- Docker for local and recorded demonstrations
- Google Cloud Run for a hosted demonstration, backed by Cloud SQL
- One application instance (`max-instances=1`) for the demonstration

## AI

- OpenAI Responses API for online Luna, Terra, and Sol reasoning
- llama.cpp for offline synthetic-data generation
- `unsloth/gemma-4-E2B-it-GGUF/gemma-4-E2B-it-UD-Q8_K_XL.gguf` as the synthetic-data model artifact

---

# AI Responsibilities

## Luna

Processes raw operational events.

Responsibilities:

- Classification
- Translation
- Entity extraction
- Deduplication
- Summarization
- Normalization

Produces canonical Event objects.

## Terra

Reasons over canonical event deltas and an identified operational-state revision. Terra never rewrites source-derived facts; it produces evidence-backed assessments about the current state.

Responsibilities:

- Correlation assessment
- Pattern detection
- Resource-conflict assessment
- Confidence estimation
- Operational Insights

Terra reasons only over **changes**.

## Sol

Runs only when needed.

Produces:

- Supervisor briefings
- Strategic summaries
- Human-review recommendations

---

# AI Output and Audit Contract

## Output classes

Mosaic distinguishes three displayed claim types:

- **Reported Fact** — a source observation carried by a Canonical Event.
- **Derived Assessment** — an Insight that interprets evidence and includes a confidence value.
- **Human-review Recommendation** — a Sol-generated option for a human to consider.

The dashboard labels these classes distinctly. An assessment or recommendation must never be presented as a confirmed fact.

## Confidence contract

Confidence is an evidence-strength assessment, not a probability that an outcome will occur and never a permission to act autonomously. It is stored as the explicit dimensions `source_quality`, `transformation_certainty`, and `reasoning_support`, each using the ordered band `low`, `medium`, or `high` with a short basis statement. Luna normally supplies the first two dimensions; Terra supplies reasoning support. Sol displays the relevant confidence dimensions but does not invent an outcome probability.

## Luna contract

Luna receives a Raw Event and returns only one schema-valid result: `accepted`, `repaired`, `quarantined`, or `rejected`.

A repaired result must include the original value, the replacement value, the repair method, the evidence, and a confidence score. A quarantined or rejected event never changes operational state; it remains visible for demonstration and audit.

## Terra contract

Terra receives Canonical Event IDs and a bounded structured state slice. Each Insight contains an `insight_id`, `insight_type`, `state_revision`, `summary`, `confidence`, `evidence_refs`, `created_at`, and lifecycle status. Each material assertion must cite at least one Canonical Event or an explicitly identified state fact. When its basis no longer holds, Terra emits an obsolete-insight notice instead of deleting history.

## Sol contract

Sol is invoked only by a supervisor request or a configured material state change, such as a new high-severity Insight or resource-conflict assessment. Its briefing and Recommendations use only structured state, Insights, and Evidence.

Recommendations describe options with neutral verbs such as “consider,” “review,” or “verify.” They cannot dispatch resources, change an incident, contact a person, or use imperative command language. A recommendation is useful only after a human reviews it.

## Model-run record and failure handling

Every Luna, Terra, and Sol invocation records a `model_run_id`, model/provider version, prompt version, schema version, input event IDs, input state revision, output IDs, validation result, and timestamp. The system stores the structured outputs and the evidence references required to explain the display.

All untrusted text, including radio transcripts, is passed to a model as data inside a structured field; it cannot alter the workflow, schema, tool permissions, or policy. An invalid, unsupported, or unavailable model output is rejected, logged, and shown as “no AI assessment available.” The state projector continues from valid Canonical Events without it.

---

# Ontology-First Architecture

The ontology is the product.

Everything else derives from it.

```
internal/

  ontology/

    incident.go
    event.go
    person.go
    unit.go
    location.go
    resource.go
    road.go
    weather.go
    hazard.go
    evidence.go
    insight.go
    recommendation.go
```

Every runtime object is strongly typed.

JSON Schemas are the source of truth.

Generated Go types implement those schemas. The generation artifact is checked in, and CI fails when it is out of date with the schemas.

---

# Canonical Runtime Objects

## Event

An immutable record of a source observation. An Event records what was received or derived; it does not claim that every field is true beyond doubt.

## Canonical Event

An immutable, schema-valid normalized revision of a Raw Event. It is the only event form consumed by the state projector and Terra.

## Incident

Continuously evolving operational object.

## Entity

People, units, vehicles, hospitals, roads, shelters, weather systems.

## Evidence

Every AI output references supporting evidence.

## Insight

Generated by Terra.

Provides operational intelligence.

Never commands.

## Recommendation

Generated by Sol.

Requires human review.

## Operational State

The current Common Operational Picture, materialized in memory from durable canonical events and checkpoints. It is a projection, not the system of record.

---

# Event and State Contract

## Event lifecycle

1. Ingestion assigns a `raw_event_id`, preserves the source payload unchanged, and records `source`, `source_record_id`, `received_at`, and the supplied `occurred_at`.
2. Luna validates the payload, produces a schema-valid Canonical Event, and records its transformation and confidence.
3. The canonical event bus delivers the event to the state projector. The projector updates the affected state and emits a state revision.
4. Terra receives the canonical event plus the affected state revision. It may emit Insights or mark existing Insights obsolete.
5. Sol runs only from structured state, Insights, and evidence when a defined trigger occurs; it may emit a human-review Recommendation.
6. A human action, acknowledgement, or override is recorded as an audit event. Mosaic never executes an operational action.

## Required event fields

Every event record has: `event_id`, `event_type`, `schema_version`, `source`, `source_record_id` when available, `occurred_at`, `received_at`, `payload`, `entity_refs`, `incident_refs`, and `provenance`. For a Raw Event, `event_id` is its `raw_event_id`; a Canonical Event receives a distinct `event_id` and references that `raw_event_id`.

Canonical Events additionally contain `raw_event_id`, `normalization_run_id`, `validation_status`, `confidence`, and `transformation_summary`. Every temporal field is UTC. `occurred_at` means when the source says the event happened; `received_at` means when Mosaic first received it.

Corrections and recoveries never mutate a stored record. Luna emits a new Canonical Event with `supersedes_event_id`, a reason, and its evidence. The state projector applies the newest applicable revision and retains the full chain for replay and audit.

## Processing rules

- **Idempotency:** The same `source` and `source_record_id` is accepted once. Re-delivery returns the original result without changing state.
- **Ordering:** Mosaic does not assume a global event order. It uses source sequence numbers where available; late events are accepted, update the affected projection, and cause Terra to reassess the affected incidents.
- **Deduplication:** Exact source duplicates are ignored idempotently. Suspected semantic duplicates remain separate immutable records and are linked with a confidence-scored `duplicate_of` relation; the link and its basis are visible to the user.
- **Correlation:** Incident and entity links are typed, evidence-backed associations. Luna may propose them; the projection records their confidence and can supersede them when better evidence arrives.
- **Validation:** JSON Schema validation occurs at ingestion, at Canonical Event publication, and at every AI-output boundary. Semantic validation enforces referential integrity, allowed transitions, and domain rules after schema validation.

## Persistence, replay, and deployment

For a local or recorded demo, SQLite is the durable system of record for raw events, canonical events, audit records, and state checkpoints. A Cloud Run container does not provide durable local storage, so a hosted demo uses the same store contract with Cloud SQL for PostgreSQL instead. PostgreSQL is an external managed service, never a process bundled into the Mosaic application container. The in-memory state is rebuilt by loading the latest checkpoint and replaying later canonical events.

The demonstration runs one application instance. Its internal event bus and in-memory projector are intentionally simple. The interfaces must preserve event IDs, revisions, idempotency, and checkpoint/replay semantics so that a later deployment can replace the internal bus and local store with a durable shared broker and state store without changing the ontology or AI contracts.

---

# Delta-Based Reasoning

Terra never regenerates the entire world. It receives a Canonical Event, the specific state revision affected by that event, and the relevant incidents. It produces new Insights, obsolete-Insight notices, correlation assessments, and confidence updates.

The deterministic state projector applies Canonical Events. Terra may assess that state, but it cannot overwrite source-derived facts. Every assessment references the event IDs and state revision that support it.

This reduces token usage, limits the reasoning context, and makes each conclusion reviewable.

---

# Repository Layout

```
cmd/

internal/

  ontology/
  ingestion/
  simulator/
  eventbus/
  state/
  reasoning/
    luna/
    terra/
    sol/
  api/

ui/

datasets/

prompts/

docs/
```

---

# JSON Schema Strategy

The schemas are authored by the Mosaic team.

They are **not** generated by an LLM.

```
ontology/

incident.schema.json
event.schema.json
person.schema.json
unit.schema.json
location.schema.json
resource.schema.json
road.schema.json
weather.schema.json
hazard.schema.json
evidence.schema.json
insight.schema.json
recommendation.schema.json
```

Schemas are:

- Versioned with semantic versions
- Documented with field-level meaning and examples
- Validated at ingress, persistence, and AI-output boundaries
- Converted into checked-in generated Go structs

A schema version is immutable once published. Additive compatible changes increment the minor version; breaking changes require a new major version and an explicit migration or adapter. Semantic validators enforce rules that JSON Schema cannot express, including referential integrity and valid lifecycle transitions.

---

# Synthetic Operational World Model

Terra and Sol never operate on arbitrary text. Luna may receive raw text, but it converts it to a Canonical Event before any reasoning over operational state occurs.

Instead:

- The ontology defines the world.
- Local models generate synthetic data.
- GPT-5.6 reasons over structured operational state.

This cleanly separates:

1. World Definition
2. World Generation
3. World Reasoning

---

# Offline Synthetic Data Generation

The Gemma 4 E2B GGUF model runs locally through llama.cpp and is responsible only for generating synthetic data. The selected artifact is `unsloth/gemma-4-E2B-it-GGUF/gemma-4-E2B-it-UD-Q8_K_XL.gguf`.

It is an offline generator, not part of the online Mosaic application image or the Luna, Terra, and Sol runtime path. It never designs schemas.

It receives:

- JSON Schemas
- Generation instructions
- Dataset targets

Gemma must:

- Produce JSON only
- Never invent fields
- Conform exactly to the supplied schemas
- Generate synthetic names and locations
- Use only the deterministic ID allocation map supplied by the Prompt Builder
- Preserve referential integrity against that map

The Prompt Builder records the model version, prompt version, seed where supported, and ID allocation map. The validator rejects any generated record that uses an unknown ID, breaks a reference, or fails a schema. Accepted datasets are versioned artifacts; Mosaic does not treat repeated model inference as deterministic.

---

# Dataset Layout

```
datasets/

city.json
units.json
people.json
roads.json
locations.json
hospitals.json
weather.json

incident_templates/

event_templates/

scenarios/
```

---

# Synthetic Data Pipeline

```
JSON Schemas

↓

Prompt Builder

↓

llama.cpp + Gemma 4 E2B GGUF

↓

Generated JSON

↓

JSON Schema Validator

↓

Accepted Dataset
```

---

# Scenario Engine

Replayable scenarios drive demonstrations.

Example:

```
t=0   911 Call

t=8   Previous welfare check

t=14  Weather alert

t=22  Road closure

t=35  EMS available

t=48  Officer radio update
```

The simulator publishes Raw Events to Ingestion, which records them before Luna produces Canonical Events.

The reasoning engine updates the operational picture.

---

# Guiding Principle

Mosaic separates four concerns:

- **Ontology** — Human-authored JSON Schemas and Go types.
- **World Generation** — Offline synthetic data generation using the Gemma 4 E2B GGUF model through llama.cpp.
- **World Reasoning** — Online information fusion using GPT-5.6.

The data lifecycle, validation, state projection, and replay process are deterministic and explainable. Model inference remains probabilistic, so Mosaic records the exact inputs, configuration, and accepted outputs needed for an auditable replay.


---

# Adaptive Reasoning

Mosaic is designed to continuously improve operational reasoning while remaining fully human-centered.

The platform does **not** retrain foundation models during runtime.

Instead, it captures operational experience and incorporates it into future reasoning.

This provides explainable adaptation without changing model weights.

---

# Self-Healing

Luna continuously validates and repairs incoming operational events before they reach the reasoning engine.

Typical recovery operations include:

- Recover missing timestamps
- Resolve incomplete locations
- Merge duplicate reports
- Normalize terminology
- Repair malformed payloads
- Flag confidence when automatic recovery is uncertain

Every repaired event preserves provenance and records what was changed.

Example:

```
Incoming Event
    ↓
Validation
    ↓
Automatic Recovery
    ↓
Confidence Scoring
    ↓
Canonical Event
```

The dashboard can surface these recoveries in real time, making the system's resilience visible during demonstrations.

---

# Adaptive Experience Layer

After every incident, Mosaic records:

- AI insights
- AI recommendations
- Human decisions
- Operational outcomes
- Resolution metrics

These become structured operational experience.

Example:

```json
{
  "incident_id": "inc_102",
  "recommendation": "Consider Crisis Intervention Team",
  "human_action": "EMS dispatched",
  "outcome": "Peaceful resolution",
  "resolution_time_minutes": 18,
  "accepted": false
}
```

This data is stored in an Experience Store.

No model weights are modified.

---

# Experience Store

```
Operational State
        ↓
Human Decision
        ↓
Outcome Recorder
        ↓
Experience Store
        ↓
Future Reasoning
```

Terra and Sol may retrieve similar historical operational experiences to provide additional context during reasoning.

Examples:

- Similar incidents
- Successful response patterns
- Resource utilization
- Resolution timelines

The system presents these as supporting evidence rather than instructions.

---

# Explainable Adaptation

Future reasoning may include statements such as:

> Similar to 17 previous incidents.

> Previous peaceful resolutions frequently involved Crisis Intervention Teams.

These are descriptive observations derived from operational experience.

They are never mandatory recommendations.

## Experience retrieval rules

The demonstration Experience Store contains synthetic, completed scenarios only. An experience record is eligible for retrieval only when it has an outcome, an evidence bundle, a schema version, and a defined retention status.

A retrieved comparison must identify the matching criteria, the eligible cohort, the record count, and the supporting experience IDs. It may describe an observed association, but it must not claim that a historical action caused an outcome, score people, or predict individual risk. Experience retrieval supplies context to Terra and Sol; it cannot change canonical facts or take an action.

---

# Demonstration Governance and Acceptance

The demonstration uses only synthetic data and has no connection to dispatch, records-management, or communications systems. It has two simple roles: a viewer who can inspect the operational picture and a supervisor who can request a Sol briefing and record a decision. Every request, displayed Recommendation, acknowledgement, rejection, and override becomes an audit record.

Before a scenario is accepted for the demo, it must show that:

- every displayed Insight and Recommendation resolves to valid evidence references;
- an invalid or quarantined event cannot change operational state;
- an obsolete Insight is visibly superseded rather than silently removed;
- a restart rebuilds the same state from the stored checkpoint and Canonical Event history;
- a supervisor action is attributable in the audit trail; and
- replay reproduces deterministic validation and state-projection results from recorded accepted artifacts.

Latency and throughput are observed and recorded during the demo. They are not release gates until the initial implementation provides a realistic baseline.

---

# Architectural Separation

Mosaic intentionally separates four concerns.

## 1. World Definition

Human-authored ontology and JSON Schemas.

## 2. World Generation

Offline synthetic data generation using the Gemma 4 E2B GGUF model through llama.cpp.

## 3. Runtime Reasoning

GPT-5.6 agents (Luna, Terra, Sol) perform continuous information fusion.

## 4. Adaptive Experience

Structured outcomes from completed incidents are recorded and reused to improve future reasoning.

This architecture enables continuous operational improvement without retraining foundation models. Its evidence, validation, state-projection, and audit process is replayable; repeated LLM calls are not assumed to produce identical outputs.

