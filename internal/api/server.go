// Package api implements Mosaic's local v0.1 HTTP and SSE read surface.
// It is public by default for the synthetic demo; the injectable actor and
// policy seams are composition points, not a production auth implementation.
package api

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"mosaic.local/mosaic/internal/contracts"
	"mosaic.local/mosaic/internal/ontology/gen"
	"mosaic.local/mosaic/internal/stream"
)

const (
	// IdentityHeader selects optional public display metadata. It is not
	// authentication and never gates a request in the demo.
	IdentityHeader = "X-Mosaic-Demo-Identity"

	schemaVersion = "1.0.0"
)

// RecoveryReader is implemented by replay.Runner. It is kept local to avoid
// enlarging the stable P02 contracts merely for an HTTP adapter.
type RecoveryReader interface {
	Recover(context.Context) (contracts.ProjectionResult, error)
}

// Actor is the resolved, provider-neutral HTTP caller description. It carries
// no credential or token material. Role remains schema-valid when it is used
// to create an immutable audit record.
type Actor struct {
	ID     string
	Role   string
	Labels map[string]string
	Source string
}

// ActorResolver supplies a provider-neutral actor for an HTTP request. A later
// identity-aware deployment can replace this adapter in composition.
type ActorResolver interface {
	ResolveActor(context.Context, *http.Request) (Actor, error)
}

// Action identifies one public API capability for the configurable policy.
type Action string

const (
	ActionReadCOP           Action = "read_cop"
	ActionReadEvidence      Action = "read_evidence"
	ActionReadArtifact      Action = "read_artifact"
	ActionReadStream        Action = "read_stream"
	ActionReadOperations    Action = "read_operations"
	ActionReadAdvisories    Action = "read_advisories"
	ActionRequestBriefing   Action = "record_briefing_request"
	ActionRecordAudit       Action = "record_audit_action"
	ActionControlSimulation Action = "control_simulation"
	ActionReadSimulation    Action = "read_simulation"
	// Operator-driven model and decision routes (P41).
	ActionOperatorAnalyze   Action = "operator_analyze"
	ActionOperatorBrief     Action = "operator_brief"
	ActionOperatorInterpret Action = "operator_interpret"
	ActionOperatorDecide    Action = "operator_decide"
)

// PolicyDecision is intentionally small: the API can distinguish a configured
// denial from a resolver or policy outage without exposing implementation data.
type PolicyDecision struct {
	Allowed bool
	Reason  string
}

// ActionPolicy decides whether a resolved actor may use one demo capability.
// It is a seam for future composition, not an authorization implementation.
type ActionPolicy interface {
	Authorize(context.Context, Actor, Action) (PolicyDecision, error)
}

// PublicActorResolver is the open-demo default. The optional identity header
// changes only display metadata and the schema-valid audit display role; every
// caller is still the same public actor and every request remains public.
//
// The identity tokens are supplied by the composition (the selected profile),
// not named by this reusable package. A zero-value resolver never elevates any
// request and simply reports the anonymous public viewer.
type PublicActorResolver struct {
	ViewerIdentity     string
	SupervisorIdentity string
}

// ResolveActor returns the stable public demo actor for every request.
func (r PublicActorResolver) ResolveActor(_ context.Context, request *http.Request) (Actor, error) {
	mode := r.ViewerIdentity
	role := "viewer"
	if r.SupervisorIdentity != "" && request != nil && request.Header.Get(IdentityHeader) == r.SupervisorIdentity {
		mode = r.SupervisorIdentity
		role = "supervisor"
	}
	return Actor{
		ID:     "public-demo",
		Role:   role,
		Labels: map[string]string{"demo_mode": mode},
		Source: "public-demo-default",
	}, nil
}

// AllowDemoPolicy permits every known demo read and immutable audit-record
// write. It never authorizes an operational action because no such route or
// action client exists in this demo.
type AllowDemoPolicy struct{}

