# Mosaic demo dashboard (UI)

Svelte + Vite front end for the Mosaic interactive operator demo. The built
`dist/` is served by `mosaicdemo` in production and in CI.

## Quick start (dev)

```powershell
cd ui
npm ci
npm run dev
```

Point a local `mosaicdemo` at this Vite origin (or use the full demo binary with
`-ui-dir` after `npm run build`).

## Quality

```powershell
npm run check    # svelte-check
npm run build    # production bundle → dist/
```

---

## Playwright E2E (fixture + replay)

Deterministic browser tests drive the **built** UI served by `mosaicdemo` — the
same path as production. No live OpenAI; no Vite-dev-server-only coverage.

| Project | What it proves |
|---------|----------------|
| **fixture** | Demo script board flows (play → COP → advice → handoffs → history) |
| **walkthrough** | One full narrative take with **video + trace always on** |
| **replay** | Terra / Sol / Luna UI buttons hit the **cassette bank** at $0 |

### Install once

```powershell
cd ui
npm ci
npx playwright install chromium
```

### Run the suite

```powershell
cd ui

# Full path: build UI, force-rebuild Go binary, run all projects
npm run test:e2e

# Faster re-run (smart binary rebuild if Go sources changed)
npm run test:e2e:run

# Visible browser / Playwright UI
npm run test:e2e:headed
npm run test:e2e:ui

# Recording take only
npm run test:e2e:walkthrough

# Slice by project
npm run test:e2e:fixture
npm run test:e2e:replay
```

Always run from **`ui/`** so the local `@playwright/test` is used.

### What happens under the hood

1. `vite build` → `ui/dist`
2. `go build` → `ui/.e2e-bin/mosaicdemo[.exe]`  
   - Full `test:e2e` **always** rebuilds (`e2e:prepare:force`)  
   - `test:e2e:run` rebuilds only if Go sources are **newer** than the binary
3. Playwright starts **two** `mosaicdemo` processes:
   - fixture → `http://127.0.0.1:18080`
   - replay → `http://127.0.0.1:18081` + `testdata/demo/cassettes`
4. Specs wait on **state** (`data-revision`, `data-status`, HTTP responses) — not fixed sleeps

### Environment

| Variable | Default | Purpose |
|----------|---------|---------|
| `MOSAIC_E2E_FIXTURE_PORT` | `18080` | Fixture server |
| `MOSAIC_E2E_REPLAY_PORT` | `18081` | Replay server |
| `MOSAIC_E2E_REUSE` | unset | Set `1` only to reuse an already-running server |
| `MOSAIC_E2E_REBUILD` | unset | Force Go rebuild in `e2e:prepare` |
| `MOSAIC_E2E_ALLOW_AMBIENT_PROVIDERS` | unset | Set `1` to keep shell `MOSAIC_*_PROVIDER` (not recommended) |

The start script **clears `OPENAI_API_KEY`** and **forces** Luna/Terra/Sol providers to
`fixture` unless the ambient escape hatch is set. Temp SQLite DBs live under the OS
temp dir and are removed on process exit (stale files cleaned after ~6h).

### Artifacts

| Path | Contents |
|------|----------|
| `ui/test-results/` | Failures (and walkthrough video/trace) — gitignored |
| `ui/playwright-report/` | HTML report — gitignored |
| `ui/.e2e-bin/` | Local demo binary — gitignored |

```powershell
npx playwright show-report
npx playwright show-trace test-results/**/trace.zip
```

### Specs map

| File | Flow |
|------|------|
| `e2e/01-load-modes.spec.ts` | Connection + fixture mode badges |
| `e2e/02-play-scenario.spec.ts` | Play → COP rev 9 |
| `e2e/03-cop-walk.spec.ts` | Claim-class + entity `data-status` |
| `e2e/04-refresh-advice.spec.ts` | Advisories + supersession, board unchanged |
| `e2e/05-handoffs.spec.ts` | Dispatch/maintenance `executed:false` |
| `e2e/06-decision-history.spec.ts` | Audit rows for **actions taken** |
| `e2e/07-demo-walkthrough.spec.ts` | Full narrative recording |
| `e2e/replay-model-actions.spec.ts` | Banked Terra/Sol/Luna (incl. quarantine) |

### Selectors

Prefer `data-testid` and state attributes (`data-status`, `data-revision`,
`data-executed`, `data-mode`). Do not assert on CSS classes or churnable marketing copy.

Extended runbook (troubleshooting, CI, selector policy):

→ [`docs/runbook/playwright-demo-e2e.md`](../docs/runbook/playwright-demo-e2e.md)

### CI

GitHub Actions: [`.github/workflows/playwright.yml`](../.github/workflows/playwright.yml)  
Runs headless Chromium and uploads report + test-results **on failure**.

### Safety

- Synthetic data only  
- No live OpenAI spend in this suite  
- Handoffs and model actions stay `executed: false` (board does not mutate)  
