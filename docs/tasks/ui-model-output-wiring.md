# Task: Wire live/replayed model output into the demo UI

**Goal:** Add UI affordances that call the operator model endpoints
(`/operator/interpret` Luna, `/operator/analyze` Terra, `/operator/brief` Sol) and
render the returned model output **honestly** — real when live, banked when replay,
declined when fixture — without ever mutating the board. This is what lets the demo
*show* the real Terra/Sol/Luna output that the cassette recorder banked.

**Branch:** `feat/v0.4-pluggable-event-spine`
**Unblocks:** the replay-backed Playwright flow (see
[playwright-demo-e2e.md](playwright-demo-e2e.md) §6.4).
**Depends on:** cassette recorder (`1bb0974`) + committed bank `testdata/demo/cassettes/`.
**Pairs with:** [fixture-enrichment-relive-record.md](fixture-enrichment-relive-record.md)
(after enrichment, the banked Luna output is mostly `accepted` instead of quarantined —
better to show).

---

## 0. Current state (verified)

- The UI **never** calls the model endpoints today. "Refresh advice" just re-fetches
  `GET /api/v1/advisories` ([ui/src/lib/IncidentWorkspace.svelte](../../ui/src/lib/IncidentWorkspace.svelte)).
  The board is driven entirely by the fixture/replay Play pipeline.
- There is an established operator-POST pattern to reuse:
  `readEnvelope('operator/…', { method:'POST', headers:{'Content-Type':'application/json'},
  body: JSON.stringify({...}) })` — see
  [ui/src/lib/ActionCards.svelte](../../ui/src/lib/ActionCards.svelte) (approve / annotate
  / prepare-handoff). `apiURL`, `headersFor`, `readEnvelope` live in
  [ui/src/App.svelte](../../ui/src/App.svelte); `headersFor()` already injects the
  `X-Mosaic-Demo-Identity` header (so Sol's supervisor requirement should already be met —
  verify).
- Mode badges exist ([ui/src/lib/ModelModeIndicator.svelte](../../ui/src/lib/ModelModeIndicator.svelte)) —
  reuse them to label output provenance (fixture / live / replay).

---

## 1. The determinism crux (design driver — read first)

Replay hits require **byte-identical requests** to what was banked (Luna keys on exact
`RawEventJSON` bytes; Sol/Terra on exact evidence `{kind,id,pointer,explanation}` +
insight ids + COP revision). **Therefore the demo's model actions must issue the exact
requests the recorder banked** — free-form operator input (typed evidence explanations,
ad-hoc raw events) will miss in replay.

**Single source of truth = the recording manifest**
([testdata/demo/recording-manifest.json](../../testdata/demo/recording-manifest.json)).
The UI must issue the manifest's operator payloads verbatim. Two ways to guarantee that:

- **(A) Serve the manifest to the UI (recommended).** Add a small read-only endpoint,
  e.g. `GET /api/v1/demo/interactions`, that returns the manifest's operator steps
  (kind + payload only; no secrets). The UI renders one action per step and POSTs that
  exact payload to the real operator endpoint. No drift; UI issues *real* API calls;
  guaranteed replay hits. Reuse `internal/democast` manifest loading.
- **(B) Mirror the payloads in the UI.** A UI-side constant duplicating the manifest.
  Simpler, but drifts from the manifest — avoid.

Recommend **(A)**.

---

## 2. Mode behavior — one coherent story across all three

The affordances behave correctly in every mode with no special-casing beyond labeling:

| Mode | What the model call does | UI shows |
|------|--------------------------|----------|
| `fixture` (default/demo-safe) | interactive Terra/Sol return the deterministic **refuse** client; Luna fixture | "fixture declines interactive assessment" — honest, `provider: mosaic-fixture` |
| `replay` (**the demo**) | served from the committed bank, **no network, no key** | **real banked** insight/recommendation/LunaResult, `provider: mosaic-fixture` (replay), `$0` |
| `live` (deliberate) | real OpenAI call | real fresh output, `provider: openai` |

The same buttons work in all three; only the provenance badge changes. Default the demo
to **replay** so the audience sees real model output offline at `$0`.

---

## 3. Affordances to add

Follow the ActionCards POST pattern; render results in the incident workspace.

1. **Terra — "Generate assessment"** → `POST /operator/analyze` with the manifest's
   evidence + note. Render the returned `Insight` (assertions, confidence, evidence),
   or the honest terminal status (`refused` / `invalid` / `quarantined` / `error`).
2. **Sol — "Request briefing"** (supervisor) → `POST /operator/brief` with the
   manifest's insight ids + evidence. Render the `Recommendation` text + evidence, or
   status. Requires the supervisor identity header (already sent by `headersFor()`).
