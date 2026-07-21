package store

import (
	"context"
	"strings"
	"sync"

	"mosaic.local/mosaic/internal/contracts"
)

// MemoryCOP is an in-process COPReadModelRepository keyed by read-model key.
// Progressive SQLite demos use it as a session-scoped board cache (D1h R2):
// MaterializingProjector writes under SessionCOPReadModelKey(sessionID) via
// SessionScopedCOP; PreferMaterializedRecovery loads that key for GET /cop.
// The durable append-only store still retains history; this cache only scopes
// the operator board view so a new session / Reset does not flash prior Play COP.
type MemoryCOP struct {
	mu    sync.RWMutex
	byKey map[string]contracts.ProjectionResult
}

// Compile-time: MemoryCOP is a COPReadModelRepository.
var _ contracts.COPReadModelRepository = (*MemoryCOP)(nil)

// NewMemoryCOP returns an empty in-memory materialization store.
func NewMemoryCOP() *MemoryCOP {
	return &MemoryCOP{byKey: make(map[string]contracts.ProjectionResult)}
}

// LoadCOPReadModel loads the default materialization key.
func (m *MemoryCOP) LoadCOPReadModel(ctx context.Context) (contracts.ProjectionResult, bool, error) {
	return m.LoadCOPReadModelKey(ctx, contracts.DefaultCOPReadModelKey)
}

// SaveCOPReadModel UPSERTs under the default materialization key.
func (m *MemoryCOP) SaveCOPReadModel(ctx context.Context, result contracts.ProjectionResult) error {
	return m.SaveCOPReadModelKey(ctx, contracts.DefaultCOPReadModelKey, result)
}

// LoadCOPReadModelKey returns a defensive copy when found.
func (m *MemoryCOP) LoadCOPReadModelKey(_ context.Context, key string) (contracts.ProjectionResult, bool, error) {
	key = strings.TrimSpace(key)
	if key == "" {
		key = contracts.DefaultCOPReadModelKey
	}
	if m == nil {
		return contracts.ProjectionResult{}, false, nil
	}
	m.mu.RLock()
	defer m.mu.RUnlock()
	got, ok := m.byKey[key]
	if !ok {
		return contracts.ProjectionResult{}, false, nil
	}
	return cloneProjectionResult(got), true, nil
}

// SaveCOPReadModelKey stores a defensive copy under key.
func (m *MemoryCOP) SaveCOPReadModelKey(_ context.Context, key string, result contracts.ProjectionResult) error {
	key = strings.TrimSpace(key)
	if key == "" {
		key = contracts.DefaultCOPReadModelKey
	}
	if m == nil {
		return nil
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.byKey == nil {
		m.byKey = make(map[string]contracts.ProjectionResult)
	}
	m.byKey[key] = cloneProjectionResult(result)
	return nil
}

func cloneProjectionResult(in contracts.ProjectionResult) contracts.ProjectionResult {
	out := in
	if in.COP != nil {
		out.COP = make(map[string]any, len(in.COP))
		for k, v := range in.COP {
			out.COP[k] = v
		}
	}
	return out
}
