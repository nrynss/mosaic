package terra

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"mosaic.local/mosaic/internal/contracts"
	"mosaic.local/mosaic/internal/ontology/gen"
)

var (
	// ErrInvalidAssessment means the response did not meet the schema, input,
	// evidence, or lifecycle policy required for a durable Insight.
	ErrInvalidAssessment = errors.New("invalid Terra assessment")

	// ErrAssessmentRefused means the structured client explicitly declined the
	// assessment. The ModelRun remains durable and no Insight is written.
	ErrAssessmentRefused = errors.New("Terra assessment refused")

	// ErrAssessmentFailed means the structured client could not return a usable
	// response. The ModelRun remains durable and no Insight is written.
	ErrAssessmentFailed = errors.New("Terra assessment failed")
)

// StructuredClient is the deliberately narrow model seam. It sees a serialized
// COP, its committed revision, and explicitly permitted evidence only. It has
// no Raw Event payload, repository, shell, tool, or operational-action access.
type StructuredClient interface {
	Assess(context.Context, Request) (Response, error)
}

// EvidenceResolver confirms that the explicitly permitted evidence is
// resolvable at the committed state revision before Terra is asked to assess
// it. Implementations belong at the composition boundary; Terra never queries
// or rewrites operational storage directly.
type EvidenceResolver interface {
	ResolveEvidence(context.Context, int64, []gen.Evidence) error
}

// Request is the complete, least-privilege input that may cross the Terra
// boundary. SerializedCOP is a defensive serialization, never a mutable COP
// map shared with the caller.
type Request struct {
	StateRevision int64
	SerializedCOP json.RawMessage
	Evidence      []gen.Evidence
}

// Response is transport metadata plus a JSON representation of one Insight.
// A refusal is explicit and has no InsightJSON. The service, rather than the
// client, creates the authoritative ModelRun lifecycle record.
type Response struct {
	InsightJSON   json.RawMessage
	ResponseID    string
	RefusalDetail string
}

// Config supplies deterministic policy dependencies. No runtime model or
// network client is constructed here; the composition root injects one.
type Config struct {
	Client           StructuredClient
	EvidenceResolver EvidenceResolver
	Records          contracts.ImmutableRecordRepository
	Validator        *SchemaValidator
	PromptVersion    string
	Provider         string
	Model            string
	Clock            func() time.Time
	NewModelRunID    func() string
	ExistingInsights []gen.Insight
}

// Service implements contracts.TerraAdapter and appends assessment artifacts
// after validating them. Its lifecycle registry is deliberately separate from
// the COP so Terra cannot mutate source-derived operational facts.
type Service struct {
	client        StructuredClient
	evidence      EvidenceResolver
	records       contracts.ImmutableRecordRepository
	validator     *SchemaValidator
	promptVersion string
	provider      string
	model         string
	clock         func() time.Time
	newModelRunID func() string

	mu       sync.Mutex
	insights map[string]gen.Insight
}

// New validates wiring and optional persisted lifecycle history. Existing
// Insights are supplied by the composition root because the stable v0.1 record
// contract is append-only and intentionally has no read-query method.
func New(config Config) (*Service, error) {
	if config.Client == nil {
		return nil, errors.New("Terra structured client is required")
	}
	if config.EvidenceResolver == nil {
		return nil, errors.New("Terra evidence resolver is required")
	}
	if config.Records == nil {
		return nil, errors.New("immutable record repository is required")
	}
	if config.Validator == nil {
		return nil, errors.New("Terra schema validator is required")
	}
	if strings.TrimSpace(config.PromptVersion) == "" {
		return nil, errors.New("Terra prompt version is required")
	}
	if strings.TrimSpace(config.Provider) == "" {
		return nil, errors.New("Terra provider is required")
	}
	if strings.TrimSpace(config.Model) == "" {
		return nil, errors.New("Terra model is required")
	}
	if config.Clock == nil {
		config.Clock = time.Now
	}
	if config.NewModelRunID == nil {
		config.NewModelRunID = newModelRunID
	}

	service := &Service{
		client:        config.Client,
		evidence:      config.EvidenceResolver,
		records:       config.Records,
		validator:     config.Validator,
		promptVersion: config.PromptVersion,
		provider:      config.Provider,
		model:         config.Model,
		clock:         config.Clock,
		newModelRunID: config.NewModelRunID,
		insights:      make(map[string]gen.Insight, len(config.ExistingInsights)),
	}
	for _, insight := range config.ExistingInsights {
		if err := service.validator.ValidateInsight(insight); err != nil {
			return nil, fmt.Errorf("validate existing Insight %q schema: %w", insight.InsightID, err)
		}
		if err := service.validateInsightLifecycle(insight, service.insights); err != nil {
			return nil, fmt.Errorf("validate existing Insight %q: %w", insight.InsightID, err)
		}
		service.rememberInsight(insight)
	}
	return service, nil
}

