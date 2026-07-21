# Task: Playwright E2E for the demo UI (deterministic, replay-backed)

**Goal:** Drive the demo UI end-to-end in a real browser, deterministically, backed by
the mosaicdemo server in **fixture/replay** mode (`$0`, offline). Primary purpose: lock
the demo narrative against regressions. Secondary: produce a repeatable demo
walkthrough artifact (Playwright video/trace).

**Branch:** `feat/v0.4-pluggable-event-spine`  
**Status:** ✅ **Implemented** (2026-07-21)  
**Implementation commits:**
- `f26f2c6` — data-testid selectors  
- `f4ad58b` — Playwright suite (fixture + walkthrough + replay) + CI + runbook  
- *(close-out)* — review fixes (stale binary, stricter asserts, providers, ports, docs + `ui/README.md`)

**Why now:** the deterministic replay bank (demo cassette recorder, `1bb0974`) makes the
whole app reproducible offline — the missing piece is browser-level coverage of the UI
the audience actually sees. UI model wiring (§6.4) has also landed (`73f859a` / `a429300`),
so the suite includes a **replay banked model** project.

**How to use:** [`ui/README.md`](../../ui/README.md) · runbook
[`docs/runbook/playwright-demo-e2e.md`](../runbook/playwright-demo-e2e.md)

---

## 0. Starting reality (verified)

- UI is **Svelte + Vite** ([ui/package.json](../../ui/package.json)): scripts `dev`
  (vite), `build` (vite build), `check` (svelte-check). **No test runner, no
  Playwright.**
- **Zero `data-testid`** attributes anywhere in `ui/src` — stable selectors must be
  added (see §3). This is a real prerequisite, not optional.
- The built UI (`ui/dist`) is served by `mosaicdemo` via `MOSAIC_UI_DIR`
  ([Dockerfile](../../Dockerfile)); test the **served production artifact**, not the
  vite dev server, so it matches what ships.
- Demo narrative (product): connection + modes → Play scenario → COP walk →
  advisories / Model Actions → handoffs → decision history.
- **Important:** the UI's "Refresh advice" calls `GET /api/v1/advisories` (re-poll), NOT
  the live model endpoints ([ui/src/lib/IncidentWorkspace.svelte](../../ui/src/lib/IncidentWorkspace.svelte)).
  The board is fixture/replay-driven. Surfacing real banked model output in the UI is a
  separate decision (§6.4).

---

## 1. Decisions to confirm (discuss first)

| # | Decision | Recommendation |
|---|----------|----------------|
| 1 | **Primary purpose:** regression tests vs demo-recording artifact vs both | **Both**, regression-first; recording is a near-free byproduct of Playwright trace/video |
| 2 | **Backend mode under test:** fixture vs replay vs both | Start **fixture** (matches what the UI drives today, fully deterministic); add a **replay**-backed run once §6.4 is resolved so the demo shows real banked model output |
| 3 | **Selectors:** add `data-testid` as a separate parcel vs bundled into this task | **Bundled** — the tests need them; keep the selector additions in their own commit for reviewability |
| 4 | **Server under test:** built dist served by mosaicdemo vs vite dev + API proxy | **Built dist + mosaicdemo** (production parity) |
| 5 | **CI:** run in CI now vs local-only first | CI headless from the start; upload trace/video/screenshots on failure |

---

## 2. Architecture

```
npm --prefix ui run build         # ui/dist
mosaicdemo  (MOSAIC_SIM_MODE=fixture, MOSAIC_SEED_ON_START=0,
             MOSAIC_SIM_BEAT_SPACING=1ms, MOSAIC_UI_DIR=ui/dist)  → http://127.0.0.1:PORT
Playwright (Chromium, headless) → navigates the served app, asserts on data-testid state
```

- Playwright `webServer` config launches the built binary (or a small wrapper script)
  and waits for `/api/v1/health`.
