# Mosaic Hackathon Demo Script

## DevWeek positioning (say this first)

Mosaic is a **developer framework** for putting generative agents next to a
**deterministic, auditable core** — not a vertical 911 product and not a
black-box “AI ops” app.

**One-line pitch**

> Bring your own deterministic system of record. Mosaic adds pluggable AI
> advice, streaming state, and immutable provenance — without letting models
> mutate operational truth.

**Why this matters to enterprise / government builders**

They do not want AI in a sealed box. They want to know:

- where AI touches their data;
- how it interfaces with systems that must stay deterministic; and
- how to audit every recommendation after the fact.

Mosaic demonstrates those boundaries in running code.

### Four layers (front and center)

| Layer | What it is | What you show |
|-------|------------|---------------|
| **1. Deterministic core** | Ingestion → canonical events → **projector** → **COP** (common operating picture) + **immutable SQLite** history | Board facts and state revision numbers. Only the projector writes operational state. |
| **2. Agent layer (generative)** | **Luna / Terra / Sol** — fixture or live OpenAI, swapped by env, not by rewriting the core | Mode badges; Refresh advice; model runs in Decision history. Agents **propose**; they never dispose. |
| **3. Transport / application** | Bounded **HTTP API** + **SSE** streams (`/api/v1/stream`, `/api/v1/simulation/stream`) | Connected pill; Play scenario; facts updating without a full page reload. |
| **4. Presentation** | This Svelte board is a **reference UI** — a dense CAD-style consumer of the same contracts | “Judge the API contract, not the design agency.” Another client (mobile field app, SOC console, EOC wall) can consume the same streams. |

### Reference UI framing (option B — use this)

Do **not** dismiss the UI as throwaway “slop.” Contextualize it:

> To demonstrate the framework under a high-stakes, dense event flow, we built a
> **CAD-style reference UI**. It is a consumer of our APIs and SSE streams. It
> proves the contracts work with real operator workflows — intake, constraints,
> advice, human handoffs, and provenance. The framework is the layers underneath;
> this screen is one client.

### Bring your own deterministic core (honest scope)

**Architecture intent — not a shipped multi-domain product today.**

- The domestic-disturbance package is a **reference domain profile** (synthetic
  events, projector rules, fixture advisories).
- A developer is meant to plug in **another domain profile / event feed** and
  keep the same agent, audit, and stream contracts.
- We do **not** claim Kubernetes, EHR, or trading are already wired in.

**Same pipeline, different cores (spoken examples):**

| Domain | Deterministic core (theirs) | Mosaic layer (ours) |
|--------|----------------------------|---------------------|
| **Enterprise cybersecurity** | SIEM / asset inventory / ticket state / control plane facts | Agents assess risk; humans approve; every suggestion audited; COP never silently rewritten by an LLM |
| **Government disaster management** | EOC resource tables, hazard feeds, shelter capacity, road/bridge status | Same: streamed picture, advisory agents, handoffs that are recorded not auto-executed |
| **This demo** | Synthetic 911-style incident + environment events | Reference profile so judges can *see* the density of the flow |

### Multi-domain Mosaic (theoretical plug point in this UI)

In the reference UI, **Save maintenance note (demo)** / road–maintenance handoff
is the concrete metaphor for “notify another team or system.”

**Pitch line at that control:**

> Here we only **record** a handoff — `executed: false`, never delivered to a
> real desk. In a multi-domain Mosaic deployment, this is where you’d plug a
> **theoretical next domain or system**: another Mosaic profile, a maintenance
> CMMS, a cyber ticketing bus, an EOC logistics channel. The framework’s job is
> the **immutable intent + provenance**; delivery connectors stay policy-governed
> and outside the agent.

Do not demo a second live domain. Name the **seam**, then continue.

### Three talking points to repeat

1. **State mutability guarantee** — Luna / Terra / Sol cannot mutate the COP.
   They only propose. The deterministic projector disposes.
2. **Provenance as a feature** — SQLite (and the Decision history tab) answers
   *why* advice existed at state revision *N* even after revision *N+1*
   superseded it.
3. **Decoupled client** — SSE and the public API drive the story; the UI is a
   dumb (in the good sense) consumer. Swap the glass; keep the contracts.