// Assess creates a bounded derived assessment from committed COP state. Every
// client call creates one ModelRun. A refusal, client failure, invalid JSON,
// invalid schema, evidence/revision mismatch, or invalid lifecycle creates no
// Insight and cannot alter the COP.
func (s *Service) Assess(ctx context.Context, input contracts.TerraInput) (contracts.TerraOutput, error) {
	request, err := s.prepareRequest(input)
	if err != nil {
		return contracts.TerraOutput{}, fmt.Errorf("prepare Terra request: %w", err)
	}

	started := s.clock().UTC()
	if err := s.evidence.ResolveEvidence(ctx, input.StateRevision, request.Evidence); err != nil {
		completed := s.clock().UTC()
		return s.reject(ctx, input, started, completed, "", fmt.Errorf("resolve permitted evidence: %w", err))
	}
	response, clientErr := s.client.Assess(ctx, request)
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
		return output, fmt.Errorf("%w: %v", ErrAssessmentFailed, clientErr)
	}
	if strings.TrimSpace(response.RefusalDetail) != "" {
		output, persistErr := s.persistFailure(ctx, input, started, completed, "refused", response.RefusalDetail, response.ResponseID)
		if persistErr != nil {
			return output, persistErr
		}
		return output, fmt.Errorf("%w: %s", ErrAssessmentRefused, response.RefusalDetail)
	}

	var insight gen.Insight
	if err := json.Unmarshal(response.InsightJSON, &insight); err != nil {
		return s.reject(ctx, input, started, completed, response.ResponseID, fmt.Errorf("decode Insight JSON: %w", err))
	}
	if err := s.validateCandidate(input, insight); err != nil {
		return s.reject(ctx, input, started, completed, response.ResponseID, err)
	}

	run := s.modelRun(input, started, completed, "valid", "", response.ResponseID, []string{insight.InsightID})
	if err := s.validator.ValidateModelRun(run); err != nil {
		return contracts.TerraOutput{}, fmt.Errorf("validate generated Terra model run: %w", err)
	}
	if err := s.records.AppendModelRun(ctx, run); err != nil {
		return contracts.TerraOutput{ModelRun: run}, fmt.Errorf("persist Terra model run: %w", err)
	}
	if err := s.records.AppendInsight(ctx, insight); err != nil {
		return contracts.TerraOutput{Insight: insight, ModelRun: run}, fmt.Errorf("persist Terra Insight: %w", err)
	}

	s.mu.Lock()
	s.rememberInsight(insight)
	s.mu.Unlock()
	return contracts.TerraOutput{Insight: insight, ModelRun: run}, nil
}

func (s *Service) prepareRequest(input contracts.TerraInput) (Request, error) {
	if input.StateRevision < 1 {
		return Request{}, errors.New("state revision must be positive")
	}
	if input.COP == nil {
		return Request{}, errors.New("COP is required")
	}
	if len(input.Evidence) == 0 {
		return Request{}, errors.New("at least one permitted evidence reference is required")
	}
	serializedCOP, err := json.Marshal(input.COP)
	if err != nil {
		return Request{}, fmt.Errorf("serialize COP: %w", err)
	}
	evidence := make([]gen.Evidence, len(input.Evidence))
	for index, item := range input.Evidence {
		if err := s.validator.ValidateEvidence(item); err != nil {
			return Request{}, fmt.Errorf("validate permitted evidence %q: %w", item.EvidenceID, err)
		}
		evidence[index] = item
	}
	return Request{
		StateRevision: input.StateRevision,
		SerializedCOP: append(json.RawMessage(nil), serializedCOP...),
		Evidence:      evidence,
	}, nil
}

func (s *Service) reject(ctx context.Context, input contracts.TerraInput, started, completed time.Time, responseID string, cause error) (contracts.TerraOutput, error) {
	output, persistErr := s.persistFailure(ctx, input, started, completed, "invalid", cause.Error(), responseID)
	if persistErr != nil {
		return output, persistErr
	}
	return output, fmt.Errorf("%w: %v", ErrInvalidAssessment, cause)
}

func (s *Service) persistFailure(
	ctx context.Context,
	input contracts.TerraInput,
	started, completed time.Time,
	status, detail, responseID string,
) (contracts.TerraOutput, error) {
	run := s.modelRun(input, started, completed, status, detail, responseID, nil)
	if err := s.validator.ValidateModelRun(run); err != nil {
		return contracts.TerraOutput{ModelRun: run}, fmt.Errorf("validate generated Terra failure model run: %w", err)
	}
	if err := s.records.AppendModelRun(ctx, run); err != nil {
		return contracts.TerraOutput{ModelRun: run}, fmt.Errorf("persist Terra failure model run: %w", err)
	}
	return contracts.TerraOutput{ModelRun: run}, nil
}