- For the **replay** variant: `MOSAIC_SIM_MODE=replay`, `MOSAIC_CASSETTE_DIR=testdata/demo/cassettes`,
  no `OPENAI_API_KEY`.

---

## 3. Prerequisite: stable selectors (`data-testid`)

Add `data-testid` to the elements the flows assert on (none exist today). Minimum set,
mapped to demo walkthrough steps:

- **Connection / modes:** connection pill, Luna/Terra/Sol mode badges
  ([ui/src/lib/ModelModeIndicator.svelte](../../ui/src/lib/ModelModeIndicator.svelte)).
- **Scenario bar:** `play-scenario`, `start-over`, scenario clock, run status.
- **COP / Live incident board:** container, per-section groups (incidents, units,
  resources, roads, weather_alerts), each fact row + its **claim-class** label, and a
  state-revision indicator.
- **Advisories:** insight cards, recommendation cards, current-vs-history toggle,
  `refresh-advice` button, per-card evidence buttons
  ([ui/src/lib/IncidentWorkspace.svelte](../../ui/src/lib/IncidentWorkspace.svelte)).
- **Handoffs / actions:** dispatch, maintenance/road note, save, approve, annotate, and
  the `executed:false` / "not carried out" result state
  ([ui/src/lib/ActionCards.svelte](../../ui/src/lib/ActionCards.svelte)).
- **Decision history:** list container + row (model run / audit entries).

Keep `data-testid` values stable and semantic (`cop-incident-row`, `advice-insight-card`,
`play-scenario`). Do not assert on CSS classes or visible copy that may change.

---

## 4. Flows to cover

1. **Load + modes** — app loads, connection pill connected, mode badges reflect
   configured providers (fixture in the baseline run).
2. **Play scenario** — click `play-scenario`; wait for run status `ended` / COP
   `state_revision` to reach 9 (assert via testid, **not** a timeout). Assert all 10
   beats landed (COP sections populated).
3. **COP walk** — assert incident, blocked→open road, EMS resource availability, weather
   alert, unit assignment appear with their **claim-class** labels.
4. **Refresh advice + supersession** — click `refresh-advice`; assert the access insight
   is present and that a later revision marks it superseded / not-current; toggle history
   shows the rev-7 card still queryable at rev 9. Assert the **board itself did not change**
   from pressing refresh (boundary: advice proposes, never mutates COP).
5. **Handoffs** — dispatch + maintenance notes → save; assert result shows
   `executed:false` / not delivered (human-gate boundary).
6. **Decision history** — assert audit/model-run entries accumulated for the actions taken.

---

## 5. Determinism rules (must follow)

- `MOSAIC_SEED_ON_START=0`, `MOSAIC_SIM_BEAT_SPACING=1ms`, fixed dataset → identical run
  every time (COP rev 9).
- **Wait on state, never on time.** Use `expect(locator).toHaveText/toBeVisible` and a
  revision/status testid; the app uses SSE streams (`/api/v1/simulation/stream`,
  `/api/v1/stream`) so content arrives asynchronously.
- Disable animations (`reducedMotion: 'reduce'` in Playwright context) for stable
  screenshots.
- Pin a viewport; the app is theme-aware — pick one theme for snapshot stability.
- No network to third parties; replay variant runs with no API key (assert no live
  calls by checking mode badges / provider labels = fixture).

---

## 6. Deliverables

1. `ui` devDependency `@playwright/test` + `playwright.config.ts` (webServer launches the
   built mosaicdemo; Chromium project; trace `on-first-retry`; video `retain-on-failure`).
2. `data-testid` selector additions (§3) — separate commit.
3. Specs under `ui/e2e/` (or `tests/ui/`): one spec per flow in §4, plus a full
   happy-path "demo walkthrough" spec that produces the recording artifact.
4. A launch wrapper (or Playwright `webServer.command`) that builds the UI, starts
   mosaicdemo in fixture mode, waits on `/api/v1/health`. **Windows:** pass `cygpath -w`
   paths to the Go binary; stop via `taskkill //F //IM mosaicdemo.exe` (see recorder
   runbook §3).
