# Task: Make the live OpenAI path work end-to-end (Terra, Sol, Luna)

**Status:** ✅ **Complete** (Terra previously shipped; Sol + Luna parcels landed
on `feat/v0.4-pluggable-event-spine`). Live operator paths work for all three
agents; Luna/Terra/Sol share the same cassette record/replay spine.

**Branch:** `feat/v0.4-pluggable-event-spine`

**Shipped context:**
- `6c2ad38` — `fix(openai): infer type on const/enum schema leaves for strict mode` (Terra unblocked)
- This pass — Sol brief hydration, Luna strict wire schema, transport error detail,
  Luna cassette parity with Terra/Sol

This document is a self-contained handoff and post-implementation record.

---

## 0. Background: how the live model path works

Mosaic has three model agents. Each has a fixture path and a live path:

- **Luna** — normalizes a raw event into a canonical event (`/api/v1/operator/interpret`).
- **Terra** — derives an `Insight` from the COP (`/api/v1/operator/analyze`).
- **Sol** — produces a `Recommendation` briefing (`/api/v1/operator/brief`).

Live is selected only when **both** are true per agent (see
[cmd/mosaicdemo/models.go](../../cmd/mosaicdemo/models.go) `effectiveSelection`):
1. `MOSAIC_<AGENT>_PROVIDER=live`, and
2. `OPENAI_API_KEY` is set.

Live calls go through the OpenAI **Responses API** (`https://api.openai.com/v1/responses`)
with `text.format.type = "json_schema"` and **`strict: true`**
([internal/openaimodel/transport.go](../../internal/openaimodel/transport.go)).

The wire JSON schema is derived from the authored ontology schemas in `ontology/`
by `strictCompatibleSchema` / `makeSchemaStrict` (Terra/Sol) or
`buildLunaStrictWireSchema` (Luna)
([internal/openaimodel/schema.go](../../internal/openaimodel/schema.go)). **The
authored ontology schemas are the source of truth for all internal validation and
must never be modified for OpenAI's sake** — only the wire copy is transformed.

Model output is always re-validated against the *authored* schema after the call
(ingestion / service validators), so the wire schema does not need to be as strict
as the authored one — it only needs to (a) be accepted by OpenAI strict mode and
(b) guide the model well enough.

### Cassette modes (all three agents)

| `MOSAIC_SIM_MODE` | Behaviour |
|---|---|
| `fixture` / `passthrough` / empty | No cassette; fixtures (or refuse clients for interactive Terra/Sol) |
| `live` / `record` | Call live inners when provider+key say live; bank each successful response |
| `replay` / `recorded` | Store only — **no network**, no API key required; miss → `ErrReplayMiss` |

Cassette directory: `MOSAIC_CASSETTE_DIR` (default: `$TEMP/mosaic-recordings`).
`/recordings/` is gitignored for local banks.

Composition: [cmd/mosaicdemo/models.go](../../cmd/mosaicdemo/models.go)
`applyCassette` wraps Luna + Terra + Sol structured clients.

---

## 1. What was fixed earlier (commit `6c2ad38`) — Terra

**Root cause:** OpenAI strict mode requires every schema node to declare a
`type`. Authored ontology schemas omit `type` when `const`/`enum` pin the value
(e.g. `"schema_version": { "const": "1.0.0" }`). OpenAI returned HTTP 400
`invalid_json_schema` for all three agents.

**Fix:** `inferStrictLeafType` in `schema.go` assigns an inferred type on
const/enum leaves in the wire copy only.

**Verified live:** Terra returns a valid `Insight` (HTTP 200).

---

## 2. Parcel A — Sol interactive brief (✅ done)

### Problem
`POST /api/v1/operator/brief` accepted bare `insight_id` refs. `mapOperatorInsights`
built incomplete `gen.Insight` values missing required fields; Sol validation failed:

```
validate active Insight "…": missing properties 'assertions','evidence','confidence','created_at'
```

Sol was **not** blocked by OpenAI schema (same clean family as Terra after `6c2ad38`).

### Fix
`handleOperatorBrief` hydrates full insights from **advisory history** (durable
store) by `insight_id`, bounded by recovered COP revision:

- Symbol: `hydrateOperatorInsights` in [internal/api/operator.go](../../internal/api/operator.go)
- Clients still send only `{"insights":[{"insight_id":"…"}]}`
- Missing / future / duplicate ids → `400 invalid_insights`
- Prefer newest insight version with `state_revision ≤ COP revision`

### Acceptance
- Fixture path: operator API tests seed an insight into the store and assert Sol
  receives full hydrated fields (`TestOperatorBriefSuccessWithSupervisor`,
  `TestOperatorBriefHydrateMissingInsight`).
- Live path (record mode): `status: ok`, `executed: false`, recommendation banked
  under the Sol cassette key.

### Cassette banked (local, gitignored example)
```
recordings/sol/rev9/<hash16>.json
```
Key shape: `sol/rev{N}/{request_hash16}` (fingerprint includes COP hash, evidence
ids, insight ids, `requested_by`).

---

