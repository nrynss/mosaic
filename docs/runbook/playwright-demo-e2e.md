# Runbook: Playwright demo E2E

Deterministic browser tests for the CAD-style reference UI. The suite drives the
**built** dashboard (`ui/dist`) served by `mosaicdemo` in **fixture** and
**replay** modes — never live OpenAI.

Primary user-facing guide: [`ui/README.md`](../../ui/README.md) (Playwright section).

## Prerequisites

- Go toolchain (same as `go.mod`)
- Node 20+ / npm
- Chromium via Playwright (`npx playwright install chromium`)
- Repo checked out with `testdata/demo/cassettes/` present (replay bank)

Windows paths: the start script resolves **native** absolute paths. Do not hand
MSYS `/e/...` paths to `mosaicdemo.exe`.

## One-shot local run (headless)

From repo root:

```powershell
cd ui
npm ci
npx playwright install chromium
npm run test:e2e
# Re-run with smart binary rebuild (sources newer than binary):
npm run test:e2e:run
```

Run scripts **from `ui/`** so the local `@playwright/test` is used — a repo-root
Playwright install can conflict.

What `npm run test:e2e` does:

1. `vite build` → `ui/dist`
2. **Force** `go build` → `ui/.e2e-bin/mosaicdemo[.exe]` (`e2e:prepare:force`)
3. Playwright starts two `mosaicdemo` processes via `e2e/start-demo.mjs`:
   - **fixture** on `127.0.0.1:18080`
   - **replay** on `127.0.0.1:18081` with `MOSAIC_CASSETTE_DIR=testdata/demo/cassettes`
4. Runs projects: `fixture`, `walkthrough`, `replay`

`e2e:prepare` (used by `test:e2e:run`) rebuilds when Go sources under
`cmd/mosaicdemo` / `internal` (or `go.mod` / `go.sum`) are newer than the binary.

Env enforced by the start script:

| Variable | Value |
|----------|--------|
| `MOSAIC_SEED_ON_START` | `0` |
| `MOSAIC_SIM_BEAT_SPACING` | `1ms` |
| `MOSAIC_SIM_MODE` | `fixture` or `replay` |
| `OPENAI_API_KEY` | cleared |
| `MOSAIC_UI_DIR` | absolute `ui/dist` |
| `MOSAIC_*_PROVIDER` | forced to `fixture` (unless `MOSAIC_E2E_ALLOW_AMBIENT_PROVIDERS=1`) |

## Headed / UI mode

```powershell
cd ui
npm run test:e2e:headed          # visible browser
npm run test:e2e:ui              # Playwright UI mode
npm run test:e2e:fixture
npm run test:e2e:replay
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

1. Prefer `data-testid` and **state attributes** (`data-status`, `data-revision`,
   `data-executed`, `data-mode`, `data-claim-class`) over CSS or visible copy.
2. Edit Svelte under `ui/src/` — keep values semantic.
3. Commit selector changes **separately** from spec changes when possible.
4. Rebuild UI before re-running: `npm run build` (or full `npm run test:e2e`).

Key selectors live on:

| Surface | Components |
|---------|------------|
| Connection / modes | `App.svelte`, `ModelModeIndicator.svelte` |
| Scenario bar | `SimulationControls.svelte` |
| COP / advice | `IncidentWorkspace.svelte` (`cop-claim-row` + `data-status`) |
| Handoffs | `ActionCards.svelte` |
| Decision history | `ProvenanceTab.svelte` (`audit-record-row` + note text) |
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
3. Assert **`replay (banked)`** provenance, `executed:false`, board unchanged  
4. Quarantine beat asserts **strict** `status=quarantined` + visible reason  

Requires the committed cassette bank. No API key.

## CI

Workflow: [`.github/workflows/playwright.yml`](../../.github/workflows/playwright.yml)

- Headless Chromium  
- Uploads `ui/playwright-report/` + `ui/test-results/` **on failure**  
- Uses force prepare (no stale binary)

## Ports and dual servers

Both fixture and replay servers always start (even for `--project=fixture`) so
project filters stay simple. That requires:

- Free ports (defaults `18080` / `18081`)  
- Cassette bank present for the replay process  

Override:

```powershell
$env:MOSAIC_E2E_FIXTURE_PORT = '19080'
$env:MOSAIC_E2E_REPLAY_PORT = '19081'
npm run test:e2e
```

`start-demo.mjs` preflights the port and exits with a clear message if it is busy.

## Troubleshooting

| Symptom | Fix |
|---------|-----|
| `binary not found` | `npm run e2e:prepare` or `e2e:prepare:force` from `ui/` |
| Stale backend behaviour after Go edits | Full `test:e2e` force-rebuilds; `test:e2e:run` rebuilds if sources are newer |
| `ui dist missing` | `npm run build` |
| Port in use | stop leftover `mosaicdemo`; or set `MOSAIC_E2E_FIXTURE_PORT` / `MOSAIC_E2E_REPLAY_PORT` |
| Ambient live providers | start script overwrites to `fixture` (clear key still enforced) |
| Flaky waits | assert on `data-status` / `data-revision` / testid visibility — never fixed `waitForTimeout` for logic |
| Windows process leftover | `taskkill /F /IM mosaicdemo.exe` |
| Temp DB clutter | auto-unlink on exit; files older than ~6h cleaned on next start |

## Safety

- Synthetic data only  
- No live OpenAI in this suite  
- Handoffs assert `executed:false` / not delivered  
- Model actions assert board COP revision unchanged after banked calls  
- Decision history asserts **notes from the handoffs just performed**, not only seeded audits  
