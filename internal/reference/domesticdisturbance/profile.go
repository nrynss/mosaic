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

	// Progressive path: accumulate session-local timeline snapshots so advisory
	// stages see progressive rev-7/rev-9 COPs. Cleared when Active session id
	// changes (new Start/Reset epoch) so a second Play does not reuse the prior
	// session's beat ladder or full-store Recover snapshots.
	mu                sync.Mutex
	timelineSessionID string
	sessionBeatIDs    []string
	timeline          []simulator.TimelineEntry
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

// ProcessBeat ingests one scheduled beat through the real P05 pipeline, materialises
// a session-progressive COP (not full-store Recover), and runs progressive advisory
// stages when session revisions 7 and/or 9 first become available. Idempotent via
// P05 source identity and durable advisory stage classification. This is the sync
// consumer for the interactive EventLog path (composition Appends first, then
// calls ProcessBeat).
//
// Progressive honesty on a second Play: raw events may be P05 duplicates and the
// durable log already holds all canonical events. Session board revision still
// advances beat-by-beat by replaying only the canonical events for beats
// processed in this session epoch.
func (r *runtime) ProcessBeat(ctx context.Context, beatID string) error {
	if r == nil || r.scenario == nil || r.advisory == nil {
		return errors.New("domain runtime is not configured")
	}
	r.beginSessionIfChanged()

	// P05 deliver only — do not full-Recover (that jumps session materialisation
	// to final rev when the durable store is already complete).
	entry, err := r.scenario.DeliverBeat(ctx, beatID)
	if err != nil {
		return err
	}

	r.mu.Lock()
	r.sessionBeatIDs = append(r.sessionBeatIDs, beatID)
	beatIDs := append([]string(nil), r.sessionBeatIDs...)
	r.mu.Unlock()

	projected, err := r.scenario.ProgressiveCOPFromBeatIDs(ctx, beatIDs)
	if err != nil {
		return fmt.Errorf("progressive COP after beat %q: %w", beatID, err)
	}
	entry.StateRevision = projected.StateRevision
	entry.COP = projected.COP

	r.mu.Lock()
	r.timeline = append(r.timeline, entry)
	timeline := append([]simulator.TimelineEntry(nil), r.timeline...)
	r.mu.Unlock()

	result, err := r.advisory.ContinueProgressive(ctx, timeline)
	if err != nil {
		return err
	}
	r.recordProgressiveSessionAdvisories(result, projected.StateRevision)
	return nil
}

// BeginSession clears the progressive session timeline. Composition may call
// this on Start; ProcessBeat also clears automatically when Active session id
// changes so a second Play cannot reuse the prior epoch's beat ladder.
func (r *runtime) BeginSession() {
	if r == nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.timeline = nil
	r.sessionBeatIDs = nil
	r.timelineSessionID = ""
	if r.active != nil {
		if sid, ok := r.active.ActiveSessionID(); ok {
			r.timelineSessionID = sid
		}
	}
}

// beginSessionIfChanged clears the progressive timeline when the active session
// epoch changes (Start/Reset set a new id). No-op when Active is unset.
func (r *runtime) beginSessionIfChanged() {
	if r == nil || r.active == nil {
		return
	}
	sid, ok := r.active.ActiveSessionID()
	if !ok || sid == "" {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.timelineSessionID == sid {
		return
	}
	r.timelineSessionID = sid
	r.timeline = nil
	r.sessionBeatIDs = nil
}

// recordProgressiveSessionAdvisories indexes fixture artifacts written during
// this beat against the active simulation session (C3 session-scoped board).
//
// StagesRun covers newly executed stages and is always indexed. StagesSkipped
// covers durable stages already intact (including IntactRestart on a second
// Play) and is gated by progressive session StateRevision so rev7 stages are
// not re-indexed until the progressive board reaches ≥7, and rev9 until ≥9.
func (r *runtime) recordProgressiveSessionAdvisories(result simulator.AdvisoryReplayResult, progressiveRev int64) {
	if r == nil || r.advisory == nil || r.sessionAdvisories == nil || r.active == nil {
		return
	}
	sessionID, ok := r.active.ActiveSessionID()
	if !ok || sessionID == "" {
		return
	}
	stages := make([]string, 0, len(result.StagesRun)+len(result.StagesSkipped))
	stages = append(stages, result.StagesRun...)
	for _, stage := range result.StagesSkipped {
		if progressiveStageReached(stage, progressiveRev) {
			stages = append(stages, stage)
		}
	}
	r.advisory.RecordSessionStages(r.sessionAdvisories, sessionID, stages)
}

// progressiveStageReached reports whether a fixture advisory stage should be
// visible on the session board given the progressive COP revision.
func progressiveStageReached(stage string, progressiveRev int64) bool {
	switch stage {
	case "terra_active_rev7", "briefing_requested", "sol_recommendation_rev7":
		return progressiveRev >= 7
	case "terra_obsolete_rev9", "recommendation_acknowledged":
		return progressiveRev >= 9
	default:
		return false
	}
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
