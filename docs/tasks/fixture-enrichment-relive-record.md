# Task: Enrich fixture raw events so live Luna normalizes (not quarantines), then re-record

**Goal:** Make the fixture raw events carry the structured identifiers a realistic
intake source would provide, so **live Luna echoes them and accepts** instead of
quarantining for lack of an `incident_id`/`location_id`. Then re-record the demo
cassette bank. Keep exactly one beat (invalid input) quarantined as the honest
failure case.

**Branch:** `feat/v0.4-pluggable-event-spine`  
**Status:** ✅ **Implemented** (2026-07-21)  
**Implementation commit:** `3eea527`  
**Depends on:** demo cassette recorder (`1bb0974`) and the live path
(`6c2ad38`, `660a347`). See
[demo-cassette-recorder.md](../runbook/demo-cassette-recorder.md) and
[demo-recorder-script.md](demo-recorder-script.md).

---

## Implementation record (landed)

| Decision / artifact | What shipped |
|---------------------|--------------|
| Placement | **(A) `attributes` only** — free-text `payload_bytes_b64` / `raw_sha256` / `content_type` unchanged |
| Identifiers | Exact literals from `expected-outcomes.json` (plus `event_type`, and `entity_kind: resource` on EMS beats 5/9) |
| Beat 8 | Left bare; remains the only live quarantine |
| Luna prompt | Minimal clarify in `prompts/luna/v1.0.0.md`: attributes are **authoritative intake IDs to echo**; still forbids fabricating missing IDs. Hash `3f943e46…` (was `c08e5460…`); `promptEvalHashes` updated |
| Manifest | Beats 1, 2, 6, 7 default `ok`; only beat 8 `expected_status: quarantined` |
| StubLuna | Quarantine set = only `raw-domestic-008-invalid-input` |
| Live re-record | `TestDemoCastRecordLiveE2E` ~**117s** PASS; **12** cassettes; Luna keys rehashed after attribute change |
| No-live verify | `TestDemoCastReplayNoLiveE2E` PASS ×2 (no key); offline DemoCast green |
| Runbook | §2a replaced with post-enrichment run + historical note of bare-fixture first pass |

**Live Luna outcomes (banked):**

| Beat | status | `event_type` / notes |
|------|--------|----------------------|
| 1–2 | accepted → ok | `incident_reported`; `incident-domestic-001`, `location-cedar-lane-014` |
| 3 | accepted → ok | `weather_alert_issued`; `weather-heavy-rain-001` |
| 4 | accepted → ok | `road_status_changed`; `road-main-street-bridge` |
| 5 | accepted → ok | **`resource_status_changed`**; `resource-ems-004` available |
| 6 | accepted → ok | `unit_status_changed`; `unit-017` |
| 7 | accepted → ok | `road_status_changed`; `road-brook-lane` (enriched; live accepts, does not “repair”) |
| 8 | **quarantined** | bare invalid JSON; honest failure case |
| 9 | accepted → ok | **`resource_status_changed`**; `resource-ems-004` unavailable |
| 10 | accepted → ok | `road_status_changed`; `road-brook-lane` open |
| Terra / Sol | ok | rev9 keys unchanged |

**Residual notes (not open work):**

- Beat 7 free text still says the road id was omitted while attributes supply `road_id` — intentional for live accept; the deterministic fixture pipeline’s “repaired” story is unchanged.
- Next live re-record can still drift wording/`canonical_seq`; CI is pinned to this bank.
- Option (B) structured JSON payloads remains a possible future realism upgrade.

---

## 1. Why (root cause — read before "fixing")

The first live run banked **5 of 10 Luna beats as `quarantined`**, including beat 1
(the central 911 incident). Luna's reason (banked verbatim):

> "The payload reports a domestic disturbance and a street address but does not
> supply the required `incident_id` or `location_id` for an incident_reported event."

This is **not a Luna bug — it is Luna behaving correctly.** An LLM must not fabricate
durable record identifiers. The problem is the **input data**:

- Raw events carry only free text + a demo label, e.g. beat 1:
  `payload = "911 caller reports a verbal domestic disturbance at 14 Cedar Lane."`,
  `attributes = {"story_beat": "911 call"}`.
- The canonical IDs (`incident-domestic-001`, `location-cedar-lane-014`, …) are minted
  by the deterministic fixture pipeline
  ([internal/reference/domesticdisturbance/statefacts.go](../../internal/reference/domesticdisturbance/statefacts.go))
  and are **never present in the raw event Luna sees**.
- The raw-event schema already has a home for structured intake metadata:
  `attributes` is `additionalProperties: true`
  ([ontology/raw-event.schema.json](../../ontology/raw-event.schema.json)).

**In a realistic feed** (CAD / telephony / sensor), the intake system assigns the
incident id and geocodes the address *before* normalization, so those IDs ride in the
envelope and Luna echoes them. The bare fixture is the unrealistic case.

### Non-negotiable: do NOT fix this by loosening Luna
Do **not** edit `prompts/luna/v1.0.0.md` to let Luna invent IDs. Quarantine-on-
missing-identifier is the safety boundary and a demo selling point. The fix is in the
**data layer** only (plus, if needed, telling Luna the structured fields are
authoritative — see §3).

---

## 2. The enrichment (authoritative mapping)

`datasets/domestic-disturbance/expected-outcomes.json` is the ground truth: it already
declares each beat's canonical `event_type` and payload IDs. Enrich each raw event so
Luna has exactly those identifiers. Use the **exact IDs the COP uses** so live-
normalized canonical events reconcile with the board.