## 3. Parcel B — Luna strict wire schema (✅ done)

### Problem
Luna’s composed wire schema failed OpenAI strict mode after const/enum typing:

| # | Issue | OpenAI rule |
|---|-------|-------------|
| 1 | const/enum without `type` | Fixed by `6c2ad38` |
| 2 | `allOf` + `if`/`then` | Forbidden composition keywords |
| 3 | `$defs` ref-scoping when embedding two `$id` documents | `$ref` must resolve |
| 4 | typeless `{}` (`repair.fields[].original` / `replacement`) | every node needs `type` |

Mechanical transform of authored composites broke tests that asserted authored
`$id` on embedded bodies. Approach: **purpose-built wire schema**, authored
files untouched.

### Fix
`buildLunaStrictWireSchema` / `loadLunaStructuredOutputSchema` in
[internal/openaimodel/schema.go](../../internal/openaimodel/schema.go):

1. Deep-copy authored `luna-result` + `canonical-event`
2. Hoist `$defs` to one root table with `luna_` / `canon_` prefixes
3. Strip `allOf` / `if` / `then` / `else` / `not` (re-validated by Mosaic after call)
4. Type bare `{}` as scalar union `string|number|integer|boolean|null`
5. Expand `payload` to `anyOf` of the six concrete payload defs
6. Rewrite `#/$defs/…` refs to namespaced keys; drop `$id` / `$schema` / nested `$defs`
7. Run `makeSchemaStrict` on the wrapper `{ result, canonical_event|null }`

Tests: `TestLunaWireSchemaIsOpenAIStrictCompatible`, updated
`TestLunaNormalizeMapsResultAndOptionalCanonical`, prompt-eval wire assertions.

### Acceptance
- Authored `ontology/*` unchanged
- Live interpret accepted by OpenAI; valid `LunaResult` (+ optional canonical)

### Side improvement — transport errors
Non-2xx OpenAI responses now include a **sanitized** error body snippet in the
error string and on stderr (`model`, `schema`, detail only — never request,
prompt, or key). See `extractOpenAIErrorMessage` / `sanitizedErrorMessage` in
[transport.go](../../internal/openaimodel/transport.go).

---

## 4. Parcel C — Luna cassette parity with Terra/Sol (✅ done)

### Problem
Terra and Sol were banked by `internal/simulation/cassette`. Luna live calls
returned HTTP results only — nothing for `$0` replay.

### Fix
Same decorator pattern as Terra/Sol:

| Piece | Location |
|---|---|
| `LunaCassette` | [internal/simulation/cassette/luna.go](../../internal/simulation/cassette/luna.go) |
| `LunaKey` | [key.go](../../internal/simulation/cassette/key.go) |
| `result_json` / `canonical_event_json` on `Recording` | [recording.go](../../internal/simulation/cassette/recording.go) |
| `AgentLuna` provenance | [provenance.go](../../internal/simulation/cassette/provenance.go) |
| Wire into compose | [cmd/mosaicdemo/models.go](../../cmd/mosaicdemo/models.go) `applyCassette` |

**Key shape (Luna is not COP-revision-scoped):**
```
luna/{raw_event_id}[/{beat_id}]/{request_hash16}
```
Fingerprint: `agent`, `raw_event_id`, `raw_json_sha256` (SHA-256 of
`RawEventJSON` bytes as presented), optional `beat_id`.

**Compose behaviour:**
- **Record:** wrap each live agent (Luna and/or Terra and/or Sol); demote to
  passthrough only when *no* agent is live
- **Replay:** force all three construct selections to fixture (no API key);
  wrap with ModeReplay + nil inner; `liveLunaAdapter` still maps banked JSON →
  `LunaResult` / `ModelRun` with **fixture provider labels** (honest, not
  `"openai"`)
- **Passthrough:** no decorator

**FileStore.List hardening:** only files with non-empty `schema_version`, `key`,
and `agent` count as recordings. Operator response dumps or debug JSON living
under the cassette dir are ignored; corrupt envelopes that *claim* to be
recordings still error.

### Cassette banked (local, gitignored example)
```
recordings/luna/raw-live-bank-full-001/<hash16>.json
```
Contains `result_json` (e.g. `status: accepted`), `canonical_event_json`
(`incident_reported`), prompt provenance, and OpenAI `response_id`.

### Replay (no key, $0)
```bash
export MOSAIC_SIM_MODE=replay
export MOSAIC_CASSETTE_DIR=/path/to/recordings   # Windows: absolute path
# OPENAI_API_KEY not required
# Same operator request identity → store hit; miss → ErrReplayMiss (no silent live)
```

---

## 5. How to record / verify (operational notes)

### Safe by default
Repo `.env` uses fixture providers. A live/record pass is a deliberate override.
See [.env.example](../../.env.example).

