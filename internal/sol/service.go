package sol

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"mosaic.local/mosaic/internal/contracts"
	"mosaic.local/mosaic/internal/ontology/gen"
)

const (
	// SupervisorIdentity is the only local demo identity permitted to request a
	// Sol briefing. It deliberately matches P08's fixed role boundary without
	// importing its HTTP package.
	SupervisorIdentity = "supervisor-demo"
	schemaVersion      = "1.0.0"
)

var (
	// ErrInvalidBriefing means a briefing attempt violated an input, schema,
	// evidence, insight, revision, or role policy. Its ModelRun is durable but
	// it creates no Recommendation.
	ErrInvalidBriefing = errors.New("invalid Sol briefing")

	// ErrBriefingRefused means the structured client explicitly declined the
	// briefing. The ModelRun remains durable and no Recommendation is written.
	ErrBriefingRefused = errors.New("Sol briefing refused")

	// ErrBriefingFailed means the structured client could not return a usable
	// response. The ModelRun remains durable and no Recommendation is written.
	ErrBriefingFailed = errors.New("Sol briefing failed")

	// ErrSupervisorRequired makes the fixed-demo-role policy testable without
	// treating a viewer request as a model or operational action.
	ErrSupervisorRequired = errors.New("supervisor-demo identity is required")
)

// StructuredClient is the deliberately narrow model seam. It sees a committed
// COP serialization, its revision, active Insights, permitted evidence, and
// the fixed requester identity only. It has no raw payload, repository, tool,
// shell, or operational-action access.
type StructuredClient interface {
	Brief(context.Context, Request) (Response, error)
}

// Resolver verifies that the supplied active Insights and explicit evidence
// are already durable and resolvable at the committed state revision. The
// composition root provides this read-only adapter; Sol never queries or
// changes operational storage directly.
type Resolver interface {
	ResolveEvidence(context.Context, int64, []gen.Evidence) error
	ResolveInsights(context.Context, int64, []gen.Insight) error
}

// Request is the complete least-privilege input allowed across the Sol model
// boundary. SerializedCOP is defensive serialization, never a mutable map
// shared with the caller.
type Request struct {
	StateRevision int64
	SerializedCOP json.RawMessage
	Insights      []gen.Insight
	Evidence      []gen.Evidence
	RequestedBy   string
}

// Response is transport metadata plus a JSON representation of one
// Recommendation. A refusal is explicit and has no RecommendationJSON. The
// service, not the client, creates the authoritative ModelRun record.
type Response struct {
	RecommendationJSON json.RawMessage
	ResponseID         string
	RefusalDetail      string
}

// Config supplies deterministic policy dependencies. No runtime model,
// network client, API server, or operational system is constructed here.
type Config struct {
	Client        StructuredClient
	Resolver      Resolver
	Records       contracts.ImmutableRecordRepository
	Validator     *SchemaValidator
	PromptVersion string
	Provider      string
	Model         string
	Clock         func() time.Time
	NewModelRunID func() string
}

// Service implements contracts.SolAdapter and appends only immutable briefing
// artifacts. It deliberately has no projector, API, dispatcher, or action
// client, so it cannot mutate a COP or execute an operational command.
type Service struct {
	client        StructuredClient
	resolver      Resolver
	records       contracts.ImmutableRecordRepository
	validator     *SchemaValidator
	promptVersion string
	provider      string
	model         string
	clock         func() time.Time
	newModelRunID func() string
}

// New rejects partial wiring so every Sol attempt can produce a valid,
// append-only ModelRun and perform its evidence checks.
func New(config Config) (*Service, error) {
	if config.Client == nil {
		return nil, errors.New("Sol structured client is required")
	}
	if config.Resolver == nil {
		return nil, errors.New("Sol resolver is required")
	}
	if config.Records == nil {
		return nil, errors.New("immutable record repository is required")
	}
	if config.Validator == nil {
		return nil, errors.New("Sol schema validator is required")
	}
	if strings.TrimSpace(config.PromptVersion) == "" {
		return nil, errors.New("Sol prompt version is required")
	}
	if strings.TrimSpace(config.Provider) == "" {
		return nil, errors.New("Sol provider is required")
	}
	if strings.TrimSpace(config.Model) == "" {
		return nil, errors.New("Sol model is required")
	}
	if config.Clock == nil {
		config.Clock = time.Now
	}
	if config.NewModelRunID == nil {
		config.NewModelRunID = newModelRunID
	}
	return &Service{
		client:        config.Client,
		resolver:      config.Resolver,
		records:       config.Records,
		validator:     config.Validator,
		promptVersion: config.PromptVersion,
		provider:      config.Provider,
		model:         config.Model,
		clock:         config.Clock,
		newModelRunID: config.NewModelRunID,
	}, nil
}