5. CI job: install browsers (`npx playwright install --with-deps chromium`), run headless,
   upload `playwright-report/` + traces/videos on failure.
6. Short runbook: how to run locally (`headed`, `--ui`), how to update selectors, how to
   regenerate the walkthrough recording.

### 6.4 Open dependency — showing REAL model output in the UI
Today the UI never calls `/operator/analyze|brief|interpret`, so Playwright (fixture or
replay) exercises the **fixture-driven board**, not live/replayed model output. If the
demo should *show* the banked real Terra/Sol/Luna output, that needs UI wiring (a
"generate advice"/"interpret" affordance that issues those operator requests) — a
**separate parcel**. When it lands, add a **replay-backed** Playwright flow that drives
it and asserts the banked recommendation/insight text renders. Until then, keep the
Playwright suite on the fixture board. (See the recorder task's audience-visibility
decision.)

---

## 7. Acceptance criteria

- [x] `@playwright/test` + config added; `webServer` launches built mosaicdemo (fixture),
      waits on health.
- [x] `data-testid` selectors added for every element the specs assert on (separate commit
      `f26f2c6`).
- [x] Specs cover flows §4; all assertions wait on state, none on fixed timeouts.
- [x] Suite is deterministic: headless, no third-party network, no API key; full `test:e2e`
      force-rebuilds Go binary; smart rebuild on `test:e2e:run`.
- [x] CI runs the suite headless and uploads report/trace/video on failure.
- [x] A "demo walkthrough" spec emits a video/trace artifact.
- [x] Runbook + `ui/README.md`: local run, selectors, recording, ports, safety.
- [x] Replay-backed model flow (Terra/Sol/Luna bank + strict quarantine) — after §6.4 UI wiring.

### Review close-out

| # | Item | Fix |
|---|------|-----|
| 1 | Stale e2e binary | Smart mtime rebuild + force on full `test:e2e` |
| 2 | Weak decision history | Count + match handoff **notes** after actions |
| 3 | Loose provenance | Require `replay (banked)` on bank hits |
| 4 | Loose quarantine | Strict `quarantined` + reason visible |
| 5 | Busy ports | Preflight bind + clear env-override message |
| 6 | Ambient live providers | Force `fixture` unless escape hatch |
| 7 | Empty mode badge | Require `fixture\|mosaic-fixture` |
| 8 | Copy-based COP status | `data-status` on claim rows |
| 9 | Temp DB leak | Unlink on exit + stale cleanup |
| 10 | Dual servers always | Documented; fixture-only still boots replay |

---

## 8. Non-goals
- No live OpenAI calls in the Playwright suite (fixture/replay only).
- No assertions on CSS/visible copy that may churn — assert on `data-testid` + semantic state.
- (Historical) UI wiring for operator endpoints was a separate parcel — **now landed**.

---

## 9. Files & symbols index
| Area | Path |
|---|---|
| UI root / build | [ui/package.json](../../ui/package.json), `ui/dist` (built) |
| Board / advisories / Model Actions | [ui/src/lib/IncidentWorkspace.svelte](../../ui/src/lib/IncidentWorkspace.svelte), [ui/src/lib/ModelActions.svelte](../../ui/src/lib/ModelActions.svelte) |
| Mode badges | [ui/src/lib/ModelModeIndicator.svelte](../../ui/src/lib/ModelModeIndicator.svelte) |
| Handoff actions | [ui/src/lib/ActionCards.svelte](../../ui/src/lib/ActionCards.svelte) |
| App shell / SSE | [ui/src/App.svelte](../../ui/src/App.svelte) |
| Server flags / modes | [cmd/mosaicdemo/main.go](../../cmd/mosaicdemo/main.go), [.env.example](../../.env.example) |
| Replay bank (for replay variant) | `testdata/demo/cassettes/`, [demo-cassette-recorder.md](../runbook/demo-cassette-recorder.md) |