### Record pass (spends a few cents; Play beats stay fixture)
```bash
# Build
go build -o mosaicdemo ./cmd/mosaicdemo   # or .exe on Windows

# Env (do not echo the key)
export OPENAI_API_KEY=…                  # from .env
export MOSAIC_SIM_MODE=record
export MOSAIC_LUNA_PROVIDER=live
export MOSAIC_TERRA_PROVIDER=fixture     # optional: live only if you need Terra bank
export MOSAIC_SOL_PROVIDER=live
export MOSAIC_CASSETTE_DIR="$PWD/recordings"
export MOSAIC_SIM_BEAT_SPACING=1ms
export MOSAIC_SEED_ON_START=0

./mosaicdemo -listen-addr 127.0.0.1:8099 -asset-root "$PWD" -ui-dir "$PWD/ui"
```

Drive (Play first → COP rev 9; then operator routes):

```bash
B=http://127.0.0.1:8099
curl -s -X POST $B/api/v1/simulation/start >/dev/null; sleep 3

# Sol — hydrate insight from store by id only
curl -s -X POST $B/api/v1/operator/brief \
  -H 'Content-Type: application/json' \
  -H 'X-Mosaic-Demo-Identity: supervisor-demo' \
  -d '{"insights":[{"insight_id":"insight-domestic-access-001"}],
       "evidence":[{"kind":"raw_event","id":"raw-domestic-001-call","explanation":"v"}],
       "note":"v"}'

# Luna — structured payload more likely to accept than plain text
curl -s -X POST $B/api/v1/operator/interpret \
  -H 'Content-Type: application/json' \
  -d '{"raw_event_id":"raw-live-bank-full-001","content_type":"application/json",
       "payload_bytes_b64":"<base64 of incident JSON with incident_id/category/location_id>",
       "raw_sha256":"<sha256 hex>",
       "source":{"source_id":"sim","source_record_id":"src-1"},
       "source_occurred_at":"2026-07-18T10:00:00Z","received_at":"2026-07-18T10:00:01Z"}'
```

**Windows notes:** pass Windows paths to the Go binary (not MSYS `/c/...`).
Kill with process name of the built binary if needed.

### Cost
- Live structured call ~2–3k tokens (~a cent). Rejected schema 400s are unbilled.
- Play / progressive beats use fixture events (no live Luna per beat).
- After record: `MOSAIC_SIM_MODE=replay` → $0, offline, deterministic.

---

## 6. Review fixes applied after implementation

| Issue | Fix |
|---|---|
| `FileStore.List` treated any JSON under the dir as a recording (operator dumps could pollute provenance scans) | Skip unless `schema_version` + `key` + `agent` present; hard-fail only on corrupt *envelope-shaped* files |
| Luna `ModelRun` always claimed `provider=openai` under replay | Composition sets fixture labels on `liveLunaAdapter` in ModeReplay |
| `replayPromptVersions` scanned the store twice | Single scan; feed Luna/Terra/Sol prompt versions from one result |
| Docs still said Luna outside cassette / Terra+Sol only | Updated this file, `.env.example`, README |

### Known intentional limits
- Interactive `/operator/interpret` still does **not** `AppendLunaResult` /
  `AppendModelRun` to SQLite — durable **replay** identity is the **cassette**
  bank (same as Terra/Sol interactive path). Ingestion progressive path keeps
  using domain FixtureLuna + fixture outcomes.
- Cassette keys for Luna hash the exact `RawEventJSON` bytes from
  `json.Marshal(gen.RawEvent)` on the adapter path; replaying a hand-edited
  envelope with different key order will miss (fingerprint mismatch).

---

## 7. Files & symbols index

| Area | Paths / symbols |
|---|---|
| Wire schema (Terra/Sol) | `strictCompatibleSchema`, `makeSchemaStrict`, `inferStrictLeafType` |
| Wire schema (Luna) | `loadLunaStructuredOutputSchema`, `buildLunaStrictWireSchema`, hoist/strip/type/rewrite helpers |
| Transport | `transport.call`, `extractOpenAIErrorMessage`, `sanitizedErrorMessage` |
| Sol hydrate | `handleOperatorBrief`, `hydrateOperatorInsights` |
| Cassette | `LunaCassette`, `TerraCassette`, `SolCassette`, `LunaKey`, `FileStore.List` + `isBankedRecording` |
| Compose | `composeModels`, `applyCassette`, `replayPromptVersions`, `liveLunaAdapter` |
| Authored schemas (do not edit for OpenAI) | `ontology/insight`, `recommendation`, `luna-result`, `canonical-event` |
| Tests | `internal/openaimodel/*strict*`, `internal/api/operator_test.go` (brief hydrate), `internal/simulation/cassette/*luna*`, `cmd/mosaicdemo/models_test.go` |

---

## 8. Verification checklist (no live spend required)

```bash
go test ./internal/openaimodel/ ./internal/api/ ./internal/sol/ \
  ./internal/simulation/cassette/ ./cmd/mosaicdemo/ -count=1
```

Optional live (deliberate): one Sol brief + one Luna interpret under
`MOSAIC_SIM_MODE=record`, then confirm files under `$MOSAIC_CASSETTE_DIR/{luna,sol}/`.
Then `MOSAIC_SIM_MODE=replay` and re-issue the **same** requests — expect store
hits, no network.
