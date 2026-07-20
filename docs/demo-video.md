# Mosaic Demo Video

Production plan for the **public YouTube submission** (under 3 minutes).

This document is the source of truth for **what we show**, **what we say**, and
**how the cut is structured**. It is not a fal prompt pack. Visual language is
derived from the checked-in synthetic scenario
(`datasets/domestic-disturbance-v4` / same story as the live demo pack).

Related: presenter walkthrough detail lives in [`demo-script.md`](demo-script.md).
This file owns the **edited video**, timing budget, and required VO about tooling.

---

## Submission requirements (must satisfy)

| Constraint | How we meet it |
|------------|----------------|
| **Public YouTube** | One unlisted-or-public upload; link in project description |
| **&lt; 3 minutes** | Hard cap **2:50** edit (leave headroom for platform/encoding) |
| **Project working** | Live product on screen: Play scenario → COP updates → advice → handoffs → Decision history |
| **Audio covers Codex** | Explicit VO (and optional lower-third) on how **Codex** was used to build Mosaic |
| **Audio covers GPT-5.6** | Explicit VO on how **GPT-5.6** powers / was used with Luna, Terra, Sol (and any design reasoning) |

If the cut exceeds 3:00, cut B-roll and architecture motion first — **never** cut
the working product or the Codex / GPT-5.6 lines.

---

## One-sentence film thesis

> Mosaic keeps generative AI on the safe side of operational truth — and we
> built it with Codex while running GPT-5.6 as specialized agents that advise
> but never own the board.

---

## What the audience should remember

1. **Models propose; the projector disposes** — COP mutability is deterministic.  
2. **You see a real system** — SSE, state revisions, handoffs with `executed: false`.  
3. **Codex built the stack; GPT-5.6 runs in the agent layer** — not a black-box chat glued to a UI.  
4. **Framework, not a 911 product** — same pattern for cyber, government, industrial, finance.  
5. **Try it** — mosaic.nryn.dev → **Play scenario**.

---

## Visual register (tone)

| Lean into | Avoid |
|-----------|--------|
| Weather, flooded bridge, debris, road reopen | Police-light / bodycam / cop-show cold open |
| **Ambulance / EMS-4** as the mobile stakes character | Cruiser hero shots, badge close-ups |
| Ops glass / multi-monitor COP | Real PII, real agency logos, real 911 audio |
| House at **14 Cedar Lane** as place anchor | Domestic-violence dramatization or faces in distress |
| Event flood + “which ones are true?” | Claiming AI ran the operation |

The reference scenario is a synthetic domestic-disturbance **domain profile**.
The video sells **access constraints under weather + contested EMS + provenance**,
not a police procedural.

---

## Runtime budget (target ≈ 2:40)

| Segment | Time | On screen | Audio |
|---------|------|-----------|--------|
| **A. Cold open** | 0:00–0:18 | Cinematic / cards (optional AI B-roll) | Stakes + thesis line |
| **B. Product intro + tooling** | 0:18–0:45 | Live UI (Connected, agent badges) | What Mosaic is + **Codex + GPT-5.6** |
| **C. Working walkthrough** | 0:45–2:10 | Play scenario → board → advice → handoffs → history | Boundary callouts; beat-linked B-roll |
| **D. Architecture close + CTA** | 2:10–2:40 | Multi-domain end card + live URL | Framework pitch + try it yourself |

Hold total under **2:50**. Prefer short holds over rushed VO.

---

## Segment A — Cold open (≤18s)

### What we show

Black / storm texture over a quiet suburban street (Cedar-like).  
Event cards land one by one (labels aligned to the synthetic pipeline, not a
cop trailer):

```
Incident report
Historical note
Weather alert
Bridge flood
EMS available
Field unit update
Incomplete road data
Invalid input
Late EMS update
Road reopened
```

Freeze. On-screen text:

```
Which ones are true?
```

Beat. Then:

```
MOSAIC
AI should advise. Not decide.
```

