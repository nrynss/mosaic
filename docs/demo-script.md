# Mosaic Hackathon Demo Script

## Positioning

Mosaic is an auditable event-to-state foundation for decision-support tools. The
domestic-disturbance call is a **synthetic reference implementation**, not a
police product claim.

Distinguish three layers for the audience:

| Layer | Status |
|-------|--------|
| **Runs now** | Synthetic ingestion, deterministic COP, simulation session, evidence resolution, fixture + opt-in live models, operator handoffs/audits (`executed: false`), in-app Help + tips, local Docker durable volume, Cloud Run ephemeral demo |
| **Interface complete** | Incident command workspace, Analyze, recipient handoff cards, provenance/actions tab, recurrence surface |
| **Future** | Durable Cloud Run (Litestream/Cloud SQL), external delivery connectors, multi-instance scaling |

**Live URL:** https://mosaic-demo-358513274447.us-central1.run.app  
**Local:** `docker compose up --build` → http://localhost:8080

Use the in-app **Help** panel for architecture details; this script is the
spoken walkthrough.

---

## Synthetic data: enough for the demo?

**Yes.** No extra dataset generation is required for the hackathon narrative.

Checked-in fixture: `datasets/domestic-disturbance/`

| Item | Detail |
|------|--------|
| Beats | 10 (scenario.json) |
| Story | 911 intake → welfare → weather → road closure → EMS → officer update → incomplete repair → quarantine → late EMS → road open |
| COP end state | State revision **9** with open Brook Lane after correction |
| Advisories | Access-constraint insight (superseded) + obsolete follow-up + recommendation (not current) |
| Integrity demos | Quarantined invalid input; late delivery; repaired-then-opened road |
| Audits | Briefing requested + supervisor ack samples |

That is enough to show: intake → constraint → judgment → correction →
provenance without inventing more events mid-demo.

---

## Spoken opening (30–45 seconds)

> Mosaic turns synthetic field events into a common operating picture you can
> audit. Only the world is simulated. You are the real operator. Models may
> advise; they never dispatch, and they never rewrite the projection.

Point at the header: **Synthetic demo · single instance · no external actions**.
Open **Help** briefly, then close it — show that guidance is built in.

---

## Step 1 — Connection and agent modes

1. Confirm the connection pill is **Live** (SSE).
2. Show Luna / Terra / Sol badges.
   - **live** = server has key + `MOSAIC_*_PROVIDER=live` (Compose and Cloud Run
     are configured this way when the key is present).
   - **fixture** = deterministic local path (missing key or explicit fixture).
3. Hover a **?** tip on an agent badge; mention zero-balance keys still show
   live and fail as recorded model runs — they do not silently flip to fixture.

---

## Step 2 — Start simulation (the story clock)

1. Click **Start Simulation**.
2. Elapsed timer runs; beats arrive on the session stream.
3. Narrate as facts appear:
   - domestic incident and unit assignment;
   - heavy rain;
   - road constraint (access risk);
   - EMS availability;
   - officer update;
   - correction path that reopens the road.

Optional: click **Resolve evidence** on a road or weather row — right rail shows
the bounded artifact (raw payload bytes withheld).

---

## Step 3 — COP and claim classes

On the **Incident Command Workspace**:

- **Reported fact** — source-projected state (incident, unit, road, weather).
- **Derived assessment** — Terra-class insight (historical fixture cards may be
  superseded after the road opens — that is intentional).
- **Human-review recommendation** — Sol-class guidance for the supervisor.

Say: *The COP is deterministic. Only the projector mutates it. AI assesses; it
does not write state.*

---

## Step 4 — Analyze and advisories

1. Click **Analyze Incident** to refresh advisory composition.
2. Walk the access insight: confidence, cited evidence, later **superseded** /
   **not current** after correction.
3. If live Terra/Sol and a funded key: operator analyze/brief API paths call
   OpenAI; results appear as new model runs. If the key is empty or API errors,
   explain the recorded failure without COP mutation.

Do **not** claim historical fixture cards are live model output.

---

## Step 5 — Handoffs and human judgment

Right rail **Operator Handoff Controls**:

1. **Dispatch handoff** — add an observation; Prepare.  
2. **Maintenance handoff** — road-condition note; Prepare.  
3. Show response fields: `executed: false`, `delivered: false`, status recorded.

Then **Operator Decisions**: approve or annotate a selected target. Every action
is an immutable audit with `executed: false`.

Line to use:

> Mosaic recorded a proposed handoff. It did not contact a real department.

If recurrence alert appears for a prior maintenance-style note in the same area,
frame it as **deterministic recurrence awareness**, not LLM self-healing.

---

## Step 6 — Provenance tab

Switch to **Provenance & Action Trail**:

- model runs (fixture seed vs any live runs this session);
- audit records;
- simulation beats / session identity.

This is the “why Mosaic as a framework” moment: evidence, timing, provenance,
durable review history (durable on local Docker volume; ephemeral on Cloud Run
`/tmp` after recycle).

---

## Closing (15 seconds)

> Mosaic turns an event into a traceable operating picture, lets people make and
> record judgment calls, and preserves the evidence for the next team, the next
> incident, and the next system instance — without pretending AI ran the
> operation.

---

## Safety lines (always true)

- Synthetic data only; no real PII or operational feeds in the repo.
- No login; open public demo actor.
- Models inform; projector alone mutates COP.
- Handoffs and reviews: `executed: false`; never external delivery in this demo.
- API key is server-only (Secret Manager on Cloud Run; `.env` locally, never Git).

---

## Persistence honesty

| Environment | After restart |
|-------------|----------------|
| Local Compose volume | SQLite audits/model runs recovered |
| Cloud Run `/tmp` | Fixture reseeded; operator history lost |

Litestream / Cloud SQL = future durable parcel, not current live deploy.

---

## Live models (quick reference)

| Variable | Role |
|----------|------|
| `OPENAI_API_KEY` | Server-only secret |
| `MOSAIC_LUNA_PROVIDER` | `fixture` \| `live` |
| `MOSAIC_TERRA_PROVIDER` | `fixture` \| `live` |
| `MOSAIC_SOL_PROVIDER` | `fixture` \| `live` |

Compose defaults providers to **live** and injects the key from `.env`.  
Cloud Run: live providers + Secret Manager key (already wired for the public demo).

Budget is the key’s provider-side limit; the app does not add a separate governor.

---

## UI map for the presenter

1. Masthead — Help, connection, scope line  
2. Simulation bar — Start / Reset / elapsed  
3. Workspace — incident banner, Analyze, COP, advisories, recurrence  
4. Right rail — evidence + handoffs + decisions  
5. Provenance tab — full trail  
6. Bottom drawer — developer health / version / operations (optional)  

Hover **?** for short tips without leaving the surface.
