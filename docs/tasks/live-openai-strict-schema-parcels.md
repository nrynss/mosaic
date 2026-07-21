# Task: Make the live OpenAI path work end-to-end (Terra ✅, Sol + Luna parcels)

**Status:** Terra live path fixed and shipped. Sol and Luna live paths blocked by
two *independent* issues, each scoped as its own parcel below. This document is a
self-contained handoff — a fresh agent should be able to act on it without prior
conversation context.

**Branch:** `feat/v0.4-pluggable-event-spine`
**Shipped commit (context):** `6c2ad38` — `fix(openai): infer type on const/enum schema leaves for strict mode`

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
by `strictCompatibleSchema` / `makeSchemaStrict`
([internal/openaimodel/schema.go](../../internal/openaimodel/schema.go)). **The
authored ontology schemas are the source of truth for all internal validation and
must never be modified for OpenAI's sake** — only the wire copy is transformed.

Model output is always re-validated against the *authored* schema after the call
(ingestion / service validators), so the wire schema does not need to be as strict
as the authored one — it only needs to (a) be accepted by OpenAI strict mode and
(b) guide the model well enough. This is the key lever for the Luna parcel.

---

## 1. What was fixed and shipped (commit `6c2ad38`) — for context

**Root cause found:** OpenAI strict structured-output mode requires **every schema
node to declare a `type`**. Authored ontology schemas legally omit `type` when a
`const` or `enum` already pins the value, e.g.:

```json
"schema_version": { "const": "1.0.0" }          // no "type"
"lifecycle_status": { "enum": ["active","obsolete"] }   // no "type"
```

OpenAI rejects the whole request:

```
HTTP 400 invalid_json_schema
"Invalid schema for response_format 'mosaic_insight_v1_0_0':
 In context=('properties','schema_version'), schema must have a 'type' key."
```

Because `schema_version` (a const) is in every agent's schema, **all three live
agents failed with HTTP 400**. The transport also discards the OpenAI error body
([transport.go:165-167](../../internal/openaimodel/transport.go)), so it only ever
surfaced as an opaque "openai request failed with HTTP 400".

**Fix (shipped):** `inferStrictLeafType` in `schema.go`, called from
`makeSchemaStrict`, assigns an inferred `type` (string / integer / boolean, or a
type array for mixed enums) to any `const`/`enum` leaf lacking one. Plus tests:
`TestStrictSchemaInfersTypeForConstAndEnumLeaves` and the regression guard
`TestStrictSchemaLeavesNoUntypedConstOrEnum`.

**Verified live:** Terra now returns a valid `Insight` (HTTP 200) against the real
API. Confirmed real OpenAI (not a mock): 404 on bad path, 401 on bad key,
Cloudflare-fronted, valid TLS. (Aside: requested model `gpt-5.6` is echoed back as
`gpt-5.6-sol` with `"billing":{"payer":"openai"}` — consistent with promotional
credits. Worth confirming the billing arrangement but not a bug.)

---

## 2. Parcel A — Sol interactive brief cannot be driven (endpoint contract gap)

**Sol is NOT blocked by OpenAI schema.** Its output schema
(`recommendation.schema.json`) is the same clean family as Terra's, already covered
by commit `6c2ad38`. Sol never returned an HTTP 400.

**The actual blocker:** `POST /api/v1/operator/brief` requires at least one **valid
active `Insight`**, but the request type can't carry one.

- Request shape: `operatorBriefRequest.Insights []operatorInsightRef`
  ([internal/api/operator.go:29](../../internal/api/operator.go)).
- `operatorInsightRef` only has `insight_id`, `state_revision`, `lifecycle_status`,
  `schema_version` — **not** `assertions` / `evidence` / `confidence` / `created_at`.
- `mapOperatorInsights` ([operator.go:509](../../internal/api/operator.go)) therefore
  builds a `gen.Insight` missing required fields, and `Sol.Brief` validates it and
  fails:

```
status: invalid
failure: validate active Insight "insight-domestic-access-001":
  missing properties 'state_revision','assertions','evidence','confidence','created_at'
```