### What we do **not** show

- 911 call-center wall  
- Police light-bar hero shot  
- Real faces, real audio from emergency services  

### Purpose

Hook: **event flood + contested truth**, then cut into a working system.

---

## Segment B — Product + how we used Codex and GPT-5.6 (≈25–30s)

### What we show

Live Mosaic (https://mosaic.nryn.dev or local Docker):

1. Browser on the reference CAD-style UI.  
2. **Connected** pill (SSE).  
3. Luna / Terra / Sol mode badges (**AI on** if live key; else honest demo pack).  
4. Synthetic-data scope line visible.  
5. Optional half-second on **How this works**, then close.

### Required voiceover (tooling — do not skip)

> We built Mosaic with **Codex** — agentic coding across the Go spine, schemas,
> projector, APIs, and this Svelte reference client — so the architecture stayed
> contracts-first instead of a one-off demo script.
>
> In the product, **GPT-5.6** sits in the **agent layer**: **Luna** normalizes
> events, **Terra** reasons over the common operating picture, **Sol** drafts
> briefings when a human asks. They propose. They do not write operational state.

Optional lower-thirds (short):

| Time | Lower-third |
|------|-------------|
| On “Codex” | `Built with Codex` |
| On “GPT-5.6” | `GPT-5.6 · Luna / Terra / Sol` |

### Boundary line (one sentence)

> What you’re looking at is a **reference UI** over a synthetic high-stakes
> flow. Judge the **API contract**, not the design agency.

---

## Segment C — Project working (core of the submission)

Drive the **same actions** as [`demo-script.md`](demo-script.md). Record at
1920×1080. Hold long enough for VO; do not rely on fixture `delay_ms` (~100ms)
for pacing.

### C1 — Play scenario

**Do:** Click **Play scenario**. Watch the demo clock and left-rail / board facts
update without full page reloads.

**Say:**

> Simulation and public SSE streams push state. The UI only subscribes. Canonical
> events feed a **deterministic projector** — not the LLM — to build the board.

### C2 — Beat-linked story (what appears vs what we cut to)

Interleave short world cuts (1.5–2.5s) with the live board so judges see *why*
rows change. Prefer **ambulance, weather, roads, house** over police imagery.

| Order | Scenario beat | In the product (truth) | World cut (if used) |
|------:|---------------|------------------------|---------------------|
| 1 | 911 / incident reported | Incident at **14 Cedar Lane** appears | House exterior in rain — place only |
| 2 | Historical welfare | Location history / prior note | Same house, calmer “earlier” feel (optional) |
| 3 | Weather alert | Heavy rain · **Cedar district** | Storm over suburb / rain |
| 4 | Road closure | **Main Street bridge** blocked (flooding) | Flooded bridge approach |
| 5 | EMS available | **EMS-4** available (Central Station) | **Ambulance staged / at station** |
| 6 | Unit update | **Unit 17** assigned / near address | Wet residential approach — **no light-bar hero** |
| 7 | Incomplete road repaired | **Brook Lane** blocked; Luna **repaired** missing id | Debris blocking residential side street |
| 8 | Quarantined input | Malformed payload **quarantined** — COP not mutated | **UI only** (quarantine / reject badge) |
| 9 | Late EMS delivery | EMS-4 **unavailable** (occurred earlier, received late) | **Ambulance unavailable / pulled away** |
| 10 | Road correction | Brook Lane **open** (debris removed); prior advice may obsolete | Same street cleared / reopened |

**VO while EMS flips (beats 5 → 9):**

> Resource state is first-class. EMS-4 is available — then a late update marks it
> unavailable. History keeps both; nothing is silently rewritten.

**VO on quarantine / repair (beats 7–8):**

> Bad or incomplete inputs don’t get to invent truth. Luna repairs when the
> fixture allows, or quarantines — and the projector never accepts garbage into
> the operating picture as if it were fact.

### C3 — COP callout (money line)

**Do:** Hold **Live incident board**. Point at claim classes if visible
(fact / suggested assessment / suggestion for you).

**Say:**

> This board is the common operating picture. **Only the deterministic projector
> mutates it.** GPT-5.6 may assess; it does not dispose operational state.

Optional on-screen motion (3–5s, not full B-roll):

```
Incoming events  ═══►  PROJECTOR  ═══►  COP

LLM advice  ─►  Projector  ✗ rejected as state write
                ↘ stored as advice / audit
```

### C4 — Refresh advice

**Do:** **Refresh advice**. Walk access-constraint insight while Brook Lane is
constrained; note supersession after reopen if timing allows.

**Say:**

> Terra and Sol propose insights and recommendations against a **state revision**.
> When the road reopens, old advice can become obsolete — and remains searchable.
> That’s provenance, not chat amnesia.

### C5 — Human gate / handoffs

**Do:** Save a **Dispatch** note and a **Maintenance / road** note (demo).

**Show:** Not carried out / not delivered · `executed: false`.

**Say:**

> Humans record intent. Mosaic does not silently call a real desk. Delivery stays
> outside the agent — the framework’s job is immutable intent and provenance.

### C6 — Decision history

**Do:** Open **Decision history** — scenario steps, model runs, operator notes.

**Say:**

> Decision history is the audit spine — who advised, on which inputs, at which
> revision, what the human recorded. Like version control for operational
> decisions.

---

## Segment D — Close + CTA (≈25–30s)

### What we show

End card (motion or static), not a long UI pan:

```
Cybersecurity
Government
Emergency Response
Industrial Control
Financial Operations

        ↓
One framework.
Different deterministic cores.

        MOSAIC
        mosaic.nryn.dev

        Narayan SS
```

Name placement: final end card (and optional YouTube title/description credit).
Do **not** open the video with a long personal bio — product first, credit last.

### Voiceover (preferred close)

> Mosaic keeps AI on the right side of the line. Models propose. The projector
> owns the operating picture. Codex helped us ship that boundary in code;
> GPT-5.6 runs where generation belongs — advice, not silent authority.
>
> Same contracts for a cyber ops floor, a disaster EOC, or your own ledger — by
> plugging a different deterministic core.
>
> The framework is live. Open the link, hit **Play scenario**, and drive it
> yourself.

Shorter alternate if over time:

> Models propose. The projector disposes. Built with Codex; agents on GPT-5.6.
> Live now — **Play scenario** at mosaic.nryn.dev.

---

## Full VO spine (read-through draft ≈ 2:30–2:45)

Use as a continuous script; trim on the edit bay if a line overruns a hold.

**[Cold open — sparse VO or SFX only until title]**

> Every second, thousands of events arrive. Which ones are true?

**[Cut to live UI]**

> Mosaic is a developer framework for safe AI next to systems that must stay
> deterministic. We built it with **Codex** — contracts, projector, APIs, and
> this reference client — and we run **GPT-5.6** as specialized agents: Luna,
> Terra, and Sol. They advise. They never own the board.

**[Play scenario + intercuts]**

> Watch the synthetic scenario stream in. Weather, a flooded bridge, EMS-4
> available, then contested. Incomplete data gets repaired or quarantined. A late
> EMS update still lands in order. Brook Lane closes, then reopens after debris
> is cleared. Only the projector writes that truth.

**[Advice + handoffs + history]**

> When we refresh advice, GPT-5.6 reasons over the picture and cites evidence —
> without mutating state. Operators record handoffs that are not executed and not
> delivered in this demo. Decision history keeps every step auditable.

**[End card]**

> One framework. Different deterministic cores. Try it live — Play scenario at
> mosaic.nryn.dev.

---

## Codex vs GPT-5.6 (what “audio covering” means)

Judges must **hear** both names. Be specific enough to be credible; do not invent
features.

| Tool | Role in this project | What we claim on video |
|------|----------------------|-------------------------|
| **Codex** | Agentic implementation: Go services, ontology/schemas, projector, store, HTTP/SSE, tests, Svelte UI, Docker/Cloud Run wiring | “Built with Codex” across the stack; contracts-first development |
| **GPT-5.6** | Live agent reasoning path (Luna / Terra / Sol via OpenAI transport when providers are live) | “GPT-5.6 in the agent layer”; propose-only; failures audited; COP unchanged |

Honest scope if live key is unavailable during recording:

- Still name GPT-5.6 as the **designed live agent model class**.  
- Show **Demo pack / fixture** badges if that is what the screen shows.  
- Do **not** claim a live model run if the UI shows fixture mode.

Preferred recording: **AI on** with a funded server-side key so **Refresh advice**
shows real model runs in Decision history.

---

## What must appear on camera (checklist)

- [ ] Live URL or clear “Mosaic” product chrome  
- [ ] **Connected** / streaming state  
- [ ] **Play scenario** clicked; board updates  
- [ ] At least one of: weather, road, EMS row changing  
- [ ] **Ambulance** story present (EMS-4 available and/or unavailable) — UI and/or B-roll  
- [ ] **Refresh advice** or visible insight/recommendation  
- [ ] Human handoff with not-executed / not-delivered  
- [ ] **Decision history** (or equivalent audit trail)  
- [ ] Spoken **Codex**  
- [ ] Spoken **GPT-5.6**  
- [ ] CTA: **Play scenario** + mosaic.nryn.dev  
- [ ] Total runtime **&lt; 3:00**

---

## What we never claim on the video

- Real dispatch, real PII, or real external delivery  
- That AI executed operational actions  
- Multi-tenant production platform  
- Durable Cloud Run history (ephemeral `/tmp` unless you show local Docker persistence)  
- That multi-domain cores already ship — only that the architecture is pluggable  

Safety line if needed:

> Synthetic data only. Nothing is sent to a real department.

---

## Production notes

| Item | Guidance |
|------|----------|
| **Capture** | 1920×1080; browser zoom readable; hide personal bookmarks/OS noise |
| **Environment** | Prefer local Docker for retries; one clean take against https://mosaic.nryn.dev optional |
| **Pacing** | Pause simulation or use natural holds after major board updates for VO |
| **B-roll** | Optional; spend time on first ~18s if used; product proof is non-negotiable |
| **Audio** | Single clear VO track; light bed OK if speech stays intelligible |
| **Captions** | Burn-in or YouTube captions for Codex / GPT-5.6 / projector lines |
| **Export** | H.264 MP4; upload public (or unlisted if rules allow and link is shareable) |
| **Title idea** | `Mosaic — Safe AI next to deterministic systems (DevWeek)` |
| **Description** | One-line pitch + live URL + “Built with Codex; agents on GPT-5.6” + credit **Narayan SS** |
| **On-screen credit** | End card: **Narayan SS** under URL (see Segment D) |

---

## Edit order (practical)

1. Record product walkthrough with VO gaps (or record VO after).  
2. Lay cold open (even if temporary: cards on black).  
3. Drop Segment B tooling lines so Codex / GPT-5.6 land early.  
4. Trim walkthrough to essential board motion; intercut EMS / road / weather only where it clarifies.  
5. Add projector reject graphic if time allows.  
6. End card + CTA.  
7. Watch full cut once for the checklist; cut to **≤2:50**.

---

## Relationship to other docs

| Doc | Role |
|-----|------|
| [`demo-script.md`](demo-script.md) | Live presenter / long-form pitch and step-by-step UI actions |
| **This file** | YouTube cut structure, &lt;3 min budget, tooling VO, beat→visual map |
| [`HANDOFF.md`](../HANDOFF.md) | Planned Playwright capture is optional later; this video can be manual |

When the final URL is live, add it to the project description and optionally to
`README.md` / `HANDOFF.md` under demo preparation.
