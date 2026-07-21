package pgstore

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"testing"
	"time"

	"mosaic.local/mosaic/internal/contracts"
	"mosaic.local/mosaic/internal/ontology/gen"
)

func TestCOPReadModelMigrationCreatesTable(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)

	var regclass string
	err := s.Pool().QueryRow(ctx, "SELECT to_regclass($1)::text", COPReadModelTable).Scan(&regclass)
	if err != nil {
		t.Fatalf("look up %s: %v", COPReadModelTable, err)
	}
	if regclass == "" {
		t.Fatalf("migration 0003 did not create %s", COPReadModelTable)
	}

	// Materialization is mutable: UPDATE must succeed (unlike append-only provenance).
	if err := s.SaveCOPReadModel(ctx, sampleProjection(1, "first")); err != nil {
		t.Fatalf("seed materialization: %v", err)
	}
	if _, err := s.Pool().Exec(ctx,
		`UPDATE cop_read_model SET state_revision = 2 WHERE read_model_key = $1`,
		contracts.DefaultCOPReadModelKey,
	); err != nil {
		t.Fatalf("mutable UPSERT path must allow UPDATE: %v", err)
	}
}

func TestCOPReadModelSaveLoadRoundTrip(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)

	_, found, err := s.LoadCOPReadModel(ctx)
	if err != nil {
		t.Fatalf("load empty: %v", err)
	}
	if found {
		t.Fatal("expected no materialization before first save")
	}

	want := sampleProjection(3, "round-trip")
	want.ProjectedAt = time.Date(2026, 3, 15, 12, 0, 0, 0, time.UTC)
	want.Checkpoint = gen.Checkpoint{
		CheckpointID:        "chk-3",
		SchemaVersion:       "1.0.0",
		StateRevision:       3,
		ThroughCanonicalSeq: 3,
		COP:                 mustJSON(map[string]any{"label": "round-trip", "state_revision": float64(3)}),
		CreatedAt:           "2026-03-15T12:00:00Z",
	}

	if err := s.SaveCOPReadModel(ctx, want); err != nil {
		t.Fatalf("save: %v", err)
	}

	got, found, err := s.LoadCOPReadModel(ctx)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if !found {
		t.Fatal("expected materialization after save")
	}
	if got.StateRevision != want.StateRevision {
		t.Fatalf("state_revision = %d, want %d", got.StateRevision, want.StateRevision)
	}
	if !got.ProjectedAt.UTC().Equal(want.ProjectedAt.UTC()) {
		t.Fatalf("projected_at = %v, want %v", got.ProjectedAt, want.ProjectedAt)
	}
	if got.COP["label"] != "round-trip" {
		t.Fatalf("cop label = %v, want round-trip", got.COP["label"])
	}
	if got.Checkpoint.CheckpointID != "chk-3" {
		t.Fatalf("checkpoint_id = %q, want chk-3", got.Checkpoint.CheckpointID)
	}
	if got.Checkpoint.ThroughCanonicalSeq != 3 {
		t.Fatalf("through_canonical_seq = %d, want 3", got.Checkpoint.ThroughCanonicalSeq)
	}
}

func TestCOPReadModelUpsertAdvancesRevision(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)

	if err := s.SaveCOPReadModel(ctx, sampleProjection(1, "v1")); err != nil {
		t.Fatalf("save v1: %v", err)
	}
	if err := s.SaveCOPReadModel(ctx, sampleProjection(5, "v5")); err != nil {
		t.Fatalf("save v5: %v", err)
	}

	got, found, err := s.LoadCOPReadModel(ctx)
	if err != nil || !found {
		t.Fatalf("load after upsert: found=%v err=%v", found, err)
	}
	if got.StateRevision != 5 {
		t.Fatalf("state_revision = %d, want 5", got.StateRevision)
	}
	if got.COP["label"] != "v5" {
		t.Fatalf("cop label = %v, want v5", got.COP["label"])
	}

	// Only one row for the default key.
	var count int
	if err := s.Pool().QueryRow(ctx,
		`SELECT COUNT(*) FROM cop_read_model WHERE read_model_key = $1`,
		contracts.DefaultCOPReadModelKey,
	).Scan(&count); err != nil {
		t.Fatalf("count rows: %v", err)
	}
	if count != 1 {
		t.Fatalf("row count = %d, want 1", count)
	}
}