### What we never claim

- Multi-tenant hosted platform or “AI ran the operation.”
- Real dispatch, real PII, or real external delivery.
- Durable Cloud Run production (live demo is ephemeral `/tmp` unless noted).
- That multi-domain products already ship — only that the **architecture** is
  aimed at plugging new cores and handoff targets.

---

## Live surfaces

| | |
|--|--|
| **Live URL** | https://mosaic.nryn.dev ([Cloud Run](https://mosaic-demo-358513274447.us-central1.run.app)) |
| **Local** | `docker compose up --build` → http://localhost:8080 |
| **In-app** | **How this works** + **?** tips (operator-facing; this doc is the *pitch*) |

---

## Synthetic data (unchanged story)

Checked-in fixture: `datasets/domestic-disturbance/`

| Item | Detail |
|------|--------|
| Beats | 10 (scenario.json) |
| Story | 911 intake → welfare → weather → road closure → EMS → officer update → incomplete repair → quarantine → late EMS → road open |
| COP end state | State revision **9** with open Brook Lane after correction |
| Advisories | Access-constraint insight (superseded) + obsolete follow-up + recommendation (not current) |
| Integrity demos | Quarantined invalid input; late delivery; repaired-then-opened road |
| Audits | Briefing requested + supervisor ack samples |

Enough for: intake → constraint → judgment → correction → provenance.

---

## Spoken opening (45–60 seconds)

> Mosaic is a framework for **safe AI next to systems that must stay
> deterministic**. Four layers: an immutable core and projector, pluggable
> agents, streaming APIs, and any client UI.
>
> Enterprises and governments — cyber ops, disaster EOCs — need AI that
> **advises without silently rewriting** the system of record. Mosaic keeps
> that boundary explicit.
>
> What you’re looking at is a **CAD-style reference UI** for a synthetic
> high-stakes scenario. Don’t evaluate us as a design studio. Evaluate the
> **API contract**: SSE pushes COP state, agents only propose, humans record
> intent, provenance never lies about execution.
>
> The domestic-disturbance board is a **dummy core** so you can see dense
> events flow. The same pipeline is how you’d wrap a cyber ledger or a
> government disaster operating picture — by plugging a different deterministic
> core, not by rewriting the agent and audit layers.

Point at: scope line (synthetic · nothing sent outside) and **Connected**.
Optional: open **How this works** once, then close.

---

## Walkthrough (actions unchanged — boundary callouts added)

Use current UI labels: **Play scenario**, **Refresh advice**, **Live incident
board**, **Decision history**, handoff cards, etc.

### Step 1 — Connection and agent modes

**Do**

1. Confirm connection shows **Connected** (SSE).
2. Show Luna / Terra / Sol badges (**AI on** vs **Demo pack**).
3. Optional: hover a **?** on a badge.

**Boundary callout**

> Transport layer is live. Agent layer is configured independently of the core —
> fixture or live OpenAI without rewriting the projector. Missing key falls back
> to demo pack; a zero-balance key still shows live and fails as a recorded model
> run, not a silent mode flip.

---

### Step 2 — Play scenario (story clock)

**Do**

1. Click **Play scenario**.
2. Watch the demo clock and facts arrive (incident, weather, roads, EMS, unit…).
3. Optional: **Show source** on a road or weather row.

**Boundary callout**

> Simulation SSE and the public API are the transport. The UI is only
> subscribing. Canonical events feed the **deterministic projector** — not the
> LLM — to build the board.

---

### Step 3 — COP / “what we know right now”

**Do**

On **Live incident board**, walk claim classes:

- Fact from the scenario  
- Suggested assessment  
- Suggestion for you  

**Boundary callout (repeat the money line)**

> This board is the COP. **Only the deterministic projector mutates it.** AI
> may assess; it does not write operational state. That mutability guarantee is
> the enterprise selling point.

---

### Step 4 — Refresh advice

**Do**

1. Click **Refresh advice**.
2. Walk access insight → later superseded / not current after road opens.
3. If live + funded key: note real model runs; if error: failure is audited, COP unchanged.

**Boundary callout**

> Agent layer only. Terra/Sol **propose**. Historical fixture cards are not live
> model output unless you just generated them. Supersession at a later revision
> is provenance working — advice at rev 7 remains queryable after rev 9.

