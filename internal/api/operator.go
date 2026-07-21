package api

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"time"

	"mosaic.local/mosaic/internal/contracts"
	"mosaic.local/mosaic/internal/ontology/gen"
	"mosaic.local/mosaic/internal/sol"
	"mosaic.local/mosaic/internal/terra"
)

// operatorEvidenceRef is a compact, schema-aligned evidence pointer from the
// operator client. It never carries raw source payload bytes.
type operatorEvidenceRef struct {
	Kind        string `json:"kind"`
	ID          string `json:"id"`
	Explanation string `json:"explanation,omitempty"`
}

type operatorAnalyzeRequest struct {
	Evidence []operatorEvidenceRef `json:"evidence"`
	Note     string                `json:"note"`
}

type operatorBriefRequest struct {
	Insights []operatorInsightRef  `json:"insights"`
	Evidence []operatorEvidenceRef `json:"evidence"`
	Note     string                `json:"note"`
}

// operatorInsightRef is the minimal insight identity the client may supply for
// a Sol briefing. Text/assertions are optional structured citations only.
type operatorInsightRef struct {
	InsightID       string `json:"insight_id"`
	StateRevision   int64  `json:"state_revision,omitempty"`
	LifecycleStatus string `json:"lifecycle_status,omitempty"`
	SchemaVersion   string `json:"schema_version,omitempty"`
}

type operatorInterpretRequest struct {
	RawEventID       string         `json:"raw_event_id"`
	SchemaVersion    string         `json:"schema_version"`
	ReceivedAt       string         `json:"received_at"`
	ContentType      string         `json:"content_type"`
	PayloadBytesB64  string         `json:"payload_bytes_b64"`
	RawSha256        string         `json:"raw_sha256"`
	SourceOccurredAt string         `json:"source_occurred_at"`
	Source           map[string]any `json:"source"`
	Attributes       map[string]any `json:"attributes"`
}

type operatorDecisionRequest struct {
	TargetKind string `json:"target_kind"`
	TargetID   string `json:"target_id"`
	Note       string `json:"note"`
	// Recipient is used only by prepare-handoff (dispatch/maintenance).
	Recipient string `json:"recipient"`
}

// handleOperatorAnalyze runs Terra.Assess against the recovered COP and the
// operator-supplied evidence. Every attempt appends an executed:false audit;
// model refusals/failures return a clear status without mutating the COP.
func (s *Server) handleOperatorAnalyze(w http.ResponseWriter, r *http.Request) {
	if !requireMethod(w, r, http.MethodPost) {
		return
	}
	caller, ok := s.resolveAndAuthorize(w, r, ActionOperatorAnalyze)
	if !ok {
		return
	}
	if s.terra == nil {
		writeJSON(w, http.StatusServiceUnavailable, nil, apiError{
			Code:    "terra_unavailable",
			Message: "Terra assessment adapter is not configured",
		})
		return
	}
	var request operatorAnalyzeRequest
	if !decodeJSON(w, r, &request) {
		return
	}
	result, recovered := s.recoverCOP(w, r)
	if !recovered {
		return
	}

	input := contracts.TerraInput{
		StateRevision: result.StateRevision,
		COP:           result.COP,
		Evidence:      mapOperatorEvidence(request.Evidence),
	}
	output, assessErr := s.terra.Assess(r.Context(), input)

	note := strings.TrimSpace(request.Note)
	if note == "" {
		note = "operator analyze requested"
	}
	audit, err := s.appendAudit(r.Context(), caller, "noted", "system", "operator-analyze", note)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, nil, apiError{
			Code:    "audit_append_failed",
			Message: "could not record operator analyze audit",
		})
		return
	}

	status, httpStatus := modelOutcomeStatus(assessErr, output.ModelRun, terra.ErrAssessmentRefused, terra.ErrAssessmentFailed)
	payload := map[string]any{
		"executed":     false,
		"status":       status,
		"audit_record": audit,
		"providers":    s.providerHints(),
	}
	if output.ModelRun.ModelRunID != "" {
		payload["model_run"] = boundedModelRun(output.ModelRun)
	}
	if status == "ok" && strings.TrimSpace(output.Insight.InsightID) != "" {
		payload["insight"] = boundedInsight(output.Insight)
	}
	if assessErr != nil && status == "error" {
		writeJSON(w, http.StatusInternalServerError, payload, apiError{
			Code:    "analyze_failed",
			Message: "Terra assessment failed without a durable model run",
		})
		return
	}
	writeJSON(w, httpStatus, payload, nil)
}