// Authorize returns the open-demo decision for a recognised API capability.
func (AllowDemoPolicy) Authorize(_ context.Context, _ Actor, action Action) (PolicyDecision, error) {
	switch action {
	case ActionReadCOP, ActionReadEvidence, ActionReadArtifact, ActionReadStream,
		ActionReadOperations, ActionRequestBriefing, ActionRecordAudit, ActionReadAdvisories,
		ActionControlSimulation, ActionReadSimulation,
		ActionOperatorAnalyze, ActionOperatorBrief, ActionOperatorInterpret, ActionOperatorDecide:
		return PolicyDecision{Allowed: true}, nil
	default:
		return PolicyDecision{Reason: "unknown demo capability"}, nil
	}
}

// Config supplies the already-wired deterministic replay and append-only
// persistence seams. The composition root chooses concrete P03/P06 instances.
// Simulation is optional until composition (P46) wires the P36 controller.
// Terra/Sol/Luna adapters are optional; operator model routes return 503 when
// the corresponding adapter is not composed.
type Config struct {
	Recovery        RecoveryReader
	Records         contracts.ImmutableRecordRepository
	Evidence        EvidenceResolver
	Operations      OperationsReader
	AdvisoryHistory contracts.AdvisoryHistoryReader
	AdvisoryMode    string
	Stream          *stream.Broker
	// Simulation is the optional interactive session controller (P36).
	// When nil, simulation routes return a structured 503.
	Simulation SimulationController
	// Terra/Sol/Luna are optional operator model adapters (P41). Composition
	// selects fixture or live clients; this package never dials a network.
	Terra contracts.TerraAdapter
	Sol   contracts.SolAdapter
	Luna  contracts.LunaAdapter
	// ProviderSelection is optional capability metadata (fixture vs live) for
	// operator responses. It never contains secrets.
	ProviderSelection contracts.AgentProviderSelection
	// BriefingRequester is the RequestedBy identity passed to Sol. Composition
	// should match the Sol service RequiredRequester when Sol is wired.
	BriefingRequester string
	Actors            ActorResolver
	Policy            ActionPolicy
	Version           string
	Clock             func() time.Time
	NewID             func() string
}

// Server composes the v0.1 HTTP handlers. It reads a COP only through the P06
// recovery seam and writes only immutable audit records through P03.
type Server struct {
	recovery          RecoveryReader
	records           contracts.ImmutableRecordRepository
	evidence          EvidenceResolver
	operations        OperationsReader
	advisoryHistory   contracts.AdvisoryHistoryReader
	advisoryMode      string
	stream            *stream.Broker
	simulation        SimulationController
	terra             contracts.TerraAdapter
	sol               contracts.SolAdapter
	luna              contracts.LunaAdapter
	providerSelection contracts.AgentProviderSelection
	briefingRequester string
	actors            ActorResolver
	policy            ActionPolicy
	version           string
	startedAt         time.Time
	clock             func() time.Time
	newID             func() string
}

// New rejects partial deterministic/evidence wiring while providing public
// actor and policy defaults. An operations reader is optional until a
// composition root selects the local SQLite adapter; its endpoint then reports
// an explicit unavailable read model rather than silently inventing telemetry.
func New(config Config) (*Server, error) {
	if config.Recovery == nil {
		return nil, errors.New("recovery reader is required")
	}
	if config.Records == nil {
		return nil, errors.New("immutable record repository is required")
	}
	if config.Evidence == nil {
		return nil, errors.New("evidence resolver is required")
	}
	if config.Stream == nil {
		config.Stream = stream.NewBroker()
	}
	if config.Operations == nil {
		config.Operations = unavailableOperationsReader{}
	}
	if config.AdvisoryHistory == nil {
		config.AdvisoryHistory = unavailableAdvisoryHistoryReader{}
	}
	if config.Actors == nil {
		config.Actors = PublicActorResolver{}
	}
	if config.Policy == nil {
		config.Policy = AllowDemoPolicy{}
	}
	if config.Version == "" {
		config.Version = "v0.1"
	}
	if config.Clock == nil {
		config.Clock = time.Now
	}
	if config.NewID == nil {
		config.NewID = randomID
	}
	startedAt := config.Clock().UTC()
	return &Server{
		recovery:          config.Recovery,
		records:           config.Records,
		evidence:          config.Evidence,
		operations:        config.Operations,
		advisoryHistory:   config.AdvisoryHistory,
		advisoryMode:      config.AdvisoryMode,
		stream:            config.Stream,
		simulation:        config.Simulation,
		terra:             config.Terra,
		sol:               config.Sol,
		luna:              config.Luna,
		providerSelection: config.ProviderSelection,
		briefingRequester: config.BriefingRequester,
		actors:            config.Actors,
		policy:            config.Policy,
		version:           config.Version,
		startedAt:         startedAt,
		clock:             config.Clock,
		newID:             config.NewID,
	}, nil
}