// Brief produces a bounded, supervisor-review-only Recommendation from
// committed structured state. Every attempt appends one ModelRun. A refusal,
// client failure, malformed output, role violation, or policy mismatch creates
// no Recommendation and cannot mutate the COP or trigger an external action.
func (s *Service) Brief(ctx context.Context, input contracts.SolInput) (contracts.SolOutput, error) {
	started := s.clock().UTC()
	request, err := s.prepareRequest(input)
	if err != nil {
		return s.reject(ctx, input, started, s.clock().UTC(), "", err)
	}
	if err := s.resolver.ResolveEvidence(ctx, request.StateRevision, cloneEvidence(request.Evidence)); err != nil {
		return s.reject(ctx, input, started, s.clock().UTC(), "", fmt.Errorf("resolve permitted evidence: %w", err))
	}
	if err := s.resolver.ResolveInsights(ctx, request.StateRevision, cloneInsights(request.Insights)); err != nil {
		return s.reject(ctx, input, started, s.clock().UTC(), "", fmt.Errorf("resolve active Insights: %w", err))
	}

	response, clientErr := s.client.Brief(ctx, cloneRequest(request))
	completed := s.clock().UTC()
	if clientErr != nil {
		status := "failed"
		if errors.Is(clientErr, context.DeadlineExceeded) || errors.Is(ctx.Err(), context.DeadlineExceeded) {
			status = "timed_out"
		}
		output, persistErr := s.persistFailure(ctx, input, started, completed, status, clientErr.Error(), "")
		if persistErr != nil {
			return output, persistErr
		}
		return output, fmt.Errorf("%w: %v", ErrBriefingFailed, clientErr)
	}
	if strings.TrimSpace(response.RefusalDetail) != "" {
		output, persistErr := s.persistFailure(ctx, input, started, completed, "refused", response.RefusalDetail, response.ResponseID)
		if persistErr != nil {
			return output, persistErr
		}
		return output, fmt.Errorf("%w: %s", ErrBriefingRefused, response.RefusalDetail)
	}

	var recommendation gen.Recommendation
	if err := json.Unmarshal(response.RecommendationJSON, &recommendation); err != nil {
		return s.reject(ctx, input, started, completed, response.ResponseID, fmt.Errorf("decode Recommendation JSON: %w", err))
	}
	if err := s.validateCandidate(input, recommendation); err != nil {
		return s.reject(ctx, input, started, completed, response.ResponseID, err)
	}

	run := s.modelRun(input, started, completed, "valid", "", response.ResponseID, []string{recommendation.RecommendationID})
	if err := s.validator.ValidateModelRun(run); err != nil {
		return contracts.SolOutput{}, fmt.Errorf("validate generated Sol model run: %w", err)
	}
	if err := s.records.AppendModelRun(ctx, run); err != nil {
		return contracts.SolOutput{ModelRun: run}, fmt.Errorf("persist Sol model run: %w", err)
	}
	if err := s.records.AppendRecommendation(ctx, recommendation); err != nil {
		return contracts.SolOutput{Recommendation: recommendation, ModelRun: run}, fmt.Errorf("persist Sol Recommendation: %w", err)
	}
	return contracts.SolOutput{Recommendation: recommendation, ModelRun: run}, nil
}

