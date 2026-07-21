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
// # Inactive session behaviour (intentional silent no-op)
//
// When ActiveSessionSource reports no active session:
//
//   - LoadCOPReadModel returns (zero, false, nil) — same as a cold/missing key.
//   - SaveCOPReadModel returns nil without writing — intentional silent success.
//
// This is NOT an error path. Demos and recovery treat "no active session" as an
// empty board; projectors must not fail or invent a DefaultCOPReadModelKey row
// when the pointer is inactive. Callers that need a hard failure when nothing
// is active should check ActiveSessionSource themselves before Save. Changing
// Save to return an error would break progressive demos and existing tests
// that project while the session pointer is cleared between epochs.
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

// SaveCOPReadModel UPSERTs under the active session key.
//
// SILENT NO-OP: when no session is active this returns nil without calling
// Inner. See type docs — do not "fix" by returning an error here; demos
// and empty-board recovery depend on success-with-no-write semantics.
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