// Handler returns the versioned HTTP surface. No route is an operational
// command: public POSTs only append immutable audit records.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/v1/health", s.handleHealth)
	mux.HandleFunc("/api/v1/version", s.handleVersion)
	mux.HandleFunc("/api/v1/cop", s.handleCOP)
	mux.HandleFunc("/api/v1/evidence/", s.handleEvidence)
	mux.HandleFunc("/api/v1/artifacts/", s.handleArtifact)
	mux.HandleFunc("/api/v1/stream", s.handleStream)
	mux.HandleFunc("/api/v1/operations", s.handleOperations)
	mux.HandleFunc("/api/v1/advisories", s.handleAdvisories)
	mux.HandleFunc("/api/v1/briefings", s.handleBriefing)
	mux.HandleFunc("/api/v1/audit-actions", s.handleAuditAction)
	mux.HandleFunc("/api/v1/simulation/start", s.handleSimulationStart)
	mux.HandleFunc("/api/v1/simulation/reset", s.handleSimulationReset)
	mux.HandleFunc("/api/v1/simulation/status", s.handleSimulationStatus)
	mux.HandleFunc("/api/v1/simulation/end", s.handleSimulationEnd)
	mux.HandleFunc("/api/v1/simulation/stream", s.handleSimulationStream)
	mux.HandleFunc("/api/v1/operator/analyze", s.handleOperatorAnalyze)
	mux.HandleFunc("/api/v1/operator/brief", s.handleOperatorBrief)
	mux.HandleFunc("/api/v1/operator/interpret", s.handleOperatorInterpret)
	mux.HandleFunc("/api/v1/operator/approve", s.handleOperatorApprove)
	mux.HandleFunc("/api/v1/operator/annotate", s.handleOperatorAnnotate)
	mux.HandleFunc("/api/v1/operator/prepare-handoff", s.handleOperatorPrepareHandoff)
	return mux
}

// Publish sends a named, best-effort update to current local SSE clients. A
// reconnect always gets a deterministic COP snapshot before later updates.
func (s *Server) Publish(name string, data any) {
	if s == nil || strings.TrimSpace(name) == "" {
		return
	}
	s.stream.Publish(stream.Event{Name: name, Data: data})
}

func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	if !requireMethod(w, r, http.MethodGet) {
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"status": "ok"}, nil)
}

func (s *Server) handleVersion(w http.ResponseWriter, r *http.Request) {
	if !requireMethod(w, r, http.MethodGet) {
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"version": s.version, "api_version": "v1"}, nil)
}

func (s *Server) handleCOP(w http.ResponseWriter, r *http.Request) {
	if !requireMethod(w, r, http.MethodGet) || !s.authorize(w, r, ActionReadCOP) {
		return
	}
	result, ok := s.recoverCOP(w, r)
	if !ok {
		return
	}
	writeJSON(w, http.StatusOK, copPayload(result), nil)
}

func (s *Server) handleEvidence(w http.ResponseWriter, r *http.Request) {
	if !requireMethod(w, r, http.MethodGet) || !s.authorize(w, r, ActionReadEvidence) {
		return
	}
	kind, id, ok := tailPair(r.URL.Path, "/api/v1/evidence/")
	if !ok {
		writeJSON(w, http.StatusBadRequest, nil, apiError{Code: "invalid_evidence_path", Message: "evidence path must include a kind and ID"})
		return
	}

	var cop map[string]any
	if kind == "state_fact" {
		result, recovered := s.recoverCOP(w, r)
		if !recovered {
			return
		}
		cop = result.COP
	}
	resolution, err := s.evidence.Resolve(r.Context(), kind, id, cop)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, nil, apiError{Code: "evidence_resolution_failed", Message: "unable to resolve evidence"})
		return
	}
	if !resolution.Resolved {
		writeJSON(w, http.StatusNotFound, resolution, apiError{Code: "evidence_not_found", Message: resolution.Reason})
		return
	}
	writeJSON(w, http.StatusOK, resolution, nil)
}

