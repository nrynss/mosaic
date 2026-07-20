// Package domesticdisturbance is Mosaic's synthetic reference domain.
package domesticdisturbance

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"

	"mosaic.local/mosaic/internal/api"
	"mosaic.local/mosaic/internal/contracts"
	"mosaic.local/mosaic/internal/profile"
	"mosaic.local/mosaic/internal/reference/domesticdisturbance/dataset"
	"mosaic.local/mosaic/internal/reference/domesticdisturbance/simulator"
	"mosaic.local/mosaic/internal/store"
)

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

func (domainProfile) Compose(ctx context.Context, database *store.Store, assetRoot string) (profile.Runtime, error) {
	if database == nil {
		return nil, errors.New("store is required")
	}
	scenario, err := simulator.New(simulator.Config{
		Store:      database,
		SchemaDir:  filepath.Join(assetRoot, "ontology"),
		FixtureDir: filepath.Join(assetRoot, "datasets", ID),
	})
	if err != nil {
		return nil, fmt.Errorf("compose frozen scenario: %w", err)
	}
	advisory, err := simulator.NewAdvisoryReplay(simulator.AdvisoryReplayConfig{
		Store:      database,
		SchemaDir:  filepath.Join(assetRoot, "ontology"),
		FixtureDir: filepath.Join(assetRoot, "datasets", ID),
	})
	if err != nil {
		return nil, fmt.Errorf("compose fixture advisory replay: %w", err)
	}
	resolver, err := api.NewSQLiteEvidenceResolver(database, StateFacts{})
	if err != nil {
		return nil, fmt.Errorf("compose governed evidence resolver: %w", err)
	}
	return &runtime{store: database, scenario: scenario, advisory: advisory, resolver: resolver, assetRoot: assetRoot}, nil
}

type runtime struct {
	store     *store.Store
	scenario  *simulator.Service
	advisory  *simulator.AdvisoryReplay
	resolver  api.EvidenceResolver
	assetRoot string
}

var _ profile.Runtime = (*runtime)(nil)

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
	return nil
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