3. **Luna — "Interpret event"** → `POST /operator/interpret` with a beat's exact raw
   event (from the manifest / `internal/democast/raw_events.go`). Render the
   `LunaResult` status (`accepted`/`repaired`/`quarantined`) and, when present, the
   canonical event (`event_type`, payload). **Show quarantines proudly** — e.g. beat 8,
   and (pre-enrichment) the incident beats — with Luna's `reason`. That is the
   anti-fabrication selling point.

Attach Luna "Interpret" to individual beats/events (the manifest defines the set); Terra
and Sol as workspace-level actions at COP rev 9.

---

## 4. Honest rendering + boundary (non-negotiable)

Every model response is `executed: false` and appends an audit; the board **must not
change** from these actions (agents propose, never dispose). The UI must:

- Show the **provenance badge** (fixture / live / replay + model id) on each result so
  the audience knows whether it's real, replayed, or declined.
- Render non-`ok` outcomes truthfully (refusal detail, quarantine `reason`, failure) —
  never hide them or fake success.
- Reinforce the boundary: a visible "proposed, not applied — board unchanged" affordance;
  the action also lands in Decision history as `executed:false` (existing behavior).
- Never send secrets or the API key from the browser (the key is server-only).

---

## 5. Selectors for Playwright

Add `data-testid` on the new controls and result regions so the replay-backed Playwright
flow ([playwright-demo-e2e.md](playwright-demo-e2e.md)) can assert real banked text
renders:
`generate-assessment`, `request-briefing`, `interpret-event`, `model-result-card`,
`model-result-status`, `model-provenance-badge`, `luna-quarantine-reason`.

---

## 6. Decisions to confirm
1. **Manifest delivery:** endpoint (A) vs UI mirror (B). Recommend **A** (`GET
   /api/v1/demo/interactions`).
2. **Default demo mode:** replay (recommended) — audience sees real output, `$0`, offline.
3. **Luna scope in UI:** interpret on all 10 beats vs a curated few (incident + a couple
   status changes). Recommend a curated few for narrative flow, with the rest available.
4. **Sequencing vs enrichment:** this parcel works today (shows current bank, incl.
   quarantines), but reads best **after**
   [fixture-enrichment-relive-record.md](fixture-enrichment-relive-record.md) so most
   Luna beats are `accepted`. Either order is fine; note the dependency.

---

## 7. Acceptance criteria
- [ ] Terra/Sol/Luna affordances added, reusing the `readEnvelope` operator-POST pattern.
- [ ] Requests are byte-identical to the manifest (via endpoint A or an equivalent
      guarantee); **replay mode hits the bank with no key, no network** and renders real
      banked output.
- [ ] All three modes render coherently with an honest provenance badge (fixture declines /
      live real / replay banked).
- [ ] Non-`ok` outcomes (refusal / quarantine + `reason` / failure) render truthfully;
      quarantine reason is visible.
- [ ] Board is provably unchanged by any model action; action appears in Decision history
      as `executed:false`.
- [ ] No secret/API key ever leaves the server; browser sends none.
- [ ] `data-testid`s added (§5) for the Playwright replay flow.
- [ ] Works with the committed bank in CI-style replay (no key).

---

## 8. Non-goals
- No board mutation from model output (hard boundary).
- No browser-side API key or live-by-default (demo defaults to replay).
- No free-form operator model input in the demo path (would miss replay); free-form is a
  live-only power-user concern, out of scope here.

---

## 9. Files & symbols index
| Area | Path |
|---|---|
| Operator endpoints | [internal/api/operator.go](../../internal/api/operator.go) — `handleOperatorAnalyze/Brief/Interpret`, `hydrateOperatorInsights` |
| UI fetch helpers | [ui/src/App.svelte](../../ui/src/App.svelte) — `apiURL`, `headersFor`, `readEnvelope` |
| Operator-POST pattern to reuse | [ui/src/lib/ActionCards.svelte](../../ui/src/lib/ActionCards.svelte) |
| Board / advisories host | [ui/src/lib/IncidentWorkspace.svelte](../../ui/src/lib/IncidentWorkspace.svelte) |
| Mode / provenance badges | [ui/src/lib/ModelModeIndicator.svelte](../../ui/src/lib/ModelModeIndicator.svelte) |
| Manifest (request identity) | [testdata/demo/recording-manifest.json](../../testdata/demo/recording-manifest.json), `internal/democast/manifest.go`, `raw_events.go` |
| Replay bank | `testdata/demo/cassettes/`, [demo-cassette-recorder.md](../runbook/demo-cassette-recorder.md) |
| Identity header | [internal/api/server.go](../../internal/api/server.go) `IdentityHeader` = `X-Mosaic-Demo-Identity`, `supervisor-demo` |