func (s *Service) prepareRequest(input contracts.SolInput) (Request, error) {
	if input.RequestedBy != SupervisorIdentity {
		return Request{}, fmt.Errorf("%w: got %q", ErrSupervisorRequired, input.RequestedBy)
	}
	if input.StateRevision < 1 {
		return Request{}, errors.New("state revision must be positive")
	}
	if input.COP == nil {
		return Request{}, errors.New("COP is required")
	}
	if len(input.Insights) == 0 {
		return Request{}, errors.New("at least one active Insight is required")
	}
	if len(input.Evidence) == 0 {
		return Request{}, errors.New("at least one permitted evidence reference is required")
	}
	serializedCOP, err := json.Marshal(input.COP)
	if err != nil {
		return Request{}, fmt.Errorf("serialize COP: %w", err)
	}
	insights := cloneInsights(input.Insights)
	seenInsightIDs := make(map[string]struct{}, len(insights))
	for _, insight := range insights {
		if err := s.validator.ValidateInsight(insight); err != nil {
			return Request{}, fmt.Errorf("validate active Insight %q: %w", insight.InsightID, err)
		}
		if insight.LifecycleStatus != "active" {
			return Request{}, fmt.Errorf("Insight %q is not active", insight.InsightID)
		}
		if _, exists := seenInsightIDs[insight.InsightID]; exists {
			return Request{}, fmt.Errorf("active Insight %q is duplicated", insight.InsightID)
		}
		seenInsightIDs[insight.InsightID] = struct{}{}
	}
	evidence := cloneEvidence(input.Evidence)
	for _, item := range evidence {
		if err := s.validator.ValidateEvidence(item); err != nil {
			return Request{}, fmt.Errorf("validate permitted evidence %q: %w", item.EvidenceID, err)
		}
		if item.TargetKind == "insight" {
			if _, exists := seenInsightIDs[item.TargetID]; !exists {
				return Request{}, fmt.Errorf("permitted evidence references unavailable Insight %q", item.TargetID)
			}
		}
	}
	return Request{
		StateRevision: input.StateRevision,
		SerializedCOP: append(json.RawMessage(nil), serializedCOP...),
		Insights:      insights,
		Evidence:      evidence,
		RequestedBy:   input.RequestedBy,
	}, nil
}

func (s *Service) reject(ctx context.Context, input contracts.SolInput, started, completed time.Time, responseID string, cause error) (contracts.SolOutput, error) {
	output, persistErr := s.persistFailure(ctx, input, started, completed, "invalid", cause.Error(), responseID)
	if persistErr != nil {
		return output, persistErr
	}
	return output, fmt.Errorf("%w: %w", ErrInvalidBriefing, cause)
}

func (s *Service) persistFailure(
	ctx context.Context,
	input contracts.SolInput,
	started, completed time.Time,
	status, detail, responseID string,
) (contracts.SolOutput, error) {
	run := s.modelRun(input, started, completed, status, detail, responseID, nil)
	if err := s.validator.ValidateModelRun(run); err != nil {
		return contracts.SolOutput{ModelRun: run}, fmt.Errorf("validate generated Sol failure model run: %w", err)
	}
	if err := s.records.AppendModelRun(ctx, run); err != nil {
		return contracts.SolOutput{ModelRun: run}, fmt.Errorf("persist Sol failure model run: %w", err)
	}
	return contracts.SolOutput{ModelRun: run}, nil
}

func (s *Service) modelRun(
	input contracts.SolInput,
	started, completed time.Time,
	status, detail, responseID string,
	outputIDs []string,
) gen.ModelRun {
	inputIDs := inputArtifactIDs(input)
	outputs := make([]any, len(outputIDs))
	for index, id := range outputIDs {
		outputs[index] = id
	}
	run := gen.ModelRun{
		SchemaVersion:       schemaVersion,
		ModelRunID:          s.newModelRunID(),
		Agent:               "sol",
		Provider:            s.provider,
		Model:               s.model,
		PromptVersion:       s.promptVersion,
		OutputSchemaVersion: schemaVersion,
		InputEventIds:       stringsToAny(inputIDs),
		OutputIds:           outputs,
		ValidationStatus:    status,
		ResponseID:          responseID,
		FailureDetail:       detail,
		StartedAt:           started.Format(time.RFC3339Nano),
		CompletedAt:         completed.Format(time.RFC3339Nano),
	}
	if input.StateRevision > 0 {
		run.StateRevision = input.StateRevision
	}
	return run
}

func (s *Service) validateCandidate(input contracts.SolInput, recommendation gen.Recommendation) error {
	if err := s.validator.ValidateRecommendation(recommendation); err != nil {
		return err
	}
	if recommendation.StateRevision != input.StateRevision {
		return fmt.Errorf("Recommendation state revision %d does not match requested revision %d", recommendation.StateRevision, input.StateRevision)
	}
	if !sameEvidence(recommendation.Evidence, input.Evidence) {
		return errors.New("Recommendation evidence does not exactly match permitted evidence")
	}
	active := make(map[string]struct{}, len(input.Insights))
	for _, insight := range input.Insights {
		active[insight.InsightID] = struct{}{}
	}
	for _, reference := range recommendationEvidence(recommendation.Evidence) {
		if reference.TargetKind == "insight" {
			if _, exists := active[reference.TargetID]; !exists {
				return fmt.Errorf("Recommendation references unavailable Insight %q", reference.TargetID)
			}
		}
	}
	return nil
}