func TestCOPReadModelSaveJoinsAmbientTransaction(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)

	err := s.WithinTransaction(ctx, func(txCtx context.Context) error {
		if err := s.SaveCOPReadModel(txCtx, sampleProjection(2, "tx")); err != nil {
			return err
		}
		// Visible inside the same transaction.
		got, found, err := s.LoadCOPReadModel(txCtx)
		if err != nil {
			return err
		}
		if !found || got.StateRevision != 2 {
			return fmt.Errorf("inside tx: found=%v revision=%d", found, got.StateRevision)
		}
		return errors.New("force rollback")
	})
	if err == nil {
		t.Fatal("expected transaction error")
	}

	_, found, err := s.LoadCOPReadModel(ctx)
	if err != nil {
		t.Fatalf("load after rollback: %v", err)
	}
	if found {
		t.Fatal("materialization should roll back with ambient transaction")
	}
}

func TestMaterializingProjectorSavesOnApplyAndReplay(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)

	inner := &stubProjector{
		apply: sampleProjection(4, "applied"),
	}
	mp := NewMaterializingProjector(inner, s)

	got, err := mp.ApplyCanonicalEvent(ctx, gen.CanonicalEvent{CanonicalEventID: "ev-1"})
	if err != nil {
		t.Fatalf("apply: %v", err)
	}
	if got.StateRevision != 4 {
		t.Fatalf("apply result revision = %d, want 4", got.StateRevision)
	}

	loaded, found, err := s.LoadCOPReadModel(ctx)
	if err != nil || !found {
		t.Fatalf("load after apply: found=%v err=%v", found, err)
	}
	if loaded.StateRevision != 4 || loaded.COP["label"] != "applied" {
		t.Fatalf("materialized after apply = %#v", loaded)
	}

	inner.replay = sampleProjection(9, "replayed")
	got, err = mp.Replay(ctx, gen.Checkpoint{}, nil)
	if err != nil {
		t.Fatalf("replay: %v", err)
	}
	if got.StateRevision != 9 {
		t.Fatalf("replay result revision = %d, want 9", got.StateRevision)
	}
	loaded, found, err = s.LoadCOPReadModel(ctx)
	if err != nil || !found {
		t.Fatalf("load after replay: found=%v err=%v", found, err)
	}
	if loaded.StateRevision != 9 || loaded.COP["label"] != "replayed" {
		t.Fatalf("materialized after replay = %#v", loaded)
	}
}

func TestMaterializingProjectorSkipsEmptyReplay(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)

	mp := NewMaterializingProjector(&stubProjector{
		replay: contracts.ProjectionResult{COP: map[string]any{}},
	}, s)
	if _, err := mp.Replay(ctx, gen.Checkpoint{}, nil); err != nil {
		t.Fatalf("empty replay: %v", err)
	}
	_, found, err := s.LoadCOPReadModel(ctx)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if found {
		t.Fatal("empty replay must not create a materialization row")
	}
}

func TestMaterializingProjectorPropagatesInnerError(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)
	mp := NewMaterializingProjector(&stubProjector{
		applyErr: context.DeadlineExceeded,
	}, s)
	if _, err := mp.ApplyCanonicalEvent(ctx, gen.CanonicalEvent{CanonicalEventID: "x"}); err == nil {
		t.Fatal("expected inner error")
	}
	_, found, _ := s.LoadCOPReadModel(ctx)
	if found {
		t.Fatal("failed apply must not materialize")
	}
}

type stubProjector struct {
	apply     contracts.ProjectionResult
	applyErr  error
	replay    contracts.ProjectionResult
	replayErr error
}

func (s *stubProjector) ApplyCanonicalEvent(context.Context, gen.CanonicalEvent) (contracts.ProjectionResult, error) {
	return s.apply, s.applyErr
}

func (s *stubProjector) Replay(context.Context, gen.Checkpoint, []gen.CanonicalEvent) (contracts.ProjectionResult, error) {
	return s.replay, s.replayErr
}

func sampleProjection(revision int64, label string) contracts.ProjectionResult {
	return contracts.ProjectionResult{
		StateRevision: revision,
		ProjectedAt:   time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC),
		COP: map[string]any{
			"label":          label,
			"state_revision": float64(revision),
		},
		Checkpoint: gen.Checkpoint{
			CheckpointID:        "chk-" + label,
			StateRevision:       revision,
			ThroughCanonicalSeq: revision,
		},
	}
}

func mustJSON(v any) json.RawMessage {
	b, err := json.Marshal(v)
	if err != nil {
		panic(err)
	}
	return b
}