// handleOperatorBrief runs Sol.Brief for an explicit operator request. Briefing
// is never automatic; supervisor identity is required at the API boundary.
func (s *Server) handleOperatorBrief(w http.ResponseWriter, r *http.Request) {
	if !requireMethod(w, r, http.MethodPost) {
		return
	}
	caller, ok := s.resolveAndAuthorize(w, r, ActionOperatorBrief)
	if !ok {
		return
	}
	if caller.Role != "supervisor" {
		writeJSON(w, http.StatusForbidden, nil, apiError{
			Code:    "supervisor_required",
			Message: "operator brief requires the supervisor demo identity",
		})
		return
	}
	if s.sol == nil {
		writeJSON(w, http.StatusServiceUnavailable, nil, apiError{
			Code:    "sol_unavailable",
			Message: "Sol briefing adapter is not configured",
		})
		return
	}
	var request operatorBriefRequest
	if !decodeJSON(w, r, &request) {
		return
	}
	result, recovered := s.recoverCOP(w, r)
	if !recovered {
		return
	}

	// Clients only send insight_id refs. Hydrate full Insight records from
	// advisory history (the durable store) so Sol can validate required fields.
	// The COP revision bounds which insight versions are visible.
	insights, hydrateErr := s.hydrateOperatorInsights(r.Context(), request.Insights, result.StateRevision)
	if hydrateErr != nil {
		writeJSON(w, http.StatusBadRequest, nil, apiError{
			Code:    "invalid_insights",
			Message: hydrateErr.Error(),
		})
		return
	}

	requestedBy := strings.TrimSpace(s.briefingRequester)
	if requestedBy == "" {
		if mode := strings.TrimSpace(caller.Labels["demo_mode"]); mode != "" {
			requestedBy = mode
		} else {
			requestedBy = caller.ID
		}
	}

	input := contracts.SolInput{
		StateRevision: result.StateRevision,
		COP:           result.COP,
		Insights:      insights,
		Evidence:      mapOperatorEvidence(request.Evidence),
		RequestedBy:   requestedBy,
	}
	output, briefErr := s.sol.Brief(r.Context(), input)

	briefingID := "briefing-" + s.newID()
	if strings.TrimSpace(output.Recommendation.RecommendationID) != "" {
		briefingID = output.Recommendation.RecommendationID
	}
	note := strings.TrimSpace(request.Note)
	if note == "" {
		note = "operator brief requested"
	}
	audit, err := s.appendAudit(r.Context(), caller, "briefing_requested", "briefing", briefingID, note)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, nil, apiError{
			Code:    "audit_append_failed",
			Message: "could not record operator brief audit",
		})
		return
	}

	status, httpStatus := modelOutcomeStatus(briefErr, output.ModelRun, sol.ErrBriefingRefused, sol.ErrBriefingFailed)
	if briefErr != nil && errors.Is(briefErr, sol.ErrSupervisorRequired) {
		status = "failed"
		httpStatus = http.StatusForbidden
	}
	payload := map[string]any{
		"executed":     false,
		"status":       status,
		"briefing_id":  briefingID,
		"audit_record": audit,
		"providers":    s.providerHints(),
	}
	if output.ModelRun.ModelRunID != "" {
		payload["model_run"] = boundedModelRun(output.ModelRun)
	}
	if status == "ok" && strings.TrimSpace(output.Recommendation.RecommendationID) != "" {
		payload["recommendation"] = boundedRecommendation(output.Recommendation)
	}
	if briefErr != nil && status == "error" {
		writeJSON(w, http.StatusInternalServerError, payload, apiError{
			Code:    "brief_failed",
			Message: "Sol briefing failed without a durable model run",
		})
		return
	}
	if briefErr != nil && errors.Is(briefErr, sol.ErrSupervisorRequired) {
		writeJSON(w, http.StatusForbidden, payload, apiError{
			Code:    "supervisor_required",
			Message: "Sol rejected the configured briefing requester identity",
		})
		return
	}
	writeJSON(w, httpStatus, payload, nil)
}