---

### Step 5 — Handoffs and human judgment

**Do**

1. **Dispatch** note → Save (demo).  
2. **Maintenance / road** note → Save (demo).  
3. Show results: not carried out, not delivered.  
4. Optional: Agree / annotate on a selected target.

**Boundary callout**

> Human gate. Every action is an immutable audit with **executed: false**.
> Mosaic recorded intent; it did not contact a real department.
>
> **Maintenance handoff seam:** this control is where a **theoretical
> multi-domain Mosaic** (or external CMMS / cyber ticket / EOC channel) would
> plug in. Today we stop at recorded intent — delivery stays outside the agent
> and outside this demo.

If recurrence appears: **deterministic** pattern awareness, not LLM self-healing.

---

### Step 6 — Decision history

**Do**

Open **Decision history**: your notes, analysis runs, scenario steps.

**Boundary callout**

> Provenance as a service: who advised, on which inputs, at which board
> revision, what the human recorded. Framework value for the next team, the next
> incident, and the next **client** — not only this glass.

---

## Closing / end-of-video voiceover

Hold 1–2s on **Decision history** or **Connected**, then VO.

### Full close + CTA (preferred for video, ≈25–35s)

> Mosaic keeps AI on the right side of the line. Models propose. The
> deterministic projector owns the operating picture. Every suggestion and
> human decision is auditable — and nothing claims to have executed outside
> the system.
>
> This UI is one reference client. Same contracts for a cybersecurity ops
> floor, a government disaster EOC, or your own ledger — by plugging a
> different deterministic core and, at handoff seams like maintenance, a
> different downstream system. That’s the framework.
>
> The framework is live right now. Click the link in the project description,
> hit **Play scenario**, and drive the simulation yourself.

### Tighter close + CTA (≈15–20s)

> Models propose. The projector disposes. Provenance never lies.
>
> The framework is live right now. Click the link in the project description,
> hit **Play scenario**, and drive the simulation yourself.

### CTA only (if architecture pitch already ran)

> The framework is live right now. Click the link in the project description,
> hit **Play scenario**, and drive the simulation yourself.

**Delivery notes:** Match the UI label **Play scenario**. Optional half-second
hold after “live right now,” then the click instruction. Lower-third can show
**mosaic.nryn.dev** (or the project-description link).

---

## Safety lines (always true)

- Synthetic data only; no real PII or operational feeds in the repo.  
- No login; open public demo actor.  
- Models inform; projector alone mutates COP.  
- Handoffs and reviews: not carried out / not delivered in this demo.  
- API key is server-only (Secret Manager on Cloud Run; `.env` locally, never Git).  
- Not a shipped multi-domain product; architecture is built for pluggable cores.

---

## Persistence honesty (if asked)

| Environment | After restart |
|-------------|----------------|
| Local Compose volume | SQLite audits/model runs recovered |
| Cloud Run `/tmp` | Fixture reseeded; operator history lost |

Litestream / Cloud SQL = future durable path, not current live deploy.

---

## Live models (quick reference)

| Variable | Role |
|----------|------|
| `OPENAI_API_KEY` | Server-only secret |
| `MOSAIC_LUNA_PROVIDER` | `fixture` \| `live` |
| `MOSAIC_TERRA_PROVIDER` | `fixture` \| `live` |
| `MOSAIC_SOL_PROVIDER` | `fixture` \| `live` |

Compose defaults providers to **live** when the key is present.  
Cloud Run: live providers + Secret Manager key.

Budget is the key’s provider-side limit; the app adds no separate governor.

---

## UI map for the presenter

1. Top bar — How this works, AI key + connection pills, synthetic-data tag, incident id, clock  
2. Left rail — live status board (units / roads / resources / weather)  
3. Scenario bar — Play scenario / Start over / clock  
4. Live incident board — banner, Refresh advice, COP event log, advisories (+ past-advice toggle)  
5. Right rail — Show source (summary + raw record), handoffs (Dispatch + **maintenance seam**), decisions  
6. Decision history — paper trail  
7. Bottom drawer — developer health / usage estimate / API (optional; reinforces “API-first”)  

Hover **?** for operator tips; use **this script** for the framework pitch.
