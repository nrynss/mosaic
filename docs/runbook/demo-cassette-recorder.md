# Demo cassette recorder — offline design, one live bank, forever replay

This runbook covers the **scripted demo recorder** for the domestic-disturbance
timeline: Play → Luna×10 → Terra@rev9 → Sol@rev9.

**Audience visibility:** verification / regression asset only (no UI wiring yet).

**Key property:** cassette **keys are computed from the request**, not the model
response. Offline stubs prove key stability; one gated live pass only fills real
response content behind the same keys.

| Asset | Path |
|-------|------|
| Request manifest | [`testdata/demo/recording-manifest.json`](../../testdata/demo/recording-manifest.json) |
| Committed cassette bank | [`testdata/demo/cassettes/`](../../testdata/demo/cassettes/) |
| Shared driver | [`internal/democast/`](../../internal/democast/) |
| Offline loop (in-process) | `cmd/mosaicdemo` — `TestDemoCastOfflineRecordReplay` |
| No-live CI replay (e2e) | `tests/e2e` — `TestDemoCastReplayNoLiveE2E` |
| Gated live re-record | `tests/e2e` — `TestDemoCastRecordLiveE2E` |

Bank location is **`testdata/demo/cassettes/`** (committed). It is **not** covered
by the repo `.gitignore` `/recordings/` rule.

---

## 1. Verify offline / replay (no live, $0)

### Manifest + identity package tests

```powershell
go test ./internal/democast -count=1 -timeout 60s
```

### Offline stub record → replay (in-process, injects `testLiveLuna` / Terra / Sol)

```powershell
go test ./cmd/mosaicdemo -count=1 -timeout 180s -run 'DemoCastOffline'
```

This proves:

- Play reaches COP revision 9
- All 10 Luna + Terra + Sol steps bank under FileStore keys
- Two record runs produce **identical keys**
- Replay hits 100% with **no** `OPENAI_API_KEY` and zero `ErrReplayMiss`

### Refresh the committed **stub** bank (optional)

Only needed when the **manifest** (request identity) changes:

```powershell
$env:MOSAIC_WRITE_DEMO_CASSETTES = '1'
go test ./cmd/mosaicdemo -count=1 -timeout 180s -run 'TestDemoCastOfflineRecordReplay'
Remove-Item Env:MOSAIC_WRITE_DEMO_CASSETTES
```

Writes stub-shaped responses under `testdata/demo/cassettes/`. Commit the files.
Live re-record later overwrites the **same key paths** with real model content.

### No-live CI replay (subprocess mosaicdemo)

```powershell
# Ensure no ambient key is forced into the child (the test clears OPENAI_API_KEY).
go test ./tests/e2e -count=1 -timeout 300s -run 'TestDemoCastReplayNoLiveE2E'
```

Expect: `cassette_mode=replay`, every step matches the manifest `expected_status`
(default `ok`; see live outcomes in §2a), `provider: mosaic-fixture` on model
runs, two sub-passes for determinism.

### In-process committed-bank replay

```powershell
go test ./cmd/mosaicdemo -count=1 -timeout 180s -run 'TestDemoCastReplayCommittedBankNoLive'
```

---

## 2. Re-record live once (gated, spends cents)

**Not in default CI.** Requires a real `OPENAI_API_KEY` and deliberate opt-in.

```powershell
# Load key from your local .env (never commit it)
# Example: $env:OPENAI_API_KEY = (Get-Content .env | …)  — do not echo the value

$env:MOSAIC_RECORD_LIVE = '1'
go test ./tests/e2e -count=1 -timeout 600s -run 'TestDemoCastRecordLiveE2E'
Remove-Item Env:MOSAIC_RECORD_LIVE
```

What it does:

1. **Clears** `testdata/demo/cassettes/` first (no orphan / mixed stub+live keys).
2. Starts `mosaicdemo` with `MOSAIC_SIM_MODE=record`, all three providers `live`,
   `MOSAIC_CASSETTE_DIR` → that bank path, `MOSAIC_SEED_ON_START=0`,
   `MOSAIC_SIM_BEAT_SPACING=1ms`.