// handleOperatorInterpret runs Luna.Normalize on a provided raw-event envelope.
// Responses never re-echo the raw payload bytes.
func (s *Server) handleOperatorInterpret(w http.ResponseWriter, r *http.Request) {
	if !requireMethod(w, r, http.MethodPost) {
		return
	}
	caller, ok := s.resolveAndAuthorize(w, r, ActionOperatorInterpret)
	if !ok {
		return
	}
	if s.luna == nil {
		writeJSON(w, http.StatusServiceUnavailable, nil, apiError{
			Code:    "luna_unavailable",
			Message: "Luna interpret adapter is not configured",
		})
		return
	}
	var request operatorInterpretRequest
	if !decodeJSON(w, r, &request) {
		return
	}
	if strings.TrimSpace(request.RawEventID) == "" {
		writeJSON(w, http.StatusBadRequest, nil, apiError{
			Code:    "invalid_raw_event",
			Message: "raw_event_id is required",
		})
		return
	}

	raw := gen.RawEvent{
		RawEventID:       request.RawEventID,
		SchemaVersion:    request.SchemaVersion,
		ReceivedAt:       request.ReceivedAt,
		ContentType:      request.ContentType,
		PayloadBytesB64:  request.PayloadBytesB64,
		RawSha256:        request.RawSha256,
		SourceOccurredAt: request.SourceOccurredAt,
		Source:           request.Source,
		Attributes:       request.Attributes,
	}
	if strings.TrimSpace(raw.SchemaVersion) == "" {
		raw.SchemaVersion = schemaVersion
	}

	output, normalizeErr := s.luna.Normalize(r.Context(), raw)

	note := "operator interpret requested for " + request.RawEventID
	audit, err := s.appendAudit(r.Context(), caller, "noted", "system", "operator-interpret", note)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, nil, apiError{
			Code:    "audit_append_failed",
			Message: "could not record operator interpret audit",
		})
		return
	}

	status := lunaOutcomeStatus(output, normalizeErr)
	httpStatus := http.StatusOK
	if status == "error" && normalizeErr != nil {
		httpStatus = http.StatusUnprocessableEntity
	}
	payload := map[string]any{
		"executed":     false,
		"status":       status,
		"audit_record": audit,
		"providers":    s.providerHints(),
		"raw_event_id": request.RawEventID,
	}
	if strings.TrimSpace(output.Result.LunaResultID) != "" {
		payload["luna_result"] = boundedLunaResult(output.Result)
	}
	if strings.TrimSpace(output.Result.Status) != "" {
		payload["result_status"] = output.Result.Status
	}
	if output.ModelRun.ModelRunID != "" {
		payload["model_run"] = boundedModelRun(output.ModelRun)
	}
	if output.CanonicalEvent != nil && strings.TrimSpace(output.CanonicalEvent.CanonicalEventID) != "" {
		// Identifiers only — never re-echo source payload or full event body.
		payload["canonical_event_id"] = output.CanonicalEvent.CanonicalEventID
	}
	writeJSON(w, httpStatus, payload, nil)
}

// handleOperatorApprove records an immutable acknowledged audit (executed:false).
func (s *Server) handleOperatorApprove(w http.ResponseWriter, r *http.Request) {
	s.handleOperatorDecision(w, r, "acknowledged", "operator-approve")
}

// handleOperatorAnnotate records an immutable noted audit (executed:false).
func (s *Server) handleOperatorAnnotate(w http.ResponseWriter, r *http.Request) {
	s.handleOperatorDecision(w, r, "noted", "operator-annotate")
}

