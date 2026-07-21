# Task: Scripted demo recorder — bank the full demo for $0 offline replay

**Goal:** **Design the demo beats and work out every determinism kink entirely
offline (no live runs), then do exactly ONE live run to bank real model responses.**
The deliverable is a deterministic, scripted recorder that captures the entire demo
timeline (all 10 beats + the Terra/Sol/Luna model interactions) into a committed
cassette bank, plus a **no-live** verification test that replays that bank and
asserts every interaction hits. After the single live pass, everything (CI, dev,
the demo) runs from the bank at `$0`, offline.

**The key enabler:** a cassette's **key is computed from the request, not the model
response** (Luna: SHA-256 of the exact `RawEventJSON`; Sol/Terra: COP revision +
evidence set + insights + `requested_by`). So **all** the hard parts — fingerprint
stability, Sol insight hydration, evidence exact-match, reaching COP rev 9 — are
**response-independent and can be fully validated with stub responses, no OpenAI.**
The one live run only fills in *real response content* behind requests whose
identity you already proved stable offline.

**Why scripted:** Replay is fingerprint-sensitive (see §2). A hand-driven record run
is not reproducible — the request identity must live in a **single committed
manifest** that the offline loop, the live recorder, and the replay-verifier all
consume, so requests are byte-identical across all three.

**Workflow:**
1. **Offline loop (§2.5, no live):** drive the manifest with **stub** model clients;
   iterate until record→bank→replay hits for every step and keys are stable. This is
   where the beats get designed and the kinks get worked out.
2. **One live run (§3b):** swap stubs → real OpenAI client, run the *same* manifest
   once in record mode → banks real responses. Keys already validated ⇒ guaranteed
   replay hits.
3. **Forever after (§3c, no live):** committed bank + replay verifier in CI.

**Branch:** `feat/v0.4-pluggable-event-spine`
**Prereqs (already landed):** live OpenAI path for Terra/Sol/Luna + cassette record/
replay spine — commits `6c2ad38`, `660a347`; see
[live-openai-strict-schema-parcels.md](live-openai-strict-schema-parcels.md).

---

## 0. Context you must know before starting

### The demo UI does NOT call the live model endpoints
"Refresh advice" in the UI calls **`GET /api/v1/advisories`** (re-poll), *not*
`/operator/analyze|brief|interpret`
([ui/src/lib/IncidentWorkspace.svelte:148](../../ui/src/lib/IncidentWorkspace.svelte)).
The board is driven entirely by the **deterministic fixture Play** pipeline. The
live model calls (Terra/Sol/Luna) happen only on the `/api/v1/operator/*` routes,
which are driven by a **script**, not the UI.

**Implication / decision to confirm with the requester:** banking per-beat model
output makes the demo *replayable and regression-testable*, but the audience only
**sees** that output if something surfaces it. Options (pick one, out of scope for
the recorder itself but note the dependency):
- (a) Recorder is a **verification/regression asset** only — proves the live path
  works and stays stable, demo keeps running on fixtures. *Lowest effort.*
- (b) Add a demo/replay UI affordance (or CLI narration) that issues the same
  scripted operator requests so the audience sees real replayed model output.
  *Separate parcel.*

This task delivers the recorder + bank + no-live verification (works for both (a)
and (b)). (b)'s UI wiring is a follow-up.

### The beats (deterministic; from `datasets/domestic-disturbance/scenario.json`)
10 beats, each mapped to a raw event present in
`datasets/domestic-disturbance/raw-events.json`:

| # | beat_id | raw_event_id | content_type |
|---|---------|--------------|--------------|
| 1 | baseline-01-911-call | raw-domestic-001-call | text/plain |
| 2 | baseline-02-welfare-check | raw-domestic-002-welfare | text/plain |
| 3 | baseline-03-weather-alert | raw-domestic-003-weather | text/plain |
| 4 | baseline-04-road-closure | raw-domestic-004-main-road | text/plain |
| 5 | baseline-05-ems-availability | raw-domestic-005-ems-available | text/plain |
| 6 | baseline-06-officer-update | raw-domestic-006-officer-update | text/plain |
| 7 | fixture-07-repaired-incomplete-road | raw-domestic-007-incomplete-road | text/plain |
| 8 | fixture-08-quarantined-input | raw-domestic-008-invalid-input | application/json |
| 9 | fixture-09-late-delivery | raw-domestic-009-late-ems | text/plain |
| 10 | fixture-10-road-correction | raw-domestic-010-road-correction | text/plain |

Play advances the COP to **state_revision 9**. Terra/Sol advisories in the fixture
timeline land around rev 7 (active) / rev 9 (superseded).

### The three live endpoints and what they bank
- `POST /api/v1/operator/interpret` → **Luna** (normalize a raw event). Cassette key
  `luna/{raw_event_id}[/{beat_id}]/{hash16}`.