- Passing *no* insights fails differently: `"at least one active Insight is required"`.
- The demo UI does **not** call `/brief` (Sol's demo advisory comes from progressive
  Play / the domain process, not this interactive endpoint).

**Recommended fix (small):** have `handleOperatorBrief` **hydrate** the full insight
from the recovered COP / store by `insight_id`, instead of trusting the request to
carry a complete insight. The COP is already recovered in the handler
(`s.recoverCOP`) and contains the active insights. Map the incoming `insight_id`
refs to the corresponding full insights from the COP, then validate those.

**Acceptance criteria (Parcel A):**
- `POST /api/v1/operator/brief` with `X-Mosaic-Demo-Identity: supervisor-demo`,
  a body referencing an active insight id present in the COP (e.g.
  `insight-domestic-access-001` after a Play) and at least one evidence ref,
  returns `status: ok` on the fixture path and a valid `Recommendation` on the live
  path.
- No regression to the decision-boundary guarantees (`executed:false`, audit append).
- Add/extend an API test covering hydrate-from-COP.

---

## 3. Parcel B — Luna needs a strict-compatible wire schema (design change)

**Luna is genuinely blocked by a chain of OpenAI strict-mode incompatibilities** in
its composed wire schema (built by `loadLunaStructuredOutputSchema` in
[schema.go](../../internal/openaimodel/schema.go), which wraps `luna-result.schema.json`
+ `canonical-event.schema.json` into `{result, canonical_event|null}`). Each issue
below was discovered only after fixing the previous one:

| # | Issue | Where | Strict-mode rule |
|---|-------|-------|------------------|
| 1 | const/enum without `type` | all schemas | **Fixed by `6c2ad38`** |
| 2 | `allOf` + `if`/`then` discriminated union (payload keyed by `event_type`) | `canonical-event.schema.json` (6 branches), `luna-result.schema.json` | `allOf`/`if`/`then` forbidden |
| 3 | `$defs` ref-scoping when embedding two schemas into one wrapper — inner `#/$defs/confidence` refs resolve against the wrapper root, which has no `$defs` | `loadLunaStructuredOutputSchema` wrapper | `$ref` must resolve |
| 4 | typeless `{}` field (`repair.fields[].original`, an intentional "any") | `luna-result.schema.json` `$defs.repair.properties.fields.items.properties.original` | every node needs a `type` |

Exact OpenAI errors captured (for reference):
```
#2: "In context=('properties','canonical_event','anyOf','0'), 'allOf' is not permitted."
#3: "In context=(...'canonical_event','anyOf','0','properties','confidence'),
     reference to component '#/$defs/confidence' which was not found in the schema."
#4: "In context=(...'fields','items','properties','original'), schema must have a 'type' key."
```

**Why this is a parcel, not a patch:** An in-session attempt to fix 2–4 generically
(flatten `allOf`/`if`/`then` → `anyOf`, hoist and namespace `$defs`, drop `$id`)
**broke existing tests** — `TestLunaNormalizeMapsResultAndOptionalCanonical` and
`TestPromptEvalHarness/luna/*` assert the standalone schema keeps its `$id`
(`assertAuthoredSchema` in [openaimodel_test.go](../../internal/openaimodel/openaimodel_test.go)).
Hoisting `$defs` to the wrapper root conflicts with keeping `$id` on the embedded
bodies (a JSON Schema `$id` establishes a new base URI, so `#/$defs/...` would
re-scope to the body and break again). That tension = design change + test rework.
**All of that attempt was reverted;** the tree currently contains only the clean B
fix.

### Recommended approach (Parcel B)

Build a **purpose-designed, self-contained strict-compatible Luna wire schema**
rather than mechanically transforming the authored composite. Design constraints:

- One flat JSON Schema document (single `$defs` table at root, or none), no
  cross-schema embedding of two `$id`-bearing documents.