// handleOperatorPrepareHandoff records a reviewable handoff intent as a noted
// audit. It never claims delivery or contacts a real recipient.
func (s *Server) handleOperatorPrepareHandoff(w http.ResponseWriter, r *http.Request) {
	if !requireMethod(w, r, http.MethodPost) {
		return
	}
	caller, ok := s.resolveAndAuthorize(w, r, ActionOperatorDecide)
	if !ok {
		return
	}
	var request operatorDecisionRequest
	if !decodeJSON(w, r, &request) {
		return
	}
	recipient := strings.TrimSpace(request.Recipient)
	if recipient == "" {
		writeJSON(w, http.StatusBadRequest, nil, apiError{
			Code:    "invalid_handoff",
			Message: "recipient is required (e.g. dispatch or maintenance)",
		})
		return
	}
	targetKind := strings.TrimSpace(request.TargetKind)
	targetID := strings.TrimSpace(request.TargetID)
	if targetKind == "" {
		targetKind = "system"
	}
	if targetID == "" {
		targetID = "operator-prepare-handoff"
	}
	if !validOperatorAuditTarget(targetKind) {
		writeJSON(w, http.StatusBadRequest, nil, apiError{
			Code:    "invalid_audit_action",
			Message: "target_kind must be recommendation, insight, briefing, or system",
		})
		return
	}
	if targetKind == "recommendation" || targetKind == "insight" {
		if !s.resolveDecisionTarget(w, r, targetKind, targetID) {
			return
		}
	}

	note := strings.TrimSpace(request.Note)
	if note == "" {
		note = "handoff prepared for review"
	}
	// Schema-valid action is noted; recipient lives only in the note text so
	// the stored AuditRecord.Action never invents prepare_handoff.
	auditNote := "handoff recipient=" + recipient + "; " + note
	audit, err := s.appendAudit(r.Context(), caller, "noted", targetKind, targetID, auditNote)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, nil, apiError{
			Code:    "audit_append_failed",
			Message: "could not record handoff audit",
		})
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{
		"executed":       false,
		"delivered":      false,
		"handoff_status": "recorded",
		"recipient":      recipient,
		"audit_record":   audit,
		"message":        "handoff intent recorded for review; nothing was sent",
	}, nil)
}

func (s *Server) handleOperatorDecision(w http.ResponseWriter, r *http.Request, action, defaultTargetID string) {
	if !requireMethod(w, r, http.MethodPost) {
		return
	}
	caller, ok := s.resolveAndAuthorize(w, r, ActionOperatorDecide)
	if !ok {
		return
	}
	var request operatorDecisionRequest
	if !decodeJSON(w, r, &request) {
		return
	}
	targetKind := strings.TrimSpace(request.TargetKind)
	targetID := strings.TrimSpace(request.TargetID)
	if targetKind == "" || targetID == "" {
		writeJSON(w, http.StatusBadRequest, nil, apiError{
			Code:    "invalid_audit_action",
			Message: "target_kind and target_id are required",
		})
		return
	}
	if !validOperatorAuditTarget(targetKind) {
		writeJSON(w, http.StatusBadRequest, nil, apiError{
			Code:    "invalid_audit_action",
			Message: "target_kind must be recommendation, insight, briefing, or system",
		})
		return
	}
	if action != "acknowledged" && action != "noted" && action != "rejected" {
		writeJSON(w, http.StatusInternalServerError, nil, apiError{
			Code:    "invalid_audit_action",
			Message: "internal decision action is not schema-valid",
		})
		return
	}
	if targetKind == "recommendation" || targetKind == "insight" {
		if !s.resolveDecisionTarget(w, r, targetKind, targetID) {
			return
		}
	}

	note := strings.TrimSpace(request.Note)
	if note == "" {
		note = defaultTargetID
	}
	audit, err := s.appendAudit(r.Context(), caller, action, targetKind, targetID, note)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, nil, apiError{
			Code:    "audit_append_failed",
			Message: "could not record operator decision audit",
		})
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{
		"executed":     false,
		"audit_record": audit,
	}, nil)
}