func (s *Server) handleArtifact(w http.ResponseWriter, r *http.Request) {
	if !requireMethod(w, r, http.MethodGet) || !s.authorize(w, r, ActionReadArtifact) {
		return
	}
	kind, id, ok := tailPair(r.URL.Path, "/api/v1/artifacts/")
	if !ok || kind == "state_fact" {
		writeJSON(w, http.StatusBadRequest, nil, apiError{Code: "invalid_artifact_path", Message: "artifact path must include a persisted kind and ID"})
		return
	}
	resolution, err := s.evidence.Resolve(r.Context(), kind, id, nil)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, nil, apiError{Code: "artifact_resolution_failed", Message: "unable to resolve artifact"})
		return
	}
	if !resolution.Resolved {
		writeJSON(w, http.StatusNotFound, resolution, apiError{Code: "artifact_not_found", Message: resolution.Reason})
		return
	}
	writeJSON(w, http.StatusOK, resolution, nil)
}

func (s *Server) handleStream(w http.ResponseWriter, r *http.Request) {
	if !requireMethod(w, r, http.MethodGet) || !s.authorize(w, r, ActionReadStream) {
		return
	}
	flusher, ok := w.(http.Flusher)
	if !ok {
		writeJSON(w, http.StatusInternalServerError, nil, apiError{Code: "streaming_unsupported", Message: "response writer does not support streaming"})
		return
	}

	subscription := s.stream.Subscribe()
	defer subscription.Cancel()
	result, recovered := s.recoverCOP(w, r)
	if !recovered {
		return
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	if err := writeSSE(w, stream.Event{Name: "cop.snapshot", Data: copPayload(result)}); err != nil {
		return
	}
	flusher.Flush()

	for {
		select {
		case <-r.Context().Done():
			return
		case event, open := <-subscription.Events:
			if !open {
				return
			}
			if err := writeSSE(w, event); err != nil {
				return
			}
			flusher.Flush()
		}
	}
}

func (s *Server) handleOperations(w http.ResponseWriter, r *http.Request) {
	if !requireMethod(w, r, http.MethodGet) || !s.authorize(w, r, ActionReadOperations) {
		return
	}
	result, recovered := s.recoverCOP(w, r)
	if !recovered {
		return
	}
	snapshot, err := s.operations.ReadOperations(r.Context())
	if err != nil {
		writeJSON(w, http.StatusServiceUnavailable, nil, apiError{Code: "operations_unavailable", Message: "bounded operations telemetry is unavailable"})
		return
	}

	observedAt := s.clock().UTC()
	uptime := max(observedAt.Sub(s.startedAt), 0)
	metadata := s.stream.Metadata()
	writeJSON(w, http.StatusOK, operationsResponse{
		ObservedAt:             observedAt,
		LatestSourceReceivedAt: snapshot.LatestSourceReceivedAt,
		Service: operationsService{
			Version:       s.version,
			StartedAt:     s.startedAt,
			UptimeSeconds: int64(uptime / time.Second),
		},
		Recovery: operationsRecovery{
			Status:        "recovered",
			StateRevision: result.StateRevision,
			ProjectedAt:   result.ProjectedAt.UTC(),
		},
		Counts: snapshot.Counts,
		Stream: operationsStream{
			LocalSubscriberCount: metadata.SubscriberCount,
			LastPublished:        metadata.LastPublished,
		},
		Capabilities: s.operationsCapabilities(),
	}, nil)
}

func (s *Server) handleBriefing(w http.ResponseWriter, r *http.Request) {
	if !requireMethod(w, r, http.MethodPost) {
		return
	}
	caller, ok := s.resolveAndAuthorize(w, r, ActionRequestBriefing)
	if !ok {
		return
	}
	var request briefingRequest
	if !decodeJSON(w, r, &request) {
		return
	}
	if strings.TrimSpace(request.BriefingID) == "" {
		request.BriefingID = "briefing-" + s.newID()
	}
	if strings.TrimSpace(request.BriefingID) == "briefing-" {
		writeJSON(w, http.StatusInternalServerError, nil, apiError{Code: "audit_id_failed", Message: "could not create briefing audit identity"})
		return
	}

	audit, err := s.appendAudit(r.Context(), caller, "briefing_requested", "briefing", request.BriefingID, request.Note)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, nil, apiError{Code: "audit_append_failed", Message: "could not record briefing request"})
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]any{
		"briefing_id":  request.BriefingID,
		"audit_record": audit,
		"executed":     false,
	}, nil)
}

