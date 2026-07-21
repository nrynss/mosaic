package api

import (
	"strings"
	"sync"

	"mosaic.local/mosaic/internal/contracts"
	"mosaic.local/mosaic/internal/ontology/gen"
)

// SessionAdvisoryView is an in-memory, session-scoped index of advisory record
// ids. Ontology insight/recommendation schemas do not carry session_id (C3
// deliberately avoids rewriting those JSON schemas); composition records ids
// here when Terra/Sol produce artifacts during a session so GET /advisories
// can filter to the active epoch.
//
// Choice (documented for handoff):
//   - Demo / SQLite: this in-memory view (and optional empty-when-no-session).
//   - Postgres durable: migration 0004 session_advisory_index can mirror the
//     same mapping; this type stays the unit-testable seam either way.
//
// When a session has no recorded ids, Filter returns empty advisory lists
// (strict isolation). Pass-through of the full store is intentionally not
// performed — that would leak prior sessions onto the board.
type SessionAdvisoryView struct {
	mu   sync.RWMutex
	byID map[string]*sessionAdvisoryKeys
}

type sessionAdvisoryKeys struct {
	insights        map[string]struct{}
	recommendations map[string]struct{}
	modelRuns       map[string]struct{}
	auditRecords    map[string]struct{}
}

// NewSessionAdvisoryView returns an empty index.
func NewSessionAdvisoryView() *SessionAdvisoryView {
	return &SessionAdvisoryView{byID: make(map[string]*sessionAdvisoryKeys)}
}

// Record associates a persisted advisory artifact with a session epoch.
// kind is one of insight, recommendation, model_run, audit_record.
// Unknown kinds and empty ids are ignored.
func (v *SessionAdvisoryView) Record(sessionID, kind, recordID string) {
	if v == nil {
		return
	}
	sessionID = strings.TrimSpace(sessionID)
	recordID = strings.TrimSpace(recordID)
	kind = strings.TrimSpace(kind)
	if sessionID == "" || recordID == "" {
		return
	}
	v.mu.Lock()
	defer v.mu.Unlock()
	keys := v.byID[sessionID]
	if keys == nil {
		keys = &sessionAdvisoryKeys{
			insights:        make(map[string]struct{}),
			recommendations: make(map[string]struct{}),
			modelRuns:       make(map[string]struct{}),
			auditRecords:    make(map[string]struct{}),
		}
		v.byID[sessionID] = keys
	}
	switch kind {
	case "insight":
		keys.insights[recordID] = struct{}{}
	case "recommendation":
		keys.recommendations[recordID] = struct{}{}
	case "model_run":
		keys.modelRuns[recordID] = struct{}{}
	case "audit_record":
		keys.auditRecords[recordID] = struct{}{}
	}
}

// Filter returns only the advisory records registered for sessionID.
// Unknown sessions yield empty lists (not the full history).
func (v *SessionAdvisoryView) Filter(sessionID string, history contracts.AdvisoryHistory) contracts.AdvisoryHistory {
	if v == nil {
		return history
	}
	sessionID = strings.TrimSpace(sessionID)
	if sessionID == "" {
		return contracts.AdvisoryHistory{}
	}
	v.mu.RLock()
	keys := v.byID[sessionID]
	v.mu.RUnlock()
	if keys == nil {
		return contracts.AdvisoryHistory{}
	}

	out := contracts.AdvisoryHistory{
		Insights:        filterInsights(history.Insights, keys.insights),
		Recommendations: filterRecommendations(history.Recommendations, keys.recommendations),
		ModelRuns:       filterModelRuns(history.ModelRuns, keys.modelRuns),
		AuditRecords:    filterAuditRecords(history.AuditRecords, keys.auditRecords),
	}
	return out
}

func filterInsights(in []gen.Insight, allow map[string]struct{}) []gen.Insight {
	if len(allow) == 0 {
		return nil
	}
	out := make([]gen.Insight, 0, len(in))
	for _, item := range in {
		if _, ok := allow[item.InsightID]; ok {
			out = append(out, item)
		}
	}
	return out
}

func filterRecommendations(in []gen.Recommendation, allow map[string]struct{}) []gen.Recommendation {
	if len(allow) == 0 {
		return nil
	}
	out := make([]gen.Recommendation, 0, len(in))
	for _, item := range in {
		if _, ok := allow[item.RecommendationID]; ok {
			out = append(out, item)
		}
	}
	return out
}

func filterModelRuns(in []gen.ModelRun, allow map[string]struct{}) []gen.ModelRun {
	if len(allow) == 0 {
		return nil
	}
	out := make([]gen.ModelRun, 0, len(in))
	for _, item := range in {
		if _, ok := allow[item.ModelRunID]; ok {
			out = append(out, item)
		}
	}
	return out
}

func filterAuditRecords(in []gen.AuditRecord, allow map[string]struct{}) []gen.AuditRecord {
	if len(allow) == 0 {
		return nil
	}
	out := make([]gen.AuditRecord, 0, len(in))
	for _, item := range in {
		if _, ok := allow[item.AuditRecordID]; ok {
			out = append(out, item)
		}
	}
	return out
}

// emptyAdvisoryPayload is the board-empty advisories response body.
// providers may be a map[string]string; cassette mode is filled by the caller
// via withCassetteMode when composing the full empty payload.
func emptyAdvisoryPayload(status string, providers any) map[string]any {
	if status == "" {
		status = "unavailable"
	}
	return map[string]any{
		"insights":        []advisoryInsight{},
		"recommendations": []advisoryRecommendation{},
		"status":          status,
		"audit_records":   []gen.AuditRecord{},
		"model_runs":      []map[string]any{},
		"providers":       providers,
	}
}

// withCassetteMode adds the process-level cassette_mode field to an advisories payload.
func (s *Server) withCassetteMode(payload map[string]any) map[string]any {
	if payload == nil {
		payload = map[string]any{}
	}
	payload["cassette_mode"] = s.effectiveCassetteMode()
	return payload
}

// emptyCOPResult is the board-empty projection used when no session is active.
func emptyCOPResult() contracts.ProjectionResult {
	return contracts.ProjectionResult{
		StateRevision: 0,
		COP:           map[string]any{},
	}
}