func (s *Server) resolveDecisionTarget(w http.ResponseWriter, r *http.Request, targetKind, targetID string) bool {
	resolution, err := s.evidence.Resolve(r.Context(), targetKind, targetID, nil)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, nil, apiError{
			Code:    "artifact_resolution_failed",
			Message: "unable to resolve review target",
		})
		return false
	}
	if !resolution.Resolved {
		writeJSON(w, http.StatusNotFound, resolution, apiError{
			Code:    "review_target_not_found",
			Message: resolution.Reason,
		})
		return false
	}
	return true
}

func validOperatorAuditTarget(kind string) bool {
	switch kind {
	case "recommendation", "insight", "briefing", "system":
		return true
	default:
		return false
	}
}

func mapOperatorEvidence(refs []operatorEvidenceRef) []gen.Evidence {
	if len(refs) == 0 {
		return nil
	}
	out := make([]gen.Evidence, 0, len(refs))
	for _, ref := range refs {
		kind := strings.TrimSpace(ref.Kind)
		id := strings.TrimSpace(ref.ID)
		if kind == "" || id == "" {
			continue
		}
		out = append(out, gen.Evidence{
			SchemaVersion: schemaVersion,
			EvidenceID:    kind + ":" + id,
			TargetKind:    kind,
			TargetID:      id,
			Explanation:   ref.Explanation,
			CreatedAt:     time.Now().UTC().Format(time.RFC3339Nano),
		})
	}
	return out
}

// hydrateOperatorInsights resolves operator-supplied insight_id refs to full
// Insight records from advisory history. Partial request bodies never carry
// assertions/evidence/confidence; Sol requires the complete schema-valid
// records. stateRevision is the recovered COP revision — only insights at or
// before that revision are eligible.
func (s *Server) hydrateOperatorInsights(ctx context.Context, refs []operatorInsightRef, stateRevision int64) ([]gen.Insight, error) {
	if len(refs) == 0 {
		return nil, nil
	}
	history, err := s.advisoryHistory.ReadAdvisoryHistory(ctx)
	if err != nil {
		return nil, errors.New("advisory history is unavailable for insight hydration")
	}
	// Prefer the newest version of each insight_id that is not future to the COP.
	byID := make(map[string]gen.Insight, len(history.Insights))
	for _, insight := range history.Insights {
		if insight.StateRevision > stateRevision {
			continue
		}
		existing, ok := byID[insight.InsightID]
		if !ok || insight.StateRevision >= existing.StateRevision {
			byID[insight.InsightID] = insight
		}
	}
	out := make([]gen.Insight, 0, len(refs))
	seen := make(map[string]struct{}, len(refs))
	for _, ref := range refs {
		id := strings.TrimSpace(ref.InsightID)
		if id == "" {
			continue
		}
		if _, dup := seen[id]; dup {
			return nil, errors.New("duplicate insight_id " + id)
		}
		seen[id] = struct{}{}
		insight, ok := byID[id]
		if !ok {
			return nil, errors.New("insight " + id + " not found at current COP revision")
		}
		out = append(out, insight)
	}
	return out, nil
}

// modelOutcomeStatus maps adapter errors + ModelRun validation into a stable
// operator status string. Handled refusals/failures are not 500s.
func modelOutcomeStatus(err error, run gen.ModelRun, refused, failed error) (status string, httpStatus int) {
	httpStatus = http.StatusOK
	if err == nil {
		if run.ValidationStatus == "valid" || run.ValidationStatus == "" {
			return "ok", httpStatus
		}
		switch run.ValidationStatus {
		case "refused":
			return "refused", http.StatusOK
		case "failed", "timed_out", "invalid":
			return run.ValidationStatus, http.StatusOK
		default:
			return "ok", httpStatus
		}
	}
	if refused != nil && errors.Is(err, refused) {
		return "refused", http.StatusOK
	}
	if failed != nil && errors.Is(err, failed) {
		return "failed", http.StatusOK
	}
	if run.ModelRunID != "" {
		switch run.ValidationStatus {
		case "refused":
			return "refused", http.StatusOK
		case "timed_out":
			return "timed_out", http.StatusOK
		case "invalid":
			return "invalid", http.StatusOK
		default:
			return "failed", http.StatusOK
		}
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return "unavailable", http.StatusServiceUnavailable
	}
	return "error", http.StatusInternalServerError
}