func (s *Server) handleAuditAction(w http.ResponseWriter, r *http.Request) {
	if !requireMethod(w, r, http.MethodPost) {
		return
	}
	caller, ok := s.resolveAndAuthorize(w, r, ActionRecordAudit)
	if !ok {
		return
	}
	var request auditActionRequest
	if !decodeJSON(w, r, &request) {
		return
	}
	if !validAuditAction(request.Action) || !validAuditTarget(request.TargetKind) || strings.TrimSpace(request.TargetID) == "" {
		writeJSON(w, http.StatusBadRequest, nil, apiError{Code: "invalid_audit_action", Message: "action, target_kind, and target_id must name a supported immutable review target"})
		return
	}
	resolution, err := s.evidence.Resolve(r.Context(), request.TargetKind, request.TargetID, nil)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, nil, apiError{Code: "artifact_resolution_failed", Message: "unable to resolve review target"})
		return
	}
	if !resolution.Resolved {
		writeJSON(w, http.StatusNotFound, resolution, apiError{Code: "review_target_not_found", Message: resolution.Reason})
		return
	}

	audit, err := s.appendAudit(r.Context(), caller, request.Action, request.TargetKind, request.TargetID, request.Note)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, nil, apiError{Code: "audit_append_failed", Message: "could not record review action"})
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"audit_record": audit, "executed": false}, nil)
}

func (s *Server) recoverCOP(w http.ResponseWriter, r *http.Request) (contracts.ProjectionResult, bool) {
	result, err := s.recovery.Recover(r.Context())
	if err != nil {
		writeJSON(w, http.StatusServiceUnavailable, nil, apiError{Code: "cop_unavailable", Message: "deterministic COP recovery is unavailable"})
		return contracts.ProjectionResult{}, false
	}
	return result, true
}

func (s *Server) authorize(w http.ResponseWriter, r *http.Request, action Action) bool {
	_, ok := s.resolveAndAuthorize(w, r, action)
	return ok
}

func (s *Server) resolveAndAuthorize(w http.ResponseWriter, r *http.Request, action Action) (Actor, bool) {
	caller, err := s.actors.ResolveActor(r.Context(), r)
	if err != nil {
		writeJSON(w, http.StatusServiceUnavailable, nil, apiError{Code: "actor_unavailable", Message: "public actor resolution is unavailable"})
		return Actor{}, false
	}
	decision, err := s.policy.Authorize(r.Context(), caller, action)
	if err != nil {
		writeJSON(w, http.StatusServiceUnavailable, nil, apiError{Code: "policy_unavailable", Message: "public action policy is unavailable"})
		return Actor{}, false
	}
	if !decision.Allowed {
		writeJSON(w, http.StatusForbidden, nil, apiError{Code: "action_denied", Message: "the configured demo policy denied this request"})
		return Actor{}, false
	}
	return caller, true
}

func (s *Server) appendAudit(ctx context.Context, caller Actor, action, targetKind, targetID, note string) (gen.AuditRecord, error) {
	if strings.TrimSpace(caller.ID) == "" || !validAuditRole(caller.Role) {
		return gen.AuditRecord{}, errors.New("resolved actor cannot create a schema-valid audit record")
	}
	recordID := "audit-" + s.newID()
	if recordID == "audit-" {
		return gen.AuditRecord{}, errors.New("audit identity generator returned an empty value")
	}
	record := gen.AuditRecord{
		SchemaVersion: schemaVersion,
		AuditRecordID: recordID,
		ActorID:       caller.ID,
		ActorRole:     caller.Role,
		Action:        action,
		TargetKind:    targetKind,
		TargetID:      targetID,
		Note:          note,
		CreatedAt:     s.clock().UTC().Format(time.RFC3339Nano),
	}
	if err := s.records.AppendAuditRecord(ctx, record); err != nil {
		return gen.AuditRecord{}, err
	}
	return record, nil
}