3. Drives the **same** manifest as offline (Play → Luna×10 → Terra → Sol).
4. Asserts **exactly 12** banked keys (10 luna + 1 terra + 1 sol).

**Live vs CI status contract:** live may legitimately return `quarantined` /
`refused` on non-incident beats. CI no-live replay is **strict**: each Luna step
must match `expected_status` in the manifest (default `ok`). If a later live run
drifts, the test logs a WARN — update `expected_status` (and offline `StubLuna`
quarantine set if needed), re-run no-live, then commit bank + manifest.

After a successful live pass:

```powershell
go test ./tests/e2e -count=1 -timeout 300s -run 'TestDemoCastReplayNoLiveE2E'
```

Commit the updated bank (and any `expected_status` edits) only when that is green.

### 2a. Live bank record (2026-07-21, post attribute enrichment) — current

Current committed bank under `testdata/demo/cassettes/`. Fixture raw events
carry structured intake identifiers in `attributes` (option A enrichment from
`expected-outcomes.json`); free-text `payload_bytes_b64` / `raw_sha256` are
unchanged. Luna prompt clarifies that attributes are authoritative intake
metadata to echo — not a license to fabricate missing IDs.

| Metric | Result |
|--------|--------|
| Command | `go test ./tests/e2e -run TestDemoCastRecordLiveE2E` with `MOSAIC_RECORD_LIVE=1` |
| Wall time | ~117s (PASS) |
| Cassettes written | **12** (exactly 10 Luna + 1 Terra + 1 Sol) |
| Request keys | Luna keys rehashed after attribute enrichment (request-derived; offline identity still matches) |
| Bank size | ~25 KB total FileStore JSON |
| No-live verify | `TestDemoCastReplayNoLiveE2E` PASS ×2, no `OPENAI_API_KEY`, `provider: mosaic-fixture` |
| In-process bank replay | `TestDemoCastReplayCommittedBankNoLive` PASS |

**Luna terminal status (banked `result_json.status` → operator `ok` when accepted):**

| Beat | raw_event_id | Live status | Manifest `expected_status` | Canonical `event_type` / notes |
|------|--------------|-------------|----------------------------|--------------------------------|
| 1 | raw-domestic-001-call | accepted → ok | ok (default) | `incident_reported`; IDs `incident-domestic-001`, `location-cedar-lane-014` |
| 2 | raw-domestic-002-welfare | accepted → ok | ok (default) | `incident_reported`; same COP incident/location |
| 3 | raw-domestic-003-weather | accepted → ok | ok (default) | `weather_alert_issued`; `weather-heavy-rain-001` |
| 4 | raw-domestic-004-main-road | accepted → ok | ok (default) | `road_status_changed`; `road-main-street-bridge` |
| 5 | raw-domestic-005-ems-available | accepted → ok | ok (default) | **`resource_status_changed`**; `resource-ems-004` |
| 6 | raw-domestic-006-officer-update | accepted → ok | ok (default) | `unit_status_changed`; `unit-017` |
| 7 | raw-domestic-007-incomplete-road | accepted → ok | ok (default) | `road_status_changed`; `road-brook-lane` (enriched so live can accept) |
| 8 | raw-domestic-008-invalid-input | quarantined | quarantined | bare attributes; honest failure case |
| 9 | raw-domestic-009-late-ems | accepted → ok | ok (default) | **`resource_status_changed`**; `resource-ems-004` unavailable |
| 10 | raw-domestic-010-road-correction | accepted → ok | ok (default) | `road_status_changed`; `road-brook-lane` open |

**Terra** (`terra/rev9/…`) and **Sol** (`sol/rev9/…`): both **ok**.

**Notes from the run**

- Attribute enrichment fixed the earlier quarantine cascade: live Luna now
  accepts beats 1–7, 9, and 10 and echoes the same COP IDs the deterministic
  fixture pipeline uses.
- EMS beats 5 and 9 correctly classify as `resource_status_changed` (not unit).
- Beat 8 (intentionally malformed bare input) remains the only quarantine.
- Offline `StubLuna` quarantine set is only `raw-domestic-008-invalid-input`.
- Cost was on the order of ~12 structured OpenAI calls (cents). No secrets or
  API keys were written into the bank.

