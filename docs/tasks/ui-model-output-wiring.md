# Task: Wire live/replayed model output into the demo UI

**Goal:** Add UI affordances that call the operator model endpoints
(`/operator/interpret` Luna, `/operator/analyze` Terra, `/operator/brief` Sol) and
render the returned model output **honestly** — real when live, banked when replay,
declined when fixture — without ever mutating the board. This is what lets the demo
*show* the real Terra/Sol/Luna output that the cassette recorder banked.

**Branch:** `feat/v0.4-pluggable-event-spine`  
**Status:** ✅ **Implemented** (2026-07-21)  
**Implementation commits:**
- `73f859a` — initial wiring (endpoint + UI + unit tests)
- *(this close-out)* — review fixes: COP gate, provenance honesty, unique testids,
  beat-7 curated, interactions→POST replay bank-hit test

**Unblocks:** the replay-backed Playwright flow (see
[playwright-demo-e2e.md](playwright-demo-e2e.md) §6.4).  
**Depends on:** cassette recorder (`1bb0974`) + committed bank `testdata/demo/cassettes/`
+ fixture enrichment (`3eea527`).

---

## Implementation record (landed)

| Decision / artifact | What shipped |
|---------------------|--------------|
| Manifest delivery | **(A)** `GET /api/v1/demo/interactions` — ready-to-POST steps from recording manifest + dataset raw events (`internal/democast.BuildInteractions`) |
| UI components | `ui/src/lib/ModelActions.svelte`, `ModelResultCard.svelte`; hosted in `IncidentWorkspace.svelte` |
| Terra | **Generate assessment** → POST `operator/analyze` with served payload; `data-testid="generate-assessment"` |
| Sol | **Request briefing** → POST `operator/brief` + `X-Mosaic-Demo-Identity` from interactions doc; `data-testid="request-briefing"` |
| Luna | Curated beats + “Show all beats”; unique `data-testid="interpret-event-{beat_id}"` |
| Curated Luna set | 911, road closure, EMS, **repaired incomplete road (7)**, quarantine (8), road correction |
| COP gate | Terra/Sol **disabled** until live COP `state_revision === expected_cop_revision` (9) |
| Provenance | Prefers response signals (`model_run.provider` + status); only labels **replay (banked)** on successful banked payloads; errors show `replay · outcome: error` |
| Luna canonical body | Operator API returns **identifiers only** (`canonical_event_id`) — intentional safety boundary; UI notes that it does not re-echo `event_type`/payload |
| Bank-hit proof | `TestDemoInteractionsReplayBankHits` in `cmd/mosaicdemo`: GET interactions → Play → POST luna/terra/sol from served bodies under replay + committed bank |
| Secrets | No keys in interactions JSON; browser never sends OpenAI credentials |

### How to demo (replay, $0)

```powershell
$env:MOSAIC_SIM_MODE = "replay"
$env:MOSAIC_CASSETTE_DIR = (Join-Path (Get-Location) "testdata\demo\cassettes")
Remove-Item Env:OPENAI_API_KEY -ErrorAction SilentlyContinue
# start mosaicdemo with asset-root = repo root, then:
# 1) Play scenario → COP rev 9
# 2) Generate assessment / Request briefing / Interpret curated beats
# 3) Result card: banked insight/recommendation/LunaResult, executed:false, Decision history audit
```

### Review close-out (all items addressed)

| # | Severity | Fix |
|---|----------|-----|
| 1 | suggestion | `TestDemoInteractionsReplayBankHits` POSTs served payloads under replay |
| 2 | suggestion | Terra/Sol disabled until COP rev matches expected |
| 3 | suggestion | Provenance uses response outcome, not process mode alone |
| 4 | suggestion | Documented API boundary (id only); UI note on result card |
| 5 | nit | Unique `interpret-event-{beat_id}` testids |
| 6 | nit | Beat-7 promoted into curated grid |

---

## 0. Current state (verified) — historical

- Before this parcel the UI **never** called model endpoints. "Refresh advice" only
  re-fetched `GET /api/v1/advisories`.