func validAuditRole(role string) bool {
	return role == "viewer" || role == "supervisor" || role == "system"
}

type briefingRequest struct {
	BriefingID string `json:"briefing_id"`
	Note       string `json:"note"`
}

type auditActionRequest struct {
	Action     string `json:"action"`
	TargetKind string `json:"target_kind"`
	TargetID   string `json:"target_id"`
	Note       string `json:"note"`
}

func validAuditAction(action string) bool {
	switch action {
	case "acknowledged", "rejected", "noted":
		return true
	default:
		return false
	}
}

func validAuditTarget(kind string) bool {
	return kind == "recommendation" || kind == "insight"
}

func tailPair(path, prefix string) (string, string, bool) {
	tail := strings.TrimPrefix(path, prefix)
	parts := strings.Split(tail, "/")
	if len(parts) != 2 || strings.TrimSpace(parts[0]) == "" || strings.TrimSpace(parts[1]) == "" {
		return "", "", false
	}
	return parts[0], parts[1], true
}

func requireMethod(w http.ResponseWriter, r *http.Request, want string) bool {
	if r.Method == want {
		return true
	}
	w.Header().Set("Allow", want)
	writeJSON(w, http.StatusMethodNotAllowed, nil, apiError{Code: "method_not_allowed", Message: fmt.Sprintf("method must be %s", want)})
	return false
}

func copPayload(result contracts.ProjectionResult) map[string]any {
	return map[string]any{
		"cop":            result.COP,
		"state_revision": result.StateRevision,
		"projected_at":   result.ProjectedAt.UTC().Format(time.RFC3339Nano),
	}
}

func decodeJSON(w http.ResponseWriter, r *http.Request, destination any) bool {
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(destination); err != nil {
		writeJSON(w, http.StatusBadRequest, nil, apiError{Code: "invalid_json", Message: "request body must be a valid JSON object"})
		return false
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		writeJSON(w, http.StatusBadRequest, nil, apiError{Code: "invalid_json", Message: "request body must contain one JSON object"})
		return false
	}
	return true
}

func writeSSE(w http.ResponseWriter, event stream.Event) error {
	if strings.TrimSpace(event.Name) == "" {
		return errors.New("SSE event name is required")
	}
	encoded, err := json.Marshal(event.Data)
	if err != nil {
		return fmt.Errorf("marshal SSE event: %w", err)
	}
	_, err = fmt.Fprintf(w, "event: %s\ndata: %s\n\n", event.Name, encoded)
	return err
}

type responseEnvelope struct {
	Data  any       `json:"data,omitempty"`
	Error *apiError `json:"error,omitempty"`
}

type apiError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

