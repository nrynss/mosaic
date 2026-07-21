// Package domesticdisturbance is Mosaic's synthetic reference domain.
package domesticdisturbance

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"sync"

	"mosaic.local/mosaic/internal/api"
	"mosaic.local/mosaic/internal/contracts"
	"mosaic.local/mosaic/internal/pgstore"
	"mosaic.local/mosaic/internal/profile"
	"mosaic.local/mosaic/internal/reference/domesticdisturbance/dataset"
	"mosaic.local/mosaic/internal/reference/domesticdisturbance/simulator"
	"mosaic.local/mosaic/internal/store"
)

// activeSessionContextKey carries an optional ActiveSessionSource into Compose
// so session materialization can write session-scoped COP keys (C3/D1/D1h).
type activeSessionContextKey struct{}

// sessionAdvisoriesContextKey carries the optional in-memory session→advisory
// id index so progressive fixture stages can Record against the active epoch.
type sessionAdvisoriesContextKey struct{}

// copMaterializationContextKey carries an optional COPReadModelRepository used
// by the progressive SQLite path as a session board cache (D1h R2). Postgres
// uses its durable cop_read_model table instead and ignores this value.
type copMaterializationContextKey struct{}

// WithActiveSession attaches the active-session holder for Compose. Nil is a
// no-op. Composition creates the holder before Compose so MaterializingProjector
// and PreferMaterializedRecovery share the same epoch pointer.
func WithActiveSession(ctx context.Context, active contracts.ActiveSessionSource) context.Context {
	if active == nil {
		return ctx
	}
	return context.WithValue(ctx, activeSessionContextKey{}, active)
}

// WithSessionAdvisories attaches the session-scoped advisory id index for
// progressive Compose. Nil is a no-op. Pair with WithActiveSession so
// ProcessBeat can Record fixture advisory ids for GET /advisories filtering.
func WithSessionAdvisories(ctx context.Context, recorder simulator.SessionAdvisoryRecorder) context.Context {
	if recorder == nil {
		return ctx
	}
	return context.WithValue(ctx, sessionAdvisoriesContextKey{}, recorder)
}

// WithCOPMaterialization attaches an in-memory (or other) COP read-model
// backend for progressive SQLite session isolation. Pair with WithActiveSession
// so MaterializingProjector writes SessionCOPReadModelKey(sessionID). Nil is a
// no-op. PreferMaterializedRecovery in composition must share the same instance.
func WithCOPMaterialization(ctx context.Context, repo contracts.COPReadModelRepository) context.Context {
	if repo == nil {
		return ctx
	}
	return context.WithValue(ctx, copMaterializationContextKey{}, repo)
}

func activeSessionFromContext(ctx context.Context) contracts.ActiveSessionSource {
	if ctx == nil {
		return nil
	}
	active, _ := ctx.Value(activeSessionContextKey{}).(contracts.ActiveSessionSource)
	return active
}

func sessionAdvisoriesFromContext(ctx context.Context) simulator.SessionAdvisoryRecorder {
	if ctx == nil {
		return nil
	}
	recorder, _ := ctx.Value(sessionAdvisoriesContextKey{}).(simulator.SessionAdvisoryRecorder)
	return recorder
}

func copMaterializationFromContext(ctx context.Context) contracts.COPReadModelRepository {
	if ctx == nil {
		return nil
	}
	repo, _ := ctx.Value(copMaterializationContextKey{}).(contracts.COPReadModelRepository)
	return repo
}

const ID = dataset.DomesticDisturbance

// ViewerIdentity and SupervisorIdentity are this reference scenario's demo actor
// identities. They are the domain's data, not the reusable core's; the profile
// supplies them to the generic public actor resolver and Sol briefing guard.
const (
	ViewerIdentity     = "viewer-demo"
	SupervisorIdentity = "supervisor-demo"
)

type domainProfile struct{}

var _ profile.Profile = domainProfile{}

// New returns the sole synthetic reference profile registered by this demo.
func New() profile.Profile {
	return domainProfile{}
}

func (domainProfile) ID() string {
	return ID
}

func (domainProfile) Identities() profile.Identities {
	return profile.Identities{Viewer: ViewerIdentity, Supervisor: SupervisorIdentity}
}

func (domainProfile) Validate(assetRoot string) error {
	return dataset.Validate(assetRoot)
}

