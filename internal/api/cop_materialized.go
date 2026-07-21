package api

import (
	"context"
	"fmt"

	"mosaic.local/mosaic/internal/contracts"
)

// COPMaterialization is the optional cheap read path for GET /cop. It is
// satisfied by contracts.COPReadModelRepository (e.g. pgstore.Store or
// SessionScopedCOP). Kept local to the API package so SQLite compositions need
// not depend on pgstore.
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
//
// Active (optional, C3): when composed, read ports scope to the active
// simulation session. No active session → empty board (no fallback to a
// global/default materialization). When a session is active, materialization
// is preferred; missing materialization returns empty rather than replaying
// the full unscoped log (session isolation). Fallback is used only when Active
// is nil (legacy compositions).
type PreferMaterializedRecovery struct {
	Materialized COPMaterialization
	Fallback     RecoveryReader
	// Active resolves the session epoch for isolation. Nil keeps pre-C3 behaviour.
	Active contracts.ActiveSessionSource
}

// Compile-time: PreferMaterializedRecovery is a drop-in RecoveryReader.
var _ RecoveryReader = PreferMaterializedRecovery{}

// Recover returns the materialized ProjectionResult when present; otherwise it
// delegates to Fallback (typically replay.Runner), unless Active session
// isolation is composed (see struct docs).
func (r PreferMaterializedRecovery) Recover(ctx context.Context) (contracts.ProjectionResult, error) {
	if r.Active != nil {
		if _, active := r.Active.ActiveSessionID(); !active {
			return emptyCOPResult(), nil
		}
		// Session mode: materialization only. Never fall back to unscoped recovery.
		if r.Materialized == nil {
			return emptyCOPResult(), nil
		}
		result, found, err := r.Materialized.LoadCOPReadModel(ctx)
		if err != nil {
			return contracts.ProjectionResult{}, fmt.Errorf("load materialized COP: %w", err)
		}
		if !found {
			return emptyCOPResult(), nil
		}
		return result, nil
	}

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