- `POST /api/v1/operator/analyze` → **Terra** (assess COP + evidence). Key
  `terra/rev{N}/{hash16}`.
- `POST /api/v1/operator/brief` → **Sol** (briefing; needs `X-Mosaic-Demo-Identity:
  supervisor-demo` + a hydrated insight id). Key `sol/rev{N}/{hash16}`.

---

## 1. "Capture all the beats" — recording plan

Record **one Luna interpret per beat** (all 10 raw events → 10 Luna cassettes,
keyed with the `beat_id` segment so each beat has its own bank entry), plus the
Terra and Sol interactions the demo narrative shows:

- **Luna ×10** — for each beat, `POST /operator/interpret` with that beat's exact
  raw event from `datasets/domestic-disturbance/raw-events.json`.
- **Terra ×1 (or more)** — `POST /operator/analyze` at COP rev 9 with the demo's
  evidence set (the access/road insight story from
  [docs/demo-script.md](../../docs/demo-script.md) Step 4).
- **Sol ×1** — `POST /operator/brief` at rev 9, hydrating `insight-domestic-access-001`.

Total live cost of one record pass: ~12 structured calls, ~a few cents.

> If per-beat Luna proves noisy (some beats are non-incident payloads), the manifest
> can subset which beats get a live Luna call — but default to **all 10** per the
> requirement to "capture all the beats."

---

## 2. Determinism is the whole game (read carefully)

Replay only hits when the replay request is **byte-identical** to the recorded one.
Verified failure modes (from hands-on testing):

- **Luna** keys on the SHA-256 of the exact `RawEventJSON` bytes
  (`json.Marshal(gen.RawEvent)` on the adapter path). A re-serialized or hand-edited
  envelope misses (observed: `no recording for key luna/…/dc5db… ` vs banked `…f74c83…`).
- **Sol/Terra** validate evidence by **exact `{target_kind, target_id, json_pointer,
  explanation}` set match** (`sameEvidence` in
  [internal/sol/service.go:353](../../internal/sol/service.go),
  [internal/terra/service.go](../../internal/terra/service.go)). Observed: replaying
  with `explanation:"v"` failed because the record used `explanation:"live bank
  evidence"`. **Explanation text is part of the identity — it must be fixed in the
  manifest.**
- **COP revision** must be stable — guaranteed because Play is deterministic
  (`MOSAIC_SEED_ON_START=0`, same dataset → rev 9 every time).
- `requested_by` (Sol) is part of the fingerprint — fixed to `supervisor-demo`.

**Therefore:** every request field that feeds a fingerprint or an exact-match check
must be a literal constant in the manifest. No `time.Now()`, no random ids, no
UI-typed free text at record time. The manifest is the contract.

---

## 2.5 Offline development loop (NO live runs — this is where the beats get designed)

Design and debug the whole pipeline with **stub model clients** that return canned,
schema-valid responses. This exercises the *real* Play, the *real* operator handlers
(hydration, evidence match, COP recovery), the *real* cassette record→FileStore→
replay path, and the *real* key/fingerprint computation — with **zero OpenAI calls**.

**Enabling seam (small change):** `composeModels` already injects stub inners for
Terra and Sol via `testLiveTerra` / `testLiveSol`
([cmd/mosaicdemo/models.go:60](../../cmd/mosaicdemo/models.go)). **Add the missing
`testLiveLuna` seam** (an `openaimodel.LunaStructuredClient` stub slot) so all three
agents can be driven offline. Alternatively, wire an `MOSAIC_OPENAI_BASE_URL`
override into the live `openaimodel.Config` (the transport already accepts
`Endpoint`) and point it at a tiny local mock Responses server — pick whichever fits
the harness; the stub seam is less plumbing for a Go test.

**What the offline loop proves (the actual kinks):**
1. **Play determinism** — every run reaches COP `state_revision 9` with the same COP
   hash (seeded off, fixed dataset, `MOSAIC_SIM_BEAT_SPACING=1ms`).
2. **Hydration** — Sol's `hydrateOperatorInsights` resolves `insight-domestic-access-001`
   from the store at rev 9 (present after Play).
3. **Evidence exact-match** — each Terra/Sol step's canned response cites exactly the
   manifest's evidence set (`sameEvidence` passes). Tune the stub to echo the
   manifest evidence so this is green by construction.
4. **Key stability** — run the manifest twice; assert identical cassette keys both
   times, and that the **record-path key == replay-path key** for every step (no
   `ErrReplayMiss`). This is the guarantee that makes the single live run safe.

**Definition of done for the offline loop:** stub record → stub replay hits 100% of
manifest steps, keys stable across repeated runs, no misses. Only then spend on live.