func writeJSON(w http.ResponseWriter, status int, data any, problem any) {
	var apiProblem *apiError
	switch value := problem.(type) {
	case nil:
	case apiError:
		apiProblem = &value
	case *apiError:
		apiProblem = value
	default:
		apiProblem = &apiError{Code: "internal_error", Message: "invalid API error envelope"}
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(responseEnvelope{Data: data, Error: apiProblem})
}

func randomID() string {
	bytes := make([]byte, 16)
	if _, err := rand.Read(bytes); err != nil {
		return ""
	}
	return hex.EncodeToString(bytes)
}

type advisoryInsight struct {
	gen.Insight
	Status string `json:"status"`
}

type advisoryRecommendation struct {
	gen.Recommendation
	Status string `json:"status"`
}

type unavailableAdvisoryHistoryReader struct{}

func (unavailableAdvisoryHistoryReader) ReadAdvisoryHistory(context.Context) (contracts.AdvisoryHistory, error) {
	return contracts.AdvisoryHistory{}, errors.New("advisory history reader is not composed")
}

func (s *Server) handleAdvisories(w http.ResponseWriter, r *http.Request) {
	if !requireMethod(w, r, http.MethodGet) || !s.authorize(w, r, ActionReadAdvisories) {
		return
	}
	result, recovered := s.recoverCOP(w, r)
	if !recovered {
		return
	}
	history, err := s.advisoryHistory.ReadAdvisoryHistory(r.Context())
	if err != nil {
		writeJSON(w, http.StatusServiceUnavailable, nil, apiError{Code: "advisory_history_unavailable", Message: "advisory history is unavailable"})
		return
	}

	// Filter Insights and Recommendations by recovered revision
	var filteredInsights []gen.Insight
	for _, ins := range history.Insights {
		if ins.StateRevision <= result.StateRevision {
			filteredInsights = append(filteredInsights, ins)
		}
	}

	var filteredRecommendations []gen.Recommendation
	for _, rec := range history.Recommendations {
		if rec.StateRevision <= result.StateRevision {
			filteredRecommendations = append(filteredRecommendations, rec)
		}
	}

	supersededInsightIDs := make(map[string]bool)
	for _, ins := range filteredInsights {
		if ins.LifecycleStatus == "obsolete" {
			supersededInsightIDs[ins.InsightID] = true
		}
		if ins.SupersedesInsightID != "" {
			supersededInsightIDs[ins.SupersedesInsightID] = true
		}
	}

	isInsightSuperseded := func(ins gen.Insight) bool {
		return ins.LifecycleStatus == "obsolete" || supersededInsightIDs[ins.InsightID]
	}

	advisoriesInsights := make([]advisoryInsight, 0, len(filteredInsights))
	for _, ins := range filteredInsights {
		var status string
		if isInsightSuperseded(ins) {
			status = "superseded"
		} else if ins.StateRevision == result.StateRevision {
			status = "current"
		} else if ins.StateRevision < result.StateRevision {
			status = "historical"
		} else {
			status = "historical"
		}
		advisoriesInsights = append(advisoriesInsights, advisoryInsight{
			Insight: ins,
			Status:  status,
		})
	}

	advisoriesRecommendations := make([]advisoryRecommendation, 0, len(filteredRecommendations))
	for _, rec := range filteredRecommendations {
		var status string
		citedInsightSuperseded := false
		for _, ev := range rec.Evidence {
			if m, ok := ev.(map[string]any); ok {
				kind, _ := m["target_kind"].(string)
				id, _ := m["target_id"].(string)
				if kind == "insight" {
					var citedIns *gen.Insight
					for i := range filteredInsights {
						if filteredInsights[i].InsightID == id {
							citedIns = &filteredInsights[i]
							break
						}
					}
					if citedIns != nil {
						if isInsightSuperseded(*citedIns) {
							citedInsightSuperseded = true
						}
					} else {
						if supersededInsightIDs[id] {
							citedInsightSuperseded = true
						}
					}
				}
			}
		}

		if rec.StateRevision < result.StateRevision || citedInsightSuperseded {
			status = "not_current"
		} else if rec.StateRevision == result.StateRevision {
			status = "current"
		} else {
			status = "not_current"
		}

		advisoriesRecommendations = append(advisoriesRecommendations, advisoryRecommendation{
			Recommendation: rec,
			Status:         status,
		})
	}

	advStatus := "unavailable"
	if s.advisoryMode == "fixture_composed" || s.advisoryMode == "fixture-composed" {
		advStatus = "fixture-composed"
	}
	advisoryModelRuns := make([]map[string]any, 0, len(history.ModelRuns))
	for _, run := range history.ModelRuns {
		advisoryModelRuns = append(advisoryModelRuns, map[string]any{
			"model_run_id":      run.ModelRunID,
			"agent":             run.Agent,
			"provider":          run.Provider,
			"model":             run.Model,
			"validation_status": run.ValidationStatus,
			"started_at":        run.StartedAt,
			"completed_at":      run.CompletedAt,
			"state_revision":    run.StateRevision,
			"output_ids":        run.OutputIds,
			"input_event_ids":   run.InputEventIds,
		})
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"insights":        advisoriesInsights,
		"recommendations": advisoriesRecommendations,
		"status":          advStatus,
		"audit_records":   history.AuditRecords,
		"model_runs":      advisoryModelRuns,
		"providers":       s.providerHints(),
	}, nil)
}