func lunaOutcomeStatus(output contracts.LunaOutput, err error) string {
	if err != nil {
		if output.ModelRun.ModelRunID != "" {
			switch output.ModelRun.ValidationStatus {
			case "refused":
				return "refused"
			case "failed", "timed_out", "invalid":
				return output.ModelRun.ValidationStatus
			}
			return "failed"
		}
		return "error"
	}
	if output.ModelRun.ValidationStatus == "refused" {
		return "refused"
	}
	if output.ModelRun.ValidationStatus == "failed" || output.ModelRun.ValidationStatus == "timed_out" {
		return output.ModelRun.ValidationStatus
	}
	if strings.TrimSpace(output.Result.Status) != "" {
		switch output.Result.Status {
		case "accepted", "repaired":
			return "ok"
		case "quarantined", "rejected":
			return output.Result.Status
		}
	}
	return "ok"
}

func boundedModelRun(run gen.ModelRun) map[string]any {
	out := map[string]any{
		"model_run_id":      run.ModelRunID,
		"agent":             run.Agent,
		"provider":          run.Provider,
		"model":             run.Model,
		"validation_status": run.ValidationStatus,
		"prompt_version":    run.PromptVersion,
		"started_at":        run.StartedAt,
		"completed_at":      run.CompletedAt,
		"state_revision":    run.StateRevision,
		"response_id":       run.ResponseID,
		"output_ids":        run.OutputIds,
		"input_event_ids":   run.InputEventIds,
	}
	if detail := strings.TrimSpace(run.FailureDetail); detail != "" {
		if len(detail) > 512 {
			detail = detail[:512]
		}
		out["failure_detail"] = detail
	}
	return out
}

func boundedInsight(insight gen.Insight) map[string]any {
	return map[string]any{
		"insight_id":            insight.InsightID,
		"state_revision":        insight.StateRevision,
		"lifecycle_status":      insight.LifecycleStatus,
		"schema_version":        insight.SchemaVersion,
		"created_at":            insight.CreatedAt,
		"evidence":              insight.Evidence,
		"assertions":            insight.Assertions,
		"confidence":            insight.Confidence,
		"supersedes_insight_id": insight.SupersedesInsightID,
	}
}

func boundedRecommendation(rec gen.Recommendation) map[string]any {
	return map[string]any{
		"recommendation_id": rec.RecommendationID,
		"state_revision":    rec.StateRevision,
		"schema_version":    rec.SchemaVersion,
		"created_at":        rec.CreatedAt,
		"text":              rec.Text,
		"evidence":          rec.Evidence,
	}
}

func boundedLunaResult(result gen.LunaResult) map[string]any {
	return map[string]any{
		"luna_result_id":     result.LunaResultID,
		"raw_event_id":       result.RawEventID,
		"canonical_event_id": result.CanonicalEventID,
		"status":             result.Status,
		"reason":             result.Reason,
		"schema_version":     result.SchemaVersion,
		"created_at":         result.CreatedAt,
	}
}

func (s *Server) providerHints() map[string]string {
	out := map[string]string{}
	if s.providerSelection == nil {
		// Composition-owned defaults when selection is not supplied.
		if s.terra != nil {
			out["terra"] = string(contracts.ProviderFixture)
		}
		if s.sol != nil {
			out["sol"] = string(contracts.ProviderFixture)
		}
		if s.luna != nil {
			out["luna"] = string(contracts.ProviderFixture)
		}
		return out
	}
	for agent, provider := range s.providerSelection {
		out[agent] = string(provider)
	}
	return out
}
