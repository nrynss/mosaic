package domesticdisturbance

import (
	"context"
	"fmt"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"mosaic.local/mosaic/internal/contracts"
	"mosaic.local/mosaic/internal/pgstore"
	"mosaic.local/mosaic/internal/reference/domesticdisturbance/simulator"
)

type fixedActiveSession struct {
	id string
}

func (f fixedActiveSession) ActiveSessionID() (string, bool) {
	if f.id == "" {
		return "", false
	}
	return f.id, true
}

// Progressive ProcessBeat on Postgres is gated: without MOSAIC_TEST_PG_DSN the
// test skips so `go test ./...` stays green offline. When set, proves the
// composition-level progressive path (ingest+project+advisory continuum) reaches
// COP revision 9 on the durable pgstore backend.
func TestProgressiveProcessBeatReachesRev9OnPostgres(t *testing.T) {
	dsn := strings.TrimSpace(os.Getenv("MOSAIC_TEST_PG_DSN"))
	if dsn == "" {
		t.Skip("MOSAIC_TEST_PG_DSN not set; skipping PostgreSQL progressive smoke")
	}

	ctx := context.Background()
	pg := openIsolatedPGStore(t, ctx, dsn)

	root, err := simulator.RepositoryRoot(".")
	if err != nil {
		t.Fatalf("repository root: %v", err)
	}

	composeCtx := WithActiveSession(ctx, fixedActiveSession{id: "sim-pg-progressive-1"})

	runtime, err := New().Compose(composeCtx, pg, root)
	if err != nil {
		t.Fatalf("Compose: %v", err)
	}
	progressive, ok := runtime.(interface {
		ProcessBeat(context.Context, string) error
	})
	if !ok {
		t.Fatal("runtime does not support ProcessBeat")
	}
	schedule, ok := runtime.(contracts.SimulationSchedule)
	if !ok {
		t.Fatal("runtime does not expose SimulationSchedule")
	}

	for _, beat := range schedule.Beats() {
		if err := progressive.ProcessBeat(ctx, beat.BeatID); err != nil {
			t.Fatalf("ProcessBeat %s: %v", beat.BeatID, err)
		}
	}

	recovered, err := runtime.Recover(ctx)
	if err != nil {
		t.Fatalf("Recover: %v", err)
	}
	if recovered.StateRevision != 9 {
		t.Fatalf("state_revision = %d, want 9", recovered.StateRevision)
	}

	history, err := pg.ReadAdvisoryHistory(ctx)
	if err != nil {
		t.Fatalf("ReadAdvisoryHistory: %v", err)
	}
	if len(history.Insights) != 2 || len(history.Recommendations) != 1 {
		t.Fatalf("advisories insights=%d recs=%d, want 2/1", len(history.Insights), len(history.Recommendations))
	}
}

var progressivePGSchemaCounter atomic.Int64

func openIsolatedPGStore(t *testing.T, ctx context.Context, dsn string) *pgstore.Store {
	t.Helper()
	schema := fmt.Sprintf("mosaic_prog_%d_%d", time.Now().UnixNano(), progressivePGSchemaCounter.Add(1))

	bootstrap, err := pgx.Connect(ctx, dsn)
	if err != nil {
		t.Fatalf("connect bootstrap: %v", err)
	}
	if _, err := bootstrap.Exec(ctx, fmt.Sprintf("CREATE SCHEMA %s", pgIdent(schema))); err != nil {
		_ = bootstrap.Close(ctx)
		t.Fatalf("create test schema: %v", err)
	}
	_ = bootstrap.Close(ctx)

	cfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		t.Fatalf("parse DSN: %v", err)
	}
	cfg.ConnConfig.RuntimeParams["search_path"] = schema
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		t.Fatalf("open pool: %v", err)
	}

	store, err := pgstore.NewFromPool(ctx, pool)
	if err != nil {
		pool.Close()
		dropSchema(t, dsn, schema)
		t.Fatalf("apply migrations: %v", err)
	}
	t.Cleanup(func() {
		_ = store.Close()
		dropSchema(t, dsn, schema)
	})
	return store
}

func pgIdent(name string) string {
	return `"` + strings.ReplaceAll(name, `"`, `""`) + `"`
}

func dropSchema(t *testing.T, dsn, schema string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	conn, err := pgx.Connect(ctx, dsn)
	if err != nil {
		t.Logf("drop schema connect: %v", err)
		return
	}
	defer conn.Close(ctx)
	if _, err := conn.Exec(ctx, fmt.Sprintf("DROP SCHEMA IF EXISTS %s CASCADE", pgIdent(schema))); err != nil {
		t.Logf("drop schema %s: %v", schema, err)
	}
}