- Operator-POST pattern already existed in `ActionCards.svelte`; `headersFor` /
  `readEnvelope` live in `App.svelte`.

---

## 1. The determinism crux (design driver)

Replay hits require **byte-identical requests** to what was banked. Single source of
truth = [testdata/demo/recording-manifest.json](../../testdata/demo/recording-manifest.json),
served via **(A)** `GET /api/v1/demo/interactions` (not a UI mirror).

---

## 2. Mode behavior

| Mode | Model call | UI shows |
|------|------------|----------|
| `fixture` / passthrough | Terra/Sol refuse; Luna fixture | fixture (declined) / fixture |
| `replay` | banked cassette | **replay (banked)** + `mosaic-fixture` |
| `live` / record | OpenAI | live / record |

---

## 3. Affordances (shipped)

1. Terra — Generate assessment  
2. Sol — Request briefing (supervisor identity)  
3. Luna — Interpret event (curated + all beats); quarantines shown with reason  

---

## 4. Honest rendering + boundary (non-negotiable)

- Provenance badge reflects **outcome**, not only process mode  
- Non-ok (refused / quarantined + reason / error) rendered truthfully  
- “Proposed, not applied — board unchanged · executed: false”  
- No browser-side API key  

---

## 5. Selectors for Playwright

| testid | Notes |
|--------|--------|
| `generate-assessment` | Terra |
| `request-briefing` | Sol |
| `interpret-event-{beat_id}` | Unique per beat (e.g. `interpret-event-baseline-01-911-call`) |
| `model-result-card` | Result region |
| `model-result-status` | Terminal status pill |
| `model-provenance-badge` | Provenance string |
| `luna-quarantine-reason` | Quarantine reason block |

Also: `data-beat` on Luna buttons and result card.

---

## 6. Decisions (confirmed)

1. Manifest delivery: **(A)** endpoint — done  
2. Default demo mode: **replay** for audience demos — documented  
3. Luna scope: curated (incl. beat 7 + 8) + show all — done  
4. Sequencing: after enrichment (`3eea527`) — done  

---

## 7. Acceptance criteria

- [x] Terra/Sol/Luna affordances added, reusing the `readEnvelope` operator-POST pattern.
- [x] Requests match the manifest via `GET /api/v1/demo/interactions`; replay hits the
      bank with no key (integration test + e2e democast path).
- [x] All three modes render with honest provenance (fixture decline / live / replay banked;
      errors not mislabeled as banked).
- [x] Non-`ok` outcomes (refusal / quarantine + `reason` / failure) render truthfully.
- [x] Board unchanged; `executed: false`; Decision history refreshed after actions.
- [x] No secret/API key leaves the server; browser sends none.
- [x] `data-testid`s for Playwright (`interpret-event-{beat_id}` unique form).
- [x] CI-style replay path works without key.

---

## 8. Non-goals

- No board mutation from model output (hard boundary).
- No browser-side API key or live-by-default.
- No free-form operator model input in the demo path.
- Full canonical event body re-echo to the browser (API returns ids only).

---

## 9. Files & symbols index

| Area | Path |
|---|---|
| Interactions builder | [internal/democast/interactions.go](../../internal/democast/interactions.go) |
| API handler | [internal/api/demo_interactions.go](../../internal/api/demo_interactions.go) |
| Route / DemoAssetRoot | [internal/api/server.go](../../internal/api/server.go), [cmd/mosaicdemo/main.go](../../cmd/mosaicdemo/main.go) |
| UI actions / results | [ui/src/lib/ModelActions.svelte](../../ui/src/lib/ModelActions.svelte), [ModelResultCard.svelte](../../ui/src/lib/ModelResultCard.svelte) |
| Workspace host | [ui/src/lib/IncidentWorkspace.svelte](../../ui/src/lib/IncidentWorkspace.svelte) |
| Bank-hit integration test | `cmd/mosaicdemo` — `TestDemoInteractionsReplayBankHits` |
| Manifest | [testdata/demo/recording-manifest.json](../../testdata/demo/recording-manifest.json) |
| Replay bank | `testdata/demo/cassettes/` |
