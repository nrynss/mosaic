package api

import (
	"context"
	"fmt"

	"mosaic.local/mosaic/internal/contracts"
)

// COPMaterialization is the optional cheap read path for GET /cop. It is
// satisfied by contracts.COPReadModelRepository (e.g. pgstore.Store). Kept
// local to the API package so SQLite compositions need not depend on pgstore.
type COPMaterialization interface {
	LoadCOPReadModel(ctx context.Context) (contracts.ProjectionResult, bool, error)
}

// PreferMaterializedRecovery implements RecoveryReader by loading the
// materialized COP first and falling back to full deterministic recovery only
// when no materialization row exists. Load errors fail closed (no silent
// full-replay that would hide a broken read-model backend).
//
// When Materialized is nil, behaviour is identical to Fallback alone — the
// SQLite path keeps using replay.Runner with no materialization.
type PreferMaterializedRecovery struct {
	Materialized COPMaterialization
	Fallback     RecoveryReader
}

// Recover returns the materialized ProjectionResult when present; otherwise it
// delegates to Fallback (typically replay.Runner).
func (r PreferMaterializedRecovery) Recover(ctx context.Context) (contracts.ProjectionResult, error) {
	if r.Materialized != nil {
		result, found, err := r.Materialized.LoadCOPReadModel(ctx)
		if err != nil {
			return contracts.ProjectionResult{}, fmt.Errorf("load materialized COP: %w", err)
		}
		if found {
			return result, nil
		}
	}
	if r.Fallback == nil {
		return contracts.ProjectionResult{}, fmt.Errorf("recovery reader is required")
	}
	return r.Fallback.Recover(ctx)
}