func sameEvidence(candidate []any, permitted []gen.Evidence) bool {
	if len(candidate) != len(permitted) {
		return false
	}
	candidateKeys := make([]string, 0, len(candidate))
	for _, value := range candidate {
		encoded, err := json.Marshal(value)
		if err != nil {
			return false
		}
		var reference evidenceReference
		if err := json.Unmarshal(encoded, &reference); err != nil || !reference.valid() {
			return false
		}
		candidateKeys = append(candidateKeys, reference.key())
	}
	permittedKeys := make([]string, 0, len(permitted))
	for _, evidence := range permitted {
		permittedKeys = append(permittedKeys, evidenceReference{
			TargetKind:  evidence.TargetKind,
			TargetID:    evidence.TargetID,
			JSONPointer: evidence.JsonPointer,
			Explanation: evidence.Explanation,
		}.key())
	}
	sort.Strings(candidateKeys)
	sort.Strings(permittedKeys)
	for index := range candidateKeys {
		if candidateKeys[index] != permittedKeys[index] {
			return false
		}
	}
	return true
}

func recommendationEvidence(values []any) []evidenceReference {
	references := make([]evidenceReference, 0, len(values))
	for _, value := range values {
		encoded, err := json.Marshal(value)
		if err != nil {
			continue
		}
		var reference evidenceReference
		if err := json.Unmarshal(encoded, &reference); err == nil && reference.valid() {
			references = append(references, reference)
		}
	}
	return references
}

type evidenceReference struct {
	TargetKind  string `json:"target_kind"`
	TargetID    string `json:"target_id"`
	JSONPointer string `json:"json_pointer,omitempty"`
	Explanation string `json:"explanation"`
}

func (r evidenceReference) valid() bool {
	return strings.TrimSpace(r.TargetKind) != "" && strings.TrimSpace(r.TargetID) != "" && strings.TrimSpace(r.Explanation) != ""
}

func (r evidenceReference) key() string {
	encoded, _ := json.Marshal(r)
	return string(encoded)
}

func inputArtifactIDs(input contracts.SolInput) []string {
	seen := make(map[string]struct{}, len(input.Evidence)+len(input.Insights))
	ids := make([]string, 0, len(input.Evidence)+len(input.Insights))
	for _, evidence := range input.Evidence {
		if evidence.TargetID == "" {
			continue
		}
		if _, exists := seen[evidence.TargetID]; !exists {
			seen[evidence.TargetID] = struct{}{}
			ids = append(ids, evidence.TargetID)
		}
	}
	for _, insight := range input.Insights {
		if insight.InsightID == "" {
			continue
		}
		if _, exists := seen[insight.InsightID]; !exists {
			seen[insight.InsightID] = struct{}{}
			ids = append(ids, insight.InsightID)
		}
	}
	sort.Strings(ids)
	return ids
}

func stringsToAny(values []string) []any {
	output := make([]any, len(values))
	for index, value := range values {
		output[index] = value
	}
	return output
}

func cloneRequest(request Request) Request {
	return Request{
		StateRevision: request.StateRevision,
		SerializedCOP: append(json.RawMessage(nil), request.SerializedCOP...),
		Insights:      cloneInsights(request.Insights),
		Evidence:      cloneEvidence(request.Evidence),
		RequestedBy:   request.RequestedBy,
	}
}

func cloneInsights(insights []gen.Insight) []gen.Insight {
	cloned := make([]gen.Insight, len(insights))
	for index, insight := range insights {
		encoded, err := json.Marshal(insight)
		if err != nil {
			cloned[index] = insight
			continue
		}
		if err := json.Unmarshal(encoded, &cloned[index]); err != nil {
			cloned[index] = insight
		}
	}
	return cloned
}

func cloneEvidence(evidence []gen.Evidence) []gen.Evidence {
	return append([]gen.Evidence(nil), evidence...)
}

func newModelRunID() string {
	bytes := make([]byte, 12)
	if _, err := rand.Read(bytes); err != nil {
		return fmt.Sprintf("sol-run-%d", time.Now().UTC().UnixNano())
	}
	return "sol-run-" + hex.EncodeToString(bytes)
}

var _ contracts.SolAdapter = (*Service)(nil)