- No `allOf` / `if` / `then` / `else`. Represent the `payload` discriminated union
  as a single `anyOf` of the concrete payload object shapes (incident, incident-
  resolved, unit, resource, road, weather). Exact per-`event_type` discrimination is
  re-enforced afterward by Mosaic's authored-schema validation in ingestion.
- No typeless nodes. Give `repair.fields[].original` a concrete strict type — a
  scalar union like `["string","number","integer","boolean","null"]` is acceptable
  since Mosaic re-validates the real value against the authored `{}` (accept-any)
  afterward.
- Every object: `additionalProperties:false`, all keys in `required`, optional keys
  made nullable (this part already works via `makeSchemaStrict`).

Alternative worth evaluating first (may be simpler): **send Luna with
`strict:false`.** The transport currently hardcodes `Strict:true`
([transport.go:138](../../internal/openaimodel/transport.go)). Making `strict`
per-agent and setting Luna to `false` *may* let OpenAI accept a looser schema —
BUT note issue #3 (unresolved `$ref`) is a hard schema error independent of strict
mode, so the `$defs` composition still needs fixing even with `strict:false`. Prototype
this against the real API before committing to it.

**Do NOT modify the authored `ontology/*.schema.json` files.** Keep them as the
validation source of truth; do all shaping in `openaimodel`.

**Acceptance criteria (Parcel B):**
- `loadLunaStructuredOutputSchema` produces a document OpenAI accepts (no 400) — add
  a regression test asserting no forbidden keyword (`allOf`/`if`/`then`/`else`) and
  no untyped node survive, and that every `$ref` resolves within the document.
- Existing tests updated to match the new wire schema shape (the ones that assert
  `$id` today).
- Verified live: `POST /api/v1/operator/interpret` with a real key returns a valid
  `LunaResult` (and optional canonical event), `status: ok`, banked to cassette.
- Authored ontology schemas unchanged (`git diff ontology/` is empty).

---

## 4. How to reproduce / test the live path (operational notes)

### Safe by default
The repo's `.env` is set to **fixture** safe-mode (`MOSAIC_SIM_MODE=fixture`,
providers `fixture`) so a funded key can't spend by accident. See
[.env.example](../../.env.example) for the SAFE vs SPENDS blocks. A live pass is a
**deliberate override**.

### Run a live record pass (spends a few cents)
Build and run the binary directly with an explicit live env (bypasses `.env`
safe-mode). On Windows use `cygpath -w` for paths passed to the Go binary:

```bash
go build -o /tmp/mosaicdemo.exe ./cmd/mosaicdemo
set -a; source .env; set +a            # loads OPENAI_API_KEY (do not echo it)
export MOSAIC_SIM_MODE=record \
       MOSAIC_LUNA_PROVIDER=live MOSAIC_TERRA_PROVIDER=live MOSAIC_SOL_PROVIDER=live \
       MOSAIC_CASSETTE_DIR="$(cygpath -w /tmp/cassettes)" \
       MOSAIC_DB_PATH="$(cygpath -w /tmp/rec.db)" \
       MOSAIC_SIM_BEAT_SPACING=1ms
/tmp/mosaicdemo.exe -listen-addr 127.0.0.1:8099 \
  -asset-root "$(cygpath -w "$PWD")" -ui-dir "$(cygpath -w "$PWD/ui")" &
```

Then drive it (Play first to build a COP at revision 9):

```bash
B=http://127.0.0.1:8099
curl -s -X POST $B/api/v1/simulation/start >/dev/null; sleep 3   # fixture-driven, no model cost

# Terra (works today): evidence ref that exists after Play
curl -s -X POST $B/api/v1/operator/analyze -H 'Content-Type: application/json' \
  -d '{"evidence":[{"kind":"raw_event","id":"raw-domestic-001-call","explanation":"v"}],"note":"v"}'

# Sol (Parcel A): requires supervisor identity header + a valid insight
curl -s -X POST $B/api/v1/operator/brief -H 'Content-Type: application/json' \
  -H 'X-Mosaic-Demo-Identity: supervisor-demo' \
  -d '{"insights":[{"insight_id":"insight-domestic-access-001"}],
       "evidence":[{"kind":"raw_event","id":"raw-domestic-001-call","explanation":"v"}],"note":"v"}'

# Luna (Parcel B): raw-event envelope
curl -s -X POST $B/api/v1/operator/interpret -H 'Content-Type: application/json' \
  -d '{"raw_event_id":"raw-live-verify-001","content_type":"text/plain",
       "payload_bytes_b64":"<base64>","raw_sha256":"<sha256 hex>",
       "source":{"source_id":"sim","source_record_id":"src-1"},
       "source_occurred_at":"2026-07-18T10:00:00Z","received_at":"2026-07-18T10:00:01Z"}'
```