**Historical note:** The first live pass earlier the same day (bare fixtures,
~147s) banked five Luna quarantines (beats 1, 2, 6, 7, 8) because free-text
payloads lacked intake IDs. That was correct Luna behavior; the data layer was
the defect. This re-record supersedes that bank.

### Manual server + client (optional)

```powershell
go build -o mosaicdemo.exe ./cmd/mosaicdemo

# Windows paths for the Go binary (do not pass MSYS /c/... paths)
$root = (Get-Location).Path
$env:MOSAIC_SIM_MODE = 'record'
$env:MOSAIC_LUNA_PROVIDER = 'live'
$env:MOSAIC_TERRA_PROVIDER = 'live'
$env:MOSAIC_SOL_PROVIDER = 'live'
$env:MOSAIC_SEED_ON_START = '0'
$env:MOSAIC_SIM_BEAT_SPACING = '1ms'
$env:MOSAIC_CASSETTE_DIR = Join-Path $root 'testdata\demo\cassettes'
# OPENAI_API_KEY must already be set

.\mosaicdemo.exe -listen-addr 127.0.0.1:8099 -asset-root $root -ui-dir (Join-Path $root 'ui')
# then drive the manifest against http://127.0.0.1:8099 (or use the gated test)
```

Replay-only server:

```powershell
Remove-Item Env:OPENAI_API_KEY -ErrorAction SilentlyContinue
$env:MOSAIC_SIM_MODE = 'replay'
$env:MOSAIC_CASSETTE_DIR = Join-Path (Get-Location).Path 'testdata\demo\cassettes'
.\mosaicdemo.exe -listen-addr 127.0.0.1:8099 -asset-root (Get-Location).Path -ui-dir (Join-Path (Get-Location).Path 'ui')
```

---

## 3. Windows gotchas

| Issue | Fix |
|-------|-----|
| Go binary does not resolve MSYS `/c/...` paths | Pass **Windows** paths (`E:\work\mosaic\...` or `cygpath -w` if using bash) |
| `pkill -f mosaicdemo.exe` does not match | `taskkill /F /IM mosaicdemo.exe` |
| Ambient `OPENAI_API_KEY` in the parent shell | e2e replay test **clears** it for the child; manual runs should `Remove-Item Env:OPENAI_API_KEY` |
| Poll after Play | Wait until `GET /api/v1/simulation/status` reports `status == "ended"` before operator calls (COP must be rev 9) |

---

## 4. Request identity rules (do not break)

- **Luna:** SHA-256 of exact `json.Marshal(gen.RawEvent)` bytes after the operator
  rebuilds the envelope from the request. Always load fields **verbatim** from
  `datasets/domestic-disturbance/raw-events.json` via the manifest `raw_event_ref`.
- **Terra / Sol:** evidence match is exact
  `{target_kind, target_id, json_pointer, explanation}`. Explanation strings are
  literals in the manifest.
- **Sol:** header `X-Mosaic-Demo-Identity: supervisor-demo`; hydrates
  `insight-domestic-access-001` from store at rev 9; `requested_by` is part of the key.
- **COP revision:** Play with seed off + fixed dataset → rev 9 every time.

If the manifest changes, re-run offline identity proof, rewrite the stub bank
(`MOSAIC_WRITE_DEMO_CASSETTES=1`), then optionally live re-record.

---

## 5. Bank contents

- Only FileStore-keyed cassettes: `{agent}/…/{hash16}.json`
- 12 files expected: 10× `luna/…`, 1× `terra/rev9/…`, 1× `sol/rev9/…`
- No operator response dumps under the bank directory
- **Current committed bank is live OpenAI content** from the 2026-07-21
  post-enrichment re-record (§2a), not stubs. Offline stubs remain for the
  in-process identity loop only (`TestDemoCastOfflineRecordReplay` / optional
  `MOSAIC_WRITE_DEMO_CASSETTES=1`).
- Do not re-run `MOSAIC_WRITE_DEMO_CASSETTES=1` against the committed path unless
  you intend to replace live content with stub JSON.
