package pgstore

import (
	"context"

	"mosaic.local/mosaic/internal/contracts"
)

// SessionScopedCOP decorates a COPReadModelRepository so default Load/Save use
// the active simulation session as the materialization key. Keyed methods pass
// through unchanged. Composition wires this between MaterializingProjector and
// Store so project+materialize writes the session epoch, not "default".
//
// When no session is active, Load reports (zero, false, nil) and Save is a
// no-op success — there is no durable default row to clobber, and API recovery
// already returns an empty board when Active reports inactive.
type SessionScopedCOP struct {
	Inner  contracts.COPReadModelRepository
	Active contracts.ActiveSessionSource
}

// Compile-time: SessionScopedCOP remains a COPReadModelRepository.
var _ contracts.COPReadModelRepository = (*SessionScopedCOP)(nil)

// NewSessionScopedCOP wraps inner with session-key indirection. Both arguments
// are required.
func NewSessionScopedCOP(inner contracts.COPReadModelRepository, active contracts.ActiveSessionSource) *SessionScopedCOP {
	if inner == nil {
		panic("pgstore: NewSessionScopedCOP requires a non-nil COPReadModelRepository")
	}
	if active == nil {
		panic("pgstore: NewSessionScopedCOP requires a non-nil ActiveSessionSource")
	}
	return &SessionScopedCOP{Inner: inner, Active: active}
}

// LoadCOPReadModel loads the materialization for the active session key.
func (s *SessionScopedCOP) LoadCOPReadModel(ctx context.Context) (contracts.ProjectionResult, bool, error) {
	key, ok := s.sessionKey()
	if !ok {
		return contracts.ProjectionResult{}, false, nil
	}
	return s.Inner.LoadCOPReadModelKey(ctx, key)
}

// SaveCOPReadModel UPSERTs under the active session key. No active session is
// a successful no-op so projectors do not fail when the board is empty.
func (s *SessionScopedCOP) SaveCOPReadModel(ctx context.Context, result contracts.ProjectionResult) error {
	key, ok := s.sessionKey()
	if !ok {
		return nil
	}
	return s.Inner.SaveCOPReadModelKey(ctx, key, result)
}

// LoadCOPReadModelKey delegates to Inner unchanged.
func (s *SessionScopedCOP) LoadCOPReadModelKey(ctx context.Context, key string) (contracts.ProjectionResult, bool, error) {
	return s.Inner.LoadCOPReadModelKey(ctx, key)
}

// SaveCOPReadModelKey delegates to Inner unchanged.
func (s *SessionScopedCOP) SaveCOPReadModelKey(ctx context.Context, key string, result contracts.ProjectionResult) error {
	return s.Inner.SaveCOPReadModelKey(ctx, key, result)
}

func (s *SessionScopedCOP) sessionKey() (string, bool) {
	if s == nil || s.Active == nil {
		return "", false
	}
	id, active := s.Active.ActiveSessionID()
	if !active {
		return "", false
	}
	return contracts.SessionCOPReadModelKey(id), true
}
