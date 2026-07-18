# Mosaic demo dashboard

This is the deliberately small, local Svelte surface for the Mosaic v0.1
synthetic demonstration. It is an evidence ledger for a demo supervisor, not
an operational command surface: it displays a deterministic COP, preserves the
difference between reported facts and derived claims, and can only create the
existing immutable briefing-request and review audit records.

## Design notes

The interface is organized as a case folio rather than a metrics dashboard.
The left rail carries identity and connection context; the centre is a
chronological evidence ledger; the right rail resolves one cited artifact at a
time. The ledger spine is intentional: durable canonical order is the product's
real explanatory structure.

- **Palette:** Harbor `#173342`, Paper `#edf3f2`, Ink `#17202a`, Signal
  `#c9872f`, Tide `#8db9c7`, and Review `#7d5266`.
- **Type:** Iowan Old Style / Palatino for contextual headings, Aptos / Segoe
  UI for reading, and Cascadia Code / ui-monospace for IDs and state revisions.
- **Signature:** a single evidence ledger spine whose markers are claim-class
  colors. Opening a marker resolves that item through the authenticated evidence
  API; it does not reveal raw source payload bytes.

The first sketch used a dark command-centre treatment and a grid of status
cards. It was rejected because it made synthetic state look operational and
buried the distinction between observations and assessment. The quieter folio
layout keeps one memorable structural device while leaving enough space for
uncertainty, unavailable data, and the human review boundary.

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

The dashboard calls only the P08 public `health` and `version` endpoints plus
the authenticated COP, evidence, stream, briefing, and audit-action endpoints.
SSE uses `fetch` rather than `EventSource`, because the fixed demo identity is a
required request header. It reconnects with bounded backoff and always accepts
the API's next `cop.snapshot` as authoritative.
