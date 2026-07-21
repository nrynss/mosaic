# Archived handoffs

Historical coordinator boards and increment plans. These are **frozen
records** — not the live task surface.

**Live board:** [`HANDOFF.md`](../../../HANDOFF.md) at the repository root.

## Layout

```text
docs/archive/handoffs/
  README.md                          ← this index
  increments/
    v0.1-foundation/HANDOFF.md
    v0.2-fixture-advisory/HANDOFF.md
    v0.3-interactive-operator-demo/HANDOFF.md
    v0.4-pluggable-event-spine/HANDOFF.md
    coordinator-board/
      HANDOFF-2026-07-pre-closeout.md  ← root board snapshot before v0.4 closeout
```

## Increments

| Increment | Path | Summary |
|-----------|------|---------|
| **v0.1** Foundation | [increments/v0.1-foundation/HANDOFF.md](increments/v0.1-foundation/HANDOFF.md) | Ontology, SQLite store, ingestion, projector, demo composition |
| **v0.2** Fixture advisory | [increments/v0.2-fixture-advisory/HANDOFF.md](increments/v0.2-fixture-advisory/HANDOFF.md) | Fixture Terra/Sol composition, public advisory API |
| **v0.3** Interactive operator | [increments/v0.3-interactive-operator-demo/HANDOFF.md](increments/v0.3-interactive-operator-demo/HANDOFF.md) | Simulation control, operator API, live OpenAI opt-in |
| **v0.4** Pluggable event spine | [increments/v0.4-pluggable-event-spine/HANDOFF.md](increments/v0.4-pluggable-event-spine/HANDOFF.md) | Postgres spine, progressive Play, cassettes, durable Supabase deploy |

Coordinator board snapshots (not increment plans):

| Snapshot | Path |
|----------|------|
| Pre-closeout board (2026-07) | [increments/coordinator-board/HANDOFF-2026-07-pre-closeout.md](increments/coordinator-board/HANDOFF-2026-07-pre-closeout.md) |

## Note on links inside archived files

Internal paths in archived handoffs may still point at old locations
(`docs/archive/HANDOFF-v0.x…`, root `HANDOFF.md` as “live board”). Treat those
as historical. Prefer this index and the current root [`HANDOFF.md`](../../../HANDOFF.md).