Key facts:
- Supervisor identity header const: `X-Mosaic-Demo-Identity` = `supervisor-demo`
  ([internal/api/server.go:28](../../internal/api/server.go),
  [internal/reference/domesticdisturbance/profile.go:95](../../internal/reference/domesticdisturbance/profile.go)).
- Play uses **fixture** events (no live Luna per beat); the live model calls happen
  only on the three `/operator/*` endpoints above.
- **Windows gotcha:** the Go binary does not resolve MSYS `/c/...` paths — use
  `cygpath -w`. And `pkill -f mosaicdemo.exe` does NOT match the running process
  (ps shows it as `mosaicdemo`); kill with `taskkill //F //IM mosaicdemo.exe`.

### See the real OpenAI error body (transport swallows it)
The transport reports only `HTTP <code>`. To debug a 400, temporarily add to the
non-2xx branch in [transport.go](../../internal/openaimodel/transport.go) (revert
before committing):

```go
if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
    errBody, _ := io.ReadAll(io.LimitReader(response.Body, 8192))
    if os.Getenv("MOSAIC_DEBUG_DUMP_DIR") != "" {
        fmt.Fprintf(os.Stderr, "\n===DEBUG model=%s status=%d schema=%s\n===RESP: %s\n===END\n",
            t.model, response.StatusCode, call.SchemaName, string(errBody))
    }
    return structuredResult{}, fmt.Errorf("openai request failed with HTTP %d", response.StatusCode)
}
```

Consider landing a **permanent, safe** version of this (log the OpenAI error body to
stderr, never the request/prompt/key) as a small side-improvement — the opaque
"HTTP 400" cost real debugging time.

### Cost & demo posture
- A live structured call is ~2–3k tokens (~a cent). Rejected 400s are unbilled.
- Once recorded, flip `MOSAIC_SIM_MODE=replay` to replay banked real output at $0,
  offline, deterministic — the intended demo posture. Fixture mode also stays $0.

---

## 5. Files & symbols index

- Wire-schema transform: [internal/openaimodel/schema.go](../../internal/openaimodel/schema.go)
  — `strictCompatibleSchema`, `makeSchemaStrict`, `inferStrictLeafType` (shipped),
  `loadStructuredOutputSchema`, `loadLunaStructuredOutputSchema` (Parcel B focus).
- Transport / strict flag / error handling: [internal/openaimodel/transport.go](../../internal/openaimodel/transport.go).
- Agent clients: `terra.go`, `sol.go`, `luna.go` in the same package.
- Operator endpoints: [internal/api/operator.go](../../internal/api/operator.go)
  — `handleOperatorAnalyze` (Terra), `handleOperatorBrief` (Sol, Parcel A),
  `handleOperatorInterpret` (Luna), `mapOperatorInsights`, `mapOperatorEvidence`.
- Provider/mode selection: [cmd/mosaicdemo/models.go](../../cmd/mosaicdemo/models.go).
- Authored schemas (DO NOT edit for OpenAI): `ontology/insight.schema.json`,
  `ontology/recommendation.schema.json`, `ontology/luna-result.schema.json`,
  `ontology/canonical-event.schema.json`.
- Tests to mind: `internal/openaimodel/openaimodel_test.go`
  (`assertAuthoredSchema`, `TestStrictSchema*`, `TestLunaNormalizeMapsResultAndOptionalCanonical`,
  `TestPromptEvalHarness`).