func (domainProfile) Compose(ctx context.Context, repository contracts.ImmutableRecordRepository, assetRoot string) (profile.Runtime, error) {
	active := activeSessionFromContext(ctx)
	sessionCOP := copMaterializationFromContext(ctx)
	domain, wrapProjector, resolver, err := bindDomainStore(repository, active, sessionCOP)
	if err != nil {
		return nil, err
	}
	scenario, err := simulator.New(simulator.Config{
		Store:         domain,
		SchemaDir:     filepath.Join(assetRoot, "ontology"),
		FixtureDir:    filepath.Join(assetRoot, "datasets", ID),
		WrapProjector: wrapProjector,
	})
	if err != nil {
		return nil, fmt.Errorf("compose frozen scenario: %w", err)
	}
	advisory, err := simulator.NewAdvisoryReplay(simulator.AdvisoryReplayConfig{
		Store:      domain,
		SchemaDir:  filepath.Join(assetRoot, "ontology"),
		FixtureDir: filepath.Join(assetRoot, "datasets", ID),
		// Progressive restart: rebuild current COP from durable store when the
		// in-memory timeline was lost mid-run.
		RecoverCOP: scenario.Recover,
	})
	if err != nil {
		return nil, fmt.Errorf("compose fixture advisory replay: %w", err)
	}
	return &runtime{
		store:             domain,
		scenario:          scenario,
		advisory:          advisory,
		resolver:          resolver,
		assetRoot:         assetRoot,
		active:            active,
		sessionAdvisories: sessionAdvisoriesFromContext(ctx),
	}, nil
}

// bindDomainStore accepts either durable backend and wires optional COP
// materialization + evidence resolution for that backend only. No dual-store
// path is offered: Compose fails closed when the repository is neither SQLite
// nor Postgres.
//
// When active is set:
//   - Postgres: durable cop_read_model rows are session-keyed.
//   - SQLite progressive: sessionCOP (typically store.MemoryCOP from composition)
//     is session-keyed so GET /cop is isolated per Play without a durable
//     materialization table (D1h R2).
func bindDomainStore(
	repository contracts.ImmutableRecordRepository,
	active contracts.ActiveSessionSource,
	sessionCOP contracts.COPReadModelRepository,
) (
	simulator.DomainStore,
	func(contracts.Projector) contracts.Projector,
	api.EvidenceResolver,
	error,
) {
	switch backend := repository.(type) {
	case *store.Store:
		if backend == nil {
			return nil, nil, nil, errors.New("store is required")
		}
		resolver, err := api.NewSQLiteEvidenceResolver(backend, StateFacts{})
		if err != nil {
			return nil, nil, nil, fmt.Errorf("compose governed evidence resolver: %w", err)
		}
		// Progressive SQLite: materialize into the shared in-memory session COP
		// cache when composition supplied one (same PreferMaterializedRecovery
		// backend used for GET /cop).
		if active != nil && sessionCOP != nil {
			copRepo := pgstore.NewSessionScopedCOP(sessionCOP, active)
			wrap := func(inner contracts.Projector) contracts.Projector {
				return pgstore.NewMaterializingProjector(inner, copRepo)
			}
			return backend, wrap, resolver, nil
		}
		return backend, nil, resolver, nil
	case *pgstore.Store:
		if backend == nil {
			return nil, nil, nil, errors.New("store is required")
		}
		resolver, err := api.NewPostgresEvidenceResolver(backend.Pool(), StateFacts{})
		if err != nil {
			return nil, nil, nil, fmt.Errorf("compose governed evidence resolver: %w", err)
		}
		var copRepo contracts.COPReadModelRepository = backend
		if active != nil {
			copRepo = pgstore.NewSessionScopedCOP(backend, active)
		}
		wrap := func(inner contracts.Projector) contracts.Projector {
			return pgstore.NewMaterializingProjector(inner, copRepo)
		}
		return backend, wrap, resolver, nil
	default:
		return nil, nil, nil, errors.New("domesticdisturbance requires *store.Store or *pgstore.Store")
	}
}

type runtime struct {
	store     simulator.DomainStore
	scenario  *simulator.Service
	advisory  *simulator.AdvisoryReplay
	resolver  api.EvidenceResolver
	assetRoot string

	// C3 progressive: ActiveSession + SessionAdvisoryView so fixture stages
	// Record against the live epoch for GET /advisories filtering.
	active            contracts.ActiveSessionSource
	sessionAdvisories simulator.SessionAdvisoryRecorder

	// Progressive path: accumulate timeline snapshots so advisory stages can
	// reuse historical rev-7/rev-9 COPs without re-running bulk seed.
	// Best-effort only — ContinueProgressive recovers from the durable store
	// when this slice is empty after a process restart.
	mu       sync.Mutex
	timeline []simulator.TimelineEntry
}

var _ profile.Runtime = (*runtime)(nil)
var _ contracts.SimulationSchedule = (*runtime)(nil)

