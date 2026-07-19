# Mosaic demo dashboard

This is the deliberately small, local Svelte surface for the Mosaic synthetic
demonstration. It is an evidence ledger and public operations receipt, not an
operational command surface: it displays a deterministic COP, preserves the
difference between reported facts and derived claims, and can only create the
existing immutable briefing-request and review audit records with
`executed: false`.

## Design notes

The interface is organized as a case folio rather than a metrics dashboard.
The left rail carries public-demo and connection context; the centre is a
chronological evidence ledger; the right rail resolves one cited artifact at a
time. The ledger spine is intentional: durable canonical order is the product's
real explanatory structure.

- **Palette:** Harbor `#173342`, Paper `#edf3f2`, Ink `#17202a`, Signal
  `#c9872f`, Tide `#8db9c7`, and Review `#7d5266`.
- **Type:** Iowan Old Style / Palatino for contextual headings, Aptos / Segoe
  UI for reading, and Cascadia Code / ui-monospace for IDs and state revisions.
- **Signature:** a single evidence ledger spine whose markers are claim-class
  colors. Opening a marker resolves that item through the public evidence
  API; it does not reveal raw source payload bytes.

The first sketch used a dark command-centre treatment and a grid of status
cards. It was rejected because it made synthetic state look operational and
buried the distinction between observations and assessment. The quieter folio
layout keeps one memorable structural device while leaving enough space for
uncertainty, unavailable data, and the human review boundary.

## Operations receipt design notes

The P18 panel serves a demo evaluator who needs to answer one question: **what
was actually observed and what can this single-instance build truthfully do?**
It is deliberately a receipt from the deterministic system, rather than an
admin dashboard or an agent-control surface.

- **Tokens:** it extends the existing Harbor `#173342`, Paper `#edf3f2`, Ink
  `#17202a`, Signal `#c9872f`, Tide `#8db9c7`, and Review `#7d5266` palette.
  The receipt borrows the ledger’s fine grid and uses Tide, Signal, and Review
  only to distinguish fixture/composed/recovered/unavailable statements.
- **Type:** Iowan Old Style / Palatino keeps contextual headings human-scale;
  Aptos / Segoe UI keeps the explanatory copy legible; Cascadia Code /
  ui-monospace identifies immutable counts, modes, and timestamps without
  suggesting a command terminal.
- **Layout:** the operations receipt follows the COP ledger, so a reader first
  sees the source-derived state and then its bounded operational provenance.
  Its compact sequence is timestamped observation → durable/lifecycle counts →
  model-run outcome record → capability docket.
- **Signature:** the clipped deterministic recovery stamp is the one visual
  risk. It works like a paper receipt mark: a claim of recovery is conspicuous,
  specific to its observation, and visibly separate from the unavailable
  capabilities around it.

The initial idea used a bank of green status tiles. It was revised because
“healthy” tiles would make a local fixture look like a production control room.
The final docket treats each capability as an evidence statement, names the
missing Terra/Sol transport and reconciliation worker, and never calls an LLM
repair process self-healing.

## Run locally

Requirements: Node.js 20+ and a Mosaic API on `http://127.0.0.1:8080` (or an
equivalent reverse proxy).

```powershell
cd ui
npm install
npm run dev
```

The development server proxies `/api` to `http://127.0.0.1:8080` by default,
so the dashboard starts against the relative API base `/api/v1`. Change the
proxy target for a different local API process:

```powershell
$env:MOSAIC_API_PROXY_TARGET = 'http://127.0.0.1:9090'
npm run dev
```

For a same-origin production build, set the API base only when necessary:

```powershell
$env:VITE_MOSAIC_API_BASE_URL = '/api/v1'
npm run build
```

An absolute API URL is supported by the in-app connection field and build
environment. It must be served with an appropriate browser CORS policy; the
local Vite proxy avoids that requirement in development.

## Checks

```powershell
npm run check
npm run build
```

The dashboard calls the public `health`, `version`, COP, operations, evidence,
stream, briefing, and audit-action endpoints. It adds no browser identity header
or auth control. SSE uses `fetch` rather than `EventSource` so its reader can be
cleanly cancelled and reconnected with bounded backoff; it always accepts the
API's next `cop.snapshot` as authoritative.