> The stub bank is disposable (canned content). It validates *identity and flow*, not
> model quality. The committed CI bank comes from the single live run (§3b).

---

## 3. Deliverables

### 3a. Request manifest (committed, single source of truth)
A JSON file, e.g. `testdata/demo/recording-manifest.json`, listing every step in
demo order with fully literal payloads. Suggested shape:

```json
{
  "scenario": "domestic-disturbance",
  "expected_cop_revision": 9,
  "supervisor_identity": "supervisor-demo",
  "steps": [
    { "kind": "play" },
    { "kind": "luna", "beat_id": "baseline-01-911-call",
      "raw_event_ref": "raw-domestic-001-call" },
    "... one luna step per beat ...",
    { "kind": "terra", "state_revision": 9,
      "evidence": [ { "kind": "raw_event", "id": "raw-domestic-001-call",
                      "explanation": "initial 911 call" } ],
      "note": "demo refresh advice" },
    { "kind": "sol", "state_revision": 9,
      "insights": [ { "insight_id": "insight-domestic-access-001" } ],
      "evidence": [ { "kind": "raw_event", "id": "raw-domestic-001-call",
                      "explanation": "live bank evidence" } ],
      "note": "demo live incident brief" }
  ]
}
```

- `raw_event_ref` resolves to the exact raw event object from the dataset — do NOT
  re-encode payloads by hand; load the dataset event and pass its fields verbatim so
  the marshaled bytes match at record and replay.
- All `explanation` strings are fixed literals (identity-bearing).

### 3b. Recorder — ONE live pass (gated, run once, not in CI)
Same harness as the offline loop (§2.5) with the model inner swapped from stub to the
**real** OpenAI client: reads the manifest, starts `mosaicdemo` in **record** mode
with live providers + key, drives Play, issues each step, asserts each banks a
cassette. Because the requests are identical to the already-validated offline loop,
every recorded key matches what replay will compute — the live run cannot introduce a
fingerprint kink, only real response content.

Recommended form: a Go integration entrypoint gated by env (e.g. `MOSAIC_RECORD_LIVE=1`)
or a `cmd/democast` tool, mirroring the existing e2e harness (`startMosaicDemoProgressive`
in [tests/e2e/interactive_simulation_test.go](../../tests/e2e/interactive_simulation_test.go)).
Output: cassette files written under the committed bank path (§3d). Run it **once**;
re-run only if the manifest changes.

### 3c. Replay verifier — **NO LIVE RUNS** (this is the CI/test deliverable)
Reads the **same** manifest, starts `mosaicdemo` in **replay** mode
(`MOSAIC_SIM_MODE=replay`, **no `OPENAI_API_KEY`**) with `MOSAIC_CASSETTE_DIR`
pointed at the committed bank, drives Play, issues every step, and asserts:
- each Luna/Terra/Sol response is `status: ok` (or the intended terminal status),
  with `provider: mosaic-fixture` (proof of no network),
- zero cassette misses (`ErrReplayMiss` fails the test),
- the returned artifact ids / text match a small committed golden (optional but
  recommended for regression).

This test must run in CI with no key and no network. Model it on the existing e2e
tests; it is the primary acceptance gate.

### 3d. Committed cassette bank (not gitignored)
`recordings/` is **gitignored** today. The demo bank the no-live test replays must be
committed. Decision (recommend): store under `testdata/demo/cassettes/` (or
`tests/e2e/testdata/demo-cassettes/`) and ensure that path is NOT covered by the
`.gitignore` `recordings` rule. Keep the human-readable dump files out of it — only
the FileStore-keyed cassettes (`{agent}/…/{hash16}.json`) belong there.

---

## 4. Acceptance criteria

- [ ] Manifest committed; covers Play + all 10 beats' Luna + Terra + Sol.
- [ ] **Offline loop (§2.5) green with NO live calls:** stub record→replay hits 100%
      of steps, keys stable across repeated runs, zero `ErrReplayMiss`. `testLiveLuna`
      seam (or endpoint override) added so all three agents run offline.
- [ ] Recorder produces the bank from **one** live pass; documented, gated, not in CI.
- [ ] **No-live replay test passes with no `OPENAI_API_KEY` and no network**, asserting
      every step hits (no `ErrReplayMiss`) and returns the expected status; runs in CI.
- [ ] Committed cassette bank under a non-gitignored testdata path; contains only
      keyed cassettes (no operator response dumps polluting `FileStore.List`).
- [ ] Authored `ontology/*` unchanged; no new gitignore leak of secrets.
- [ ] Doc updated: how to re-record (live) vs how to verify/replay (no live).
- [ ] Deterministic: running the replay test twice yields identical hits.

---

## 5. Operational notes (verified on this machine)

