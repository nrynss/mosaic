# Runbook: Playwright demo E2E

Deterministic browser tests for the CAD-style reference UI. The suite drives the
**built** dashboard (`ui/dist`) served by `mosaicdemo` in **fixture** and
**replay** modes — never live OpenAI.

## Prerequisites

- Go toolchain (same as `go.mod`)
- Node 20+ / npm
- Chromium via Playwright (`npx playwright install chromium`)
- Repo checked out with `testdata/demo/cassettes/` present (replay bank)

Windows paths: pass **native** absolute paths to the Go binary (the start script
does this). Do not hand MSYS `/e/...` paths to `mosaicdemo.exe`.

## One-shot local run (headless)

From repo root:

```powershell
cd ui
npm ci
npx playwright install chromium
npm run test:e2e
# Re-run without rebuild (after first successful prepare):
npm run test:e2e:run
```

Run scripts **from `ui/`** (or `npm --prefix ui run …`) so the local
`@playwright/test` is used — a repo-root Playwright install can conflict.

What `npm run test:e2e` does:

1. `vite build` → `ui/dist`
2. `go build` → `ui/.e2e-bin/mosaicdemo[.exe]`
3. Playwright starts two `mosaicdemo` processes via `e2e/start-demo.mjs`:
   - **fixture** on `127.0.0.1:18080`
   - **replay** on `127.0.0.1:18081` with `MOSAIC_CASSETTE_DIR=testdata/demo/cassettes`
4. Runs projects: `fixture`, `walkthrough`, `replay`

Env enforced by the start script:

| Variable | Value |
|----------|--------|
| `MOSAIC_SEED_ON_START` | `0` |
| `MOSAIC_SIM_BEAT_SPACING` | `1ms` |
| `MOSAIC_SIM_MODE` | `fixture` or `replay` |
| `OPENAI_API_KEY` | cleared |
| `MOSAIC_UI_DIR` | absolute `ui/dist` |

## Headed / UI mode

```powershell
cd ui
npm run test:e2e:headed          # visible browser
npm run test:e2e:ui              # Playwright UI mode
npx playwright test --project=fixture --headed
npx playwright test e2e/03-cop-walk.spec.ts --headed
```

## Walkthrough recording (video + trace)

```powershell
cd ui
npm run test:e2e:walkthrough
```

Artifacts land under `ui/test-results/` (video/webm, trace.zip). Open a trace:

```powershell
npx playwright show-trace test-results/**/trace.zip
```

HTML report (after any run):

```powershell
npx playwright show-report
```

## Updating selectors

1. Prefer `data-testid` (and `data-*` state attrs) over CSS or visible copy.
2. Edit Svelte under `ui/src/` — keep values semantic (`play-scenario`,
   `cop-revision`, `advice-insight-card`, `dispatch-result`, …).
3. Commit selector changes **separately** from spec changes when possible.
4. Rebuild UI before re-running: `npm run build` (or full `npm run test:e2e`).

Key selectors live on:

| Surface | Components |
|---------|------------|
| Connection / modes | `App.svelte`, `ModelModeIndicator.svelte` |
| Scenario bar | `SimulationControls.svelte` |
| COP / advice | `IncidentWorkspace.svelte` |
| Handoffs | `ActionCards.svelte` |
| Decision history | `ProvenanceTab.svelte` |
| Model actions | `ModelActions.svelte`, `ModelResultCard.svelte` |

## Regenerating the walkthrough artifact

1. Ensure UI freeze for the take (or accept current branch UI).
2. `npm run test:e2e:walkthrough`
3. Copy `ui/test-results/**/*.webm` for post (ffmpeg → mp4 if needed).
4. Do **not** commit videos or reports (gitignored).

## Replay model flow

`e2e/replay-model-actions.spec.ts` (project `replay`):

1. Play scenario → COP rev 9
2. Generate assessment (Terra) / Request briefing (Sol) / Interpret (Luna)
3. Assert banked result card, `executed:false`, board unchanged

Requires the committed cassette bank. No API key.

## CI

Workflow: [`.github/workflows/playwright.yml`](../../.github/workflows/playwright.yml)

- Headless Chromium
- Uploads `ui/playwright-report/` + `ui/test-results/` **on failure**

## Troubleshooting

| Symptom | Fix |
|---------|-----|
| `binary not found` | `npm run e2e:prepare` or `e2e:prepare:force` from `ui/` |
| `ui dist missing` | `npm run build` |
| Port in use | stop leftover `mosaicdemo`; or set `MOSAIC_E2E_FIXTURE_PORT` / `MOSAIC_E2E_REPLAY_PORT` |
| Ambient live key | start script clears `OPENAI_API_KEY`; also `Remove-Item Env:OPENAI_API_KEY` |
| Flaky waits | assert on `data-status` / `data-revision` / testid visibility — never fixed `waitForTimeout` for logic |
| Windows process leftover | `taskkill /F /IM mosaicdemo.exe` |

## Safety

- Synthetic data only
- No live OpenAI in this suite
- Handoffs assert `executed:false` / `delivered:false`
- Model actions assert board COP revision unchanged after banked calls