func (r *runtime) Run(ctx context.Context) error {
	run, err := r.scenario.Run(ctx)
	if err != nil {
		return err
	}
	timeline, err := r.advisoryTimeline(ctx, run)
	if err != nil {
		return err
	}
	if _, err := r.advisory.Replay(ctx, timeline); err != nil {
		return err
	}
	// Keep progressive timeline in sync when bulk seed is used (optional path).
	r.mu.Lock()
	r.timeline = append([]simulator.TimelineEntry(nil), run.Timeline...)
	r.mu.Unlock()
	return nil
}

// ProcessBeat ingests one scheduled beat through the real P05 pipeline, recovers
// the COP, and runs progressive advisory stages when revisions 7 and/or 9 first
// become available. Idempotent via P05 source identity and durable advisory
// stage classification. This is the sync consumer for the interactive EventLog
// path (composition Appends first, then calls ProcessBeat).
func (r *runtime) ProcessBeat(ctx context.Context, beatID string) error {
	if r == nil || r.scenario == nil || r.advisory == nil {
		return errors.New("domain runtime is not configured")
	}
	entry, err := r.scenario.IngestBeat(ctx, beatID)
	if err != nil {
		return err
	}
	r.mu.Lock()
	r.timeline = append(r.timeline, entry)
	timeline := append([]simulator.TimelineEntry(nil), r.timeline...)
	r.mu.Unlock()
	result, err := r.advisory.ContinueProgressive(ctx, timeline)
	if err != nil {
		return err
	}
	r.recordProgressiveSessionAdvisories(result)
	return nil
}

// recordProgressiveSessionAdvisories indexes fixture artifacts written during
// this beat against the active simulation session (C3 session-scoped board).
func (r *runtime) recordProgressiveSessionAdvisories(result simulator.AdvisoryReplayResult) {
	if r == nil || r.advisory == nil || r.sessionAdvisories == nil || r.active == nil {
		return
	}
	sessionID, ok := r.active.ActiveSessionID()
	if !ok || sessionID == "" {
		return
	}
	r.advisory.RecordSessionStages(r.sessionAdvisories, sessionID, result.StagesRun)
}

// RawEventPayload returns fixture raw-event JSON for EventLog.Append.
func (r *runtime) RawEventPayload(rawEventID string) ([]byte, error) {
	if r == nil || r.scenario == nil {
		return nil, errors.New("domain runtime is not configured")
	}
	return r.scenario.RawEventPayload(rawEventID)
}

func (r *runtime) Recover(ctx context.Context) (contracts.ProjectionResult, error) {
	return r.scenario.Recover(ctx)
}

// advisoryTimeline preserves the frozen rev-7/rev-9 snapshots required by the
// fixture advisory replay when a retained database makes P05 deliveries
// idempotent and the current Run therefore returns no fresh timeline. The
// fallback uses a transient in-memory store and the same checked-in scenario;
// it never mutates the retained database or invokes a model/network client.
func (r *runtime) advisoryTimeline(ctx context.Context, run simulator.RunResult) ([]simulator.TimelineEntry, error) {
	if timelineHasRevision(run.Timeline, 7) && timelineHasRevision(run.Timeline, 9) {
		return run.Timeline, nil
	}
	temporary, err := store.Open(ctx, ":memory:")
	if err != nil {
		return nil, fmt.Errorf("open transient fixture timeline store: %w", err)
	}
	defer temporary.Close()
	shadow, err := simulator.New(simulator.Config{
		Store:      temporary,
		SchemaDir:  filepath.Join(r.assetRoot, "ontology"),
		FixtureDir: filepath.Join(r.assetRoot, "datasets", ID),
	})
	if err != nil {
		return nil, fmt.Errorf("compose transient fixture timeline: %w", err)
	}
	result, err := shadow.Run(ctx)
	if err != nil {
		return nil, fmt.Errorf("run transient fixture timeline: %w", err)
	}
	if !timelineHasRevision(result.Timeline, 7) || !timelineHasRevision(result.Timeline, 9) {
		return nil, errors.New("transient fixture timeline is missing required revisions")
	}
	return result.Timeline, nil
}

func timelineHasRevision(timeline []simulator.TimelineEntry, revision int64) bool {
	for _, entry := range timeline {
		if entry.StateRevision == revision {
			return true
		}
	}
	return false
}

func (r *runtime) Resolve(ctx context.Context, kind, id string, cop map[string]any) (api.Resolution, error) {
	return r.resolver.Resolve(ctx, kind, id, cop)
}

func (r *runtime) Beats() []contracts.ScheduledBeat {
	return r.scenario.Beats()
}