### Record (deliberate, spends ~cents)
```bash
go build -o mosaicdemo.exe ./cmd/mosaicdemo
set -a; source .env; set +a            # OPENAI_API_KEY (never echo it)
export MOSAIC_SIM_MODE=record \
       MOSAIC_LUNA_PROVIDER=live MOSAIC_TERRA_PROVIDER=live MOSAIC_SOL_PROVIDER=live \
       MOSAIC_SEED_ON_START=0 MOSAIC_SIM_BEAT_SPACING=1ms \
       MOSAIC_CASSETTE_DIR="$(cygpath -w "$PWD/testdata/demo/cassettes")"
./mosaicdemo.exe -listen-addr 127.0.0.1:8099 \
  -asset-root "$(cygpath -w "$PWD")" -ui-dir "$(cygpath -w "$PWD/ui")"
# then run the recorder against the manifest
```

### Replay / verify (NO live, $0)
```bash
unset OPENAI_API_KEY
export MOSAIC_SIM_MODE=replay \
       MOSAIC_CASSETTE_DIR="$(cygpath -w "$PWD/testdata/demo/cassettes")"
# start server + run the manifest; expect status ok, provider mosaic-fixture, no misses
```

### Windows gotchas (confirmed)
- The Go binary does **not** resolve MSYS `/c/...` paths — wrap every path passed to
  it in `cygpath -w`.
- `pkill -f mosaicdemo.exe` does NOT match the process (ps shows `mosaicdemo`); stop
  it with `taskkill //F //IM mosaicdemo.exe`.
- Poll `GET /api/v1/simulation/status` until `status == "ended"` after Play before
  issuing operator calls (COP must be at rev 9). See the e2e polling loop.

### Request shapes (copy exactly)
```bash
# Luna (per beat) — use the dataset raw event's exact fields
POST /api/v1/operator/interpret
{ "raw_event_id":"...", "content_type":"...", "payload_bytes_b64":"...",
  "raw_sha256":"...", "source":{...}, "source_occurred_at":"...", "received_at":"..." }

# Terra
POST /api/v1/operator/analyze
{ "evidence":[{"kind":"raw_event","id":"...","explanation":"<fixed>"}], "note":"<fixed>" }

# Sol (header required)
POST /api/v1/operator/brief   Header: X-Mosaic-Demo-Identity: supervisor-demo
{ "insights":[{"insight_id":"insight-domestic-access-001"}],
  "evidence":[{"kind":"raw_event","id":"...","explanation":"<fixed>"}], "note":"<fixed>" }
```

---

## 6. Design decisions (confirmed)

1. **Recorder form** — shared `internal/democast` package + gated Go tests
   (`MOSAIC_RECORD_LIVE=1` for live; default CI runs no-live replay). No separate
   `cmd/democast` required.
2. **Cassette bank location** — `testdata/demo/cassettes/` (committed; outside
   `.gitignore` `/recordings/`).
3. **Per-beat Luna scope** — all 10 beats, including beat 8 quarantined input.
4. **Audience visibility (§0)** — (a) verification/regression asset only for now.

**Operator runbook:** [`docs/runbook/demo-cassette-recorder.md`](../runbook/demo-cassette-recorder.md).

---

## 7. Files & symbols index

| Area | Paths / symbols |
|---|---|
| Beats / raw events | `datasets/domestic-disturbance/scenario.json`, `raw-events.json` |
| Operator endpoints | [internal/api/operator.go](../../internal/api/operator.go) — `handleOperatorInterpret`, `handleOperatorAnalyze`, `handleOperatorBrief`, `hydrateOperatorInsights` |
| Cassette store / keys | [internal/simulation/cassette/](../../internal/simulation/cassette/) — `LunaKey`, `TerraKey`, `SolKey`, `FileStore`, `isBankedRecording` |
| Compose / mode wiring | [cmd/mosaicdemo/models.go](../../cmd/mosaicdemo/models.go) — `applyCassette`, `parseCassetteMode`, `resolveCassetteDir` |
| Evidence exact-match (identity) | [internal/sol/service.go](../../internal/sol/service.go) `sameEvidence`; [internal/terra/service.go](../../internal/terra/service.go) |
| Existing e2e harness to model on | [tests/e2e/interactive_simulation_test.go](../../tests/e2e/interactive_simulation_test.go) — `startMosaicDemoProgressive`, status polling |
| Demo narrative | [docs/demo-script.md](../../docs/demo-script.md) |
| Cost-safe env template | [.env.example](../../.env.example) |
| `.gitignore` `recordings` rule | [.gitignore](../../.gitignore) (bank must live outside it) |

---

## 8. Non-goals

- No changes to authored `ontology/*` schemas.
- No live calls in CI or the default test path — recording is a manual, gated step.
- UI wiring to display replayed output (decision §0/§6 option b) is a separate parcel.