func (s *Service) modelRun(
	input contracts.TerraInput,
	started, completed time.Time,
	status, detail, responseID string,
	outputIDs []string,
) gen.ModelRun {
	inputIDs := evidenceTargetIDs(input.Evidence)
	outputs := make([]any, len(outputIDs))
	for index, id := range outputIDs {
		outputs[index] = id
	}
	return gen.ModelRun{
		SchemaVersion:       "1.0.0",
		ModelRunID:          s.newModelRunID(),
		Agent:               "terra",
		Provider:            s.provider,
		Model:               s.model,
		PromptVersion:       s.promptVersion,
		OutputSchemaVersion: "1.0.0",
		InputEventIds:       stringsToAny(inputIDs),
		StateRevision:       input.StateRevision,
		OutputIds:           outputs,
		ValidationStatus:    status,
		ResponseID:          responseID,
		FailureDetail:       detail,
		StartedAt:           started.Format(time.RFC3339Nano),
		CompletedAt:         completed.Format(time.RFC3339Nano),
	}
}

func (s *Service) validateCandidate(input contracts.TerraInput, insight gen.Insight) error {
	if err := s.validator.ValidateInsight(insight); err != nil {
		return err
	}
	if insight.StateRevision != input.StateRevision {
		return fmt.Errorf("Insight state revision %d does not match requested revision %d", insight.StateRevision, input.StateRevision)
	}
	if !sameEvidence(insight.Evidence, input.Evidence) {
		return errors.New("Insight evidence does not exactly match permitted evidence")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.validateInsightLifecycle(insight, s.insights)
}

func (s *Service) validateInsightLifecycle(insight gen.Insight, known map[string]gen.Insight) error {
	if strings.TrimSpace(insight.InsightID) == "" {
		return errors.New("Insight ID is required")
	}
	if _, exists := known[insight.InsightID]; exists {
		return fmt.Errorf("Insight %q already exists", insight.InsightID)
	}
	switch insight.LifecycleStatus {
	case "active":
		if insight.SupersedesInsightID != "" || insight.ObsoleteReason != "" {
			return errors.New("active Insight cannot declare obsolescence")
		}
	case "obsolete":
		if insight.SupersedesInsightID == "" || insight.ObsoleteReason == "" {
			return errors.New("obsolete Insight requires supersedes_insight_id and obsolete_reason")
		}
		prior, exists := known[insight.SupersedesInsightID]
		if !exists {
			return fmt.Errorf("obsolete Insight references unknown prior Insight %q", insight.SupersedesInsightID)
		}
		if prior.LifecycleStatus != "active" {
			return fmt.Errorf("obsolete Insight references non-active Insight %q", insight.SupersedesInsightID)
		}
	default:
		return fmt.Errorf("unsupported Insight lifecycle status %q", insight.LifecycleStatus)
	}
	return nil
}

// rememberInsight updates only the in-memory lifecycle index after the
// immutable record has been appended. Marking the prior index entry obsolete
// does not rewrite its persisted Insight; it prevents a second notice from
// claiming to obsolete the same active assessment.
func (s *Service) rememberInsight(insight gen.Insight) {
	if insight.LifecycleStatus == "obsolete" {
		prior := s.insights[insight.SupersedesInsightID]
		prior.LifecycleStatus = "obsolete"
		s.insights[insight.SupersedesInsightID] = prior
	}
	s.insights[insight.InsightID] = cloneInsight(insight)
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
		if err := json.Unmarshal(encoded, &reference); err != nil {
			return false
		}
		if !reference.valid() {
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

func evidenceTargetIDs(evidence []gen.Evidence) []string {
	seen := make(map[string]struct{}, len(evidence))
	ids := make([]string, 0, len(evidence))
	for _, item := range evidence {
		if _, exists := seen[item.TargetID]; exists {
			continue
		}
		seen[item.TargetID] = struct{}{}
		ids = append(ids, item.TargetID)
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

func cloneInsight(insight gen.Insight) gen.Insight {
	encoded, err := json.Marshal(insight)
	if err != nil {
		return insight
	}
	var clone gen.Insight
	if err := json.Unmarshal(encoded, &clone); err != nil {
		return insight
	}
	return clone
}

func newModelRunID() string {
	bytes := make([]byte, 12)
	if _, err := rand.Read(bytes); err != nil {
		return fmt.Sprintf("terra-run-%d", time.Now().UTC().UnixNano())
	}
	return "terra-run-" + hex.EncodeToString(bytes)
}

var _ contracts.TerraAdapter = (*Service)(nil)