| Raw event | Expected canonical `event_type` | Identifiers to make available |
|-----------|-------------------------------|-------------------------------|
| raw-domestic-001-call | incident_reported | incident_id, category, location_id (+summary) |
| raw-domestic-002-welfare | incident_reported | incident_id, category, location_id (+summary) |
| raw-domestic-003-weather | weather_alert_issued | weather_alert_id, status, severity |
| raw-domestic-004-main-road | road_status_changed | road_id, status, reason |
| raw-domestic-005-ems-available | **resource_status_changed** | resource_id, availability, incident_id |
| raw-domestic-006-officer-update | unit_status_changed | unit_id, availability, incident_id |
| raw-domestic-007-incomplete-road | road_status_changed | road_id, status, reason |
| **raw-domestic-008-invalid-input** | **(none — keep quarantined)** | leave bare; this is the honest failure case |
| raw-domestic-009-late-ems | **resource_status_changed** | resource_id, availability, incident_id |
| raw-domestic-010-road-correction | road_status_changed | road_id, status, reason |

Pull the exact literal IDs/values from `expected-outcomes.json` (do not invent). This
enrichment **also fixes the misclassification** seen in the first live run: beats 5 and
9 (EMS) came back as `unit_status_changed`, but EMS is a **resource** — the correct
`event_type` is `resource_status_changed`. Giving Luna `resource_id`/`entity_kind`
removes the ambiguity.

---

## 3. Where to put the identifiers (design decision — pick one, verify the prompt)

The identifiers must land where Luna treats them as **authoritative**. Two options:

- **(A) `attributes` metadata (less invasive, recommended first).** Keep the free-text
  payload (preserves the "Luna normalizes messy input" story) and add the IDs under
  `attributes`, e.g. `attributes: { story_beat, incident_id, location_id, category }`.
  The interpret path already marshals `Attributes` into the `RawEventJSON` the model
  sees ([internal/api/operator.go](../../internal/api/operator.go) `handleOperatorInterpret`).
  **Verify** `prompts/luna/v1.0.0.md` treats `attributes` as trusted intake metadata,
  not as untrusted text. If it currently anchors only on the payload, adjust the prompt
  minimally — but note: **any prompt edit changes its hash**, which touches the
  prompt-eval baselines (`promptEvalHashes` in
  [internal/openaimodel/prompt_eval_test.go](../../internal/openaimodel/prompt_eval_test.go))
  and requires a re-record.
- **(B) Structured JSON payload (most realistic).** Change each raw event to
  `content_type: application/json` with a structured record (the IDs + a `summary`
  text field), like beat 8 already is. Closest to a real CAD record; no reliance on
  attributes semantics. Larger diff and changes the fixture bytes broadly.

Recommend **(A)** for the demo timeline; note **(B)** as the more realistic follow-up.

**Chosen and implemented:** **(A)**. Prompt was adjusted only for attribute
authority (echo, don’t invent); anti-fabrication rule kept. Option (B) not taken.

---

## 4. Re-record (this changes the committed bank)

Enriching the inputs changes Luna outcomes, so:

1. Update `attributes` (or payloads) in `datasets/domestic-disturbance/raw-events.json`
   per §2/§3. **Do not** change `raw_event_id`s.
2. Update the manifest `expected_status`
   ([testdata/demo/recording-manifest.json](../../testdata/demo/recording-manifest.json)):
   beats 1, 2, 6, 7 flip `quarantined → ok`; **beat 8 stays `quarantined`**.
3. Update the offline `StubLuna` quarantine set to match (so the offline identity loop
   stays green) — see `internal/democast/stubs.go`.
4. Re-run the **offline** identity loop (no live) until green.
5. Run the **one** gated live pass (`MOSAIC_RECORD_LIVE=1` + key) to refresh the
   committed bank under `testdata/demo/cassettes/`.
6. Confirm the no-live replay verifier (`TestDemoCastReplayNoLiveE2E`) passes against
   the new bank with no key.
7. Update the runbook live-run table
   ([demo-cassette-recorder.md](../runbook/demo-cassette-recorder.md) §2a).

**Cost:** one live pass, ~12 calls, cents.

---

## 5. Acceptance criteria

- [x] `raw-events.json` enriched with exact `expected-outcomes.json` identifiers; only
      beat 8 left bare. (`3eea527`)
- [x] Live re-record: beats 1–7, 9, 10 bank `accepted`/`ok`; beat 8 `quarantined`.
      (~117s gated live pass; bank under `testdata/demo/cassettes/`)
- [x] EMS beats (5, 9) normalize as `resource_status_changed` (misclassification fixed).
- [x] Live-normalized canonical events use the **same IDs as the COP** (reconcilable).
- [x] `prompts/luna/v1.0.0.md` NOT loosened to fabricate IDs; attribute-trust clarify +
      `promptEvalHashes` updated in the same change (`3f943e46…`).
- [x] Offline identity loop + no-live replay verifier both green; manifest
      `expected_status` matches banked outcomes.
- [x] Runbook §2a updated with the new run.
- [x] Authored `ontology/*` schemas unchanged.

---

## 6. Non-goals
- No loosening of Luna's anti-fabrication behavior.
- Beat 8 stays quarantined (proof the boundary is real).
- No change to the deterministic fixture/domain pipeline that drives the board.
