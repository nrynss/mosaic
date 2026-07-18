// Package ingestion implements the durable Raw Event to Canonical Event
// boundary. It deliberately does not mutate the COP; that happens only after a
// committed Canonical Event is dispatched to the deterministic projector.
package ingestion

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"mosaic.local/mosaic/internal/contracts"
	"mosaic.local/mosaic/internal/luna"
	"mosaic.local/mosaic/internal/ontology/gen"
)

var (
	// ErrInvalidLunaOutput means an adapter response failed the structural,
	// schema, or provenance checks required before canonical append.
	ErrInvalidLunaOutput = errors.New("invalid Luna output")

	// ErrLunaNormalization means the adapter did not complete normalization.
	// The Raw Event and a rejected lifecycle record remain durable.
	ErrLunaNormalization = errors.New("Luna normalization failed")
)

// Outcome is the durable ingestion lifecycle returned to an API or simulator.
// DispatchError is deliberately separate from error: the canonical history is
// already committed when dispatch is attempted, so a caller must not mistake a
// scheduling failure for a rolled-back ingestion.
type Outcome struct {
	RawEventID       string
	LunaResultID     string
	CanonicalEventID string
	Status           string
	Duplicate        bool
	DispatchError    error
}

// Config supplies already-constructed contract implementations. The
// composition root owns concrete store and adapter selection.
type Config struct {
	RawEvents       contracts.RawEventRepository
	CanonicalEvents contracts.CanonicalEventRepository
	Records         contracts.ImmutableRecordRepository
	Transactions    contracts.TransactionRunner
	Luna            contracts.LunaAdapter
	Dispatcher      contracts.ProjectorDispatcher
	Validator       *luna.SchemaValidator
	Clock           func() time.Time
}

// Service persists Raw Events before normalizing them and appends a projectable
// canonical lifecycle only after all relationships have been checked.
type Service struct {
	rawEvents       contracts.RawEventRepository
	canonicalEvents contracts.CanonicalEventRepository
	records         contracts.ImmutableRecordRepository
	transactions    contracts.TransactionRunner
	luna            contracts.LunaAdapter
	dispatcher      contracts.ProjectorDispatcher
	validator       *luna.SchemaValidator
	clock           func() time.Time
}

// New rejects incomplete wiring instead of allowing a partial ingestion path to
// silently lose lifecycle history.
func New(config Config) (*Service, error) {
	if config.RawEvents == nil {
		return nil, errors.New("raw event repository is required")
	}
	if config.CanonicalEvents == nil {
		return nil, errors.New("canonical event repository is required")
	}
	if config.Records == nil {
		return nil, errors.New("immutable record repository is required")
	}
	if config.Transactions == nil {
		return nil, errors.New("transaction runner is required")
	}
	if config.Luna == nil {
		return nil, errors.New("Luna adapter is required")
	}
	if config.Dispatcher == nil {
		return nil, errors.New("projector dispatcher is required")
	}
	if config.Validator == nil {
		return nil, errors.New("Luna schema validator is required")
	}
	if config.Clock == nil {
		config.Clock = time.Now
	}
	return &Service{
		rawEvents:       config.RawEvents,
		canonicalEvents: config.CanonicalEvents,
		records:         config.Records,
		transactions:    config.Transactions,
		luna:            config.Luna,
		dispatcher:      config.Dispatcher,
		validator:       config.Validator,
		clock:           config.Clock,
	}, nil
}

// Ingest records the Raw Event before validating it or calling Luna. Repeated
// source delivery returns the first lifecycle identity and never calls Luna,
// appends another Canonical Event, or redispatches the event.
func (s *Service) Ingest(ctx context.Context, raw gen.RawEvent) (Outcome, error) {
	appendResult, err := s.rawEvents.AppendRawEvent(ctx, raw)
	if err != nil {
		return Outcome{}, fmt.Errorf("persist raw event: %w", err)
	}

	outcome := Outcome{RawEventID: appendResult.RawEventID}
	if !appendResult.Created {
		return Outcome{
			RawEventID:   appendResult.RawEventID,
			LunaResultID: appendResult.ExistingResultID,
			Status:       "duplicate",
			Duplicate:    true,
		}, nil
	}

	// A source payload is opaque and may be malformed. Only the envelope is
	// checked here, after it is durable, so its bytes can always be inspected.
	if err := s.validator.ValidateRawEvent(raw); err != nil {
		return s.persistFallback(ctx, raw, outcome, "invalid", fmt.Errorf("raw envelope: %w", err))
	}

	output, normalizeErr := s.luna.Normalize(ctx, raw)
	if normalizeErr != nil {
		return s.persistAdapterFailure(ctx, raw, outcome, output, normalizeErr)
	}
	return s.persistOutput(ctx, raw, outcome, output)
}

func (s *Service) persistAdapterFailure(
	ctx context.Context,
	raw gen.RawEvent,
	outcome Outcome,
	output contracts.LunaOutput,
	normalizeErr error,
) (Outcome, error) {
	// Some adapters can return their completed failure/refusal artifacts with an
	// error. Preserve them when they are internally valid and non-projectable.
	if err := s.validateNonProjectable(raw, output); err == nil {
		persisted, persistErr := s.persistNonProjectable(ctx, outcome, output)
		if persistErr != nil {
			return persisted, persistErr
		}
		return persisted, fmt.Errorf("%w: %v", ErrLunaNormalization, normalizeErr)
	}
	return s.persistFallback(ctx, raw, outcome, "failed", normalizeErr)
}

func (s *Service) persistOutput(ctx context.Context, raw gen.RawEvent, outcome Outcome, output contracts.LunaOutput) (Outcome, error) {
	if err := s.validator.ValidateModelRun(output.ModelRun); err != nil {
		return s.persistFallback(ctx, raw, outcome, "invalid", fmt.Errorf("model run: %w", err))
	}
	if err := s.validator.ValidateLunaResult(output.Result); err != nil {
		return s.persistFallback(ctx, raw, outcome, "invalid", fmt.Errorf("Luna result: %w", err))
	}

	switch output.Result.Status {
	case "accepted", "repaired":
		if err := s.validateProjectable(ctx, raw, output); err != nil {
			return s.persistFallback(ctx, raw, outcome, "invalid", err)
		}
		return s.persistProjectable(ctx, outcome, output)
	case "quarantined", "rejected":
		if err := s.validateNonProjectable(raw, output); err != nil {
			return s.persistFallback(ctx, raw, outcome, "invalid", err)
		}
		return s.persistNonProjectable(ctx, outcome, output)
	default:
		return s.persistFallback(ctx, raw, outcome, "invalid", fmt.Errorf("%w: unsupported result status %q", ErrInvalidLunaOutput, output.Result.Status))
	}
}

func (s *Service) persistProjectable(ctx context.Context, outcome Outcome, output contracts.LunaOutput) (Outcome, error) {
	if err := s.records.AppendModelRun(ctx, output.ModelRun); err != nil {
		return outcome, fmt.Errorf("persist Luna model run: %w", err)
	}

	var appended gen.CanonicalEvent
	if err := s.transactions.WithinTransaction(ctx, func(txCtx context.Context) error {
		var err error
		appended, err = s.canonicalEvents.AppendCanonicalEvent(txCtx, *output.CanonicalEvent)
		if err != nil {
			return fmt.Errorf("append canonical event: %w", err)
		}
		if err := s.validator.ValidateCanonicalEvent(appended); err != nil {
			return fmt.Errorf("validate appended canonical event: %w", err)
		}
		if err := s.records.AppendLunaResult(txCtx, output.Result); err != nil {
			return fmt.Errorf("append Luna result: %w", err)
		}
		return nil
	}); err != nil {
		return outcome, fmt.Errorf("persist canonical lifecycle atomically: %w", err)
	}

	outcome.LunaResultID = output.Result.LunaResultID
	outcome.CanonicalEventID = appended.CanonicalEventID
	outcome.Status = output.Result.Status
	// Dispatch is intentionally after commit. The durable canonical history is
	// retained for recovery if the in-process dispatcher is unavailable.
	outcome.DispatchError = s.dispatcher.DispatchCanonicalEvent(ctx, appended.CanonicalEventID)
	return outcome, nil
}

func (s *Service) persistNonProjectable(ctx context.Context, outcome Outcome, output contracts.LunaOutput) (Outcome, error) {
	if err := s.records.AppendModelRun(ctx, output.ModelRun); err != nil {
		return outcome, fmt.Errorf("persist Luna model run: %w", err)
	}
	if err := s.transactions.WithinTransaction(ctx, func(txCtx context.Context) error {
		return s.records.AppendLunaResult(txCtx, output.Result)
	}); err != nil {
		return outcome, fmt.Errorf("persist non-projectable Luna lifecycle: %w", err)
	}
	outcome.LunaResultID = output.Result.LunaResultID
	outcome.Status = output.Result.Status
	return outcome, nil
}

func (s *Service) persistFallback(ctx context.Context, raw gen.RawEvent, outcome Outcome, validationStatus string, cause error) (Outcome, error) {
	detail := cause.Error()
	fallback := luna.NewFailureArtifacts(raw.RawEventID, detail, validationStatus, s.clock())
	if err := s.validator.ValidateModelRun(fallback.ModelRun); err != nil {
		return outcome, fmt.Errorf("validate generated Luna failure model run: %w", err)
	}
	if err := s.validator.ValidateLunaResult(fallback.Result); err != nil {
		return outcome, fmt.Errorf("validate generated Luna failure result: %w", err)
	}
	persisted, err := s.persistNonProjectable(ctx, outcome, contracts.LunaOutput{
		Result:   fallback.Result,
		ModelRun: fallback.ModelRun,
	})
	if err != nil {
		return persisted, err
	}
	return persisted, fmt.Errorf("%w: %v", ErrInvalidLunaOutput, cause)
}

func (s *Service) validateProjectable(ctx context.Context, raw gen.RawEvent, output contracts.LunaOutput) error {
	if output.CanonicalEvent == nil {
		return fmt.Errorf("%w: %s result has no canonical event", ErrInvalidLunaOutput, output.Result.Status)
	}
	if output.ModelRun.Agent != "luna" || output.ModelRun.ValidationStatus != "valid" {
		return fmt.Errorf("%w: projectable result requires a valid Luna model run", ErrInvalidLunaOutput)
	}
	if err := s.validateSharedRelationships(raw, output); err != nil {
		return err
	}
	canonical := output.CanonicalEvent
	if canonical.RawEventID != raw.RawEventID {
		return fmt.Errorf("%w: canonical raw_event_id does not match the persisted envelope", ErrInvalidLunaOutput)
	}
	if output.Result.CanonicalEventID != canonical.CanonicalEventID {
		return fmt.Errorf("%w: Luna result canonical_event_id does not match canonical event", ErrInvalidLunaOutput)
	}
	if canonical.DuplicateOf == canonical.CanonicalEventID && canonical.DuplicateOf != "" {
		return fmt.Errorf("%w: semantic duplicate cannot point to itself", ErrInvalidLunaOutput)
	}
	if canonical.DuplicateOf != "" {
		if err := s.validateDuplicateTarget(ctx, canonical.DuplicateOf); err != nil {
			return err
		}
	}
	if err := validateCanonicalProvenance(*canonical, raw.RawEventID, output.ModelRun.ModelRunID); err != nil {
		return err
	}
	// CanonicalSeq is database-owned. Validate a provisional positive sequence
	// before append, then validate the database-assigned sequence in the same
	// transaction before the Luna lifecycle record is written.
	candidate := *canonical
	if candidate.CanonicalSeq == 0 {
		candidate.CanonicalSeq = 1
	}
	if err := s.validator.ValidateCanonicalEvent(candidate); err != nil {
		return fmt.Errorf("%w: canonical event: %v", ErrInvalidLunaOutput, err)
	}
	return nil
}

func (s *Service) validateDuplicateTarget(ctx context.Context, targetID string) error {
	events, err := s.canonicalEvents.ListCanonicalEventsAfter(ctx, 0)
	if err != nil {
		return fmt.Errorf("%w: resolve semantic duplicate target: %v", ErrInvalidLunaOutput, err)
	}
	for _, event := range events {
		if event.CanonicalEventID == targetID {
			return nil
		}
	}
	return fmt.Errorf("%w: semantic duplicate target %q is not a persisted canonical event", ErrInvalidLunaOutput, targetID)
}

func (s *Service) validateNonProjectable(raw gen.RawEvent, output contracts.LunaOutput) error {
	if output.Result.Status != "quarantined" && output.Result.Status != "rejected" {
		return fmt.Errorf("%w: failed adapter must return quarantined or rejected lifecycle", ErrInvalidLunaOutput)
	}
	if output.CanonicalEvent != nil || output.Result.CanonicalEventID != "" {
		return fmt.Errorf("%w: non-projectable lifecycle cannot name a canonical event", ErrInvalidLunaOutput)
	}
	if err := s.validator.ValidateModelRun(output.ModelRun); err != nil {
		return fmt.Errorf("%w: model run: %v", ErrInvalidLunaOutput, err)
	}
	if err := s.validator.ValidateLunaResult(output.Result); err != nil {
		return fmt.Errorf("%w: Luna result: %v", ErrInvalidLunaOutput, err)
	}
	return s.validateSharedRelationships(raw, output)
}

func (s *Service) validateSharedRelationships(raw gen.RawEvent, output contracts.LunaOutput) error {
	if output.ModelRun.Agent != "luna" {
		return fmt.Errorf("%w: model run agent must be luna", ErrInvalidLunaOutput)
	}
	if output.Result.RawEventID != raw.RawEventID {
		return fmt.Errorf("%w: Luna result raw_event_id does not match the persisted envelope", ErrInvalidLunaOutput)
	}
	if !containsID(output.ModelRun.InputEventIds, raw.RawEventID) {
		return fmt.Errorf("%w: Luna model run does not cite the raw event input", ErrInvalidLunaOutput)
	}
	if !containsID(output.ModelRun.OutputIds, output.Result.LunaResultID) {
		return fmt.Errorf("%w: Luna model run does not cite the Luna result output", ErrInvalidLunaOutput)
	}
	return nil
}

func validateCanonicalProvenance(event gen.CanonicalEvent, rawEventID, modelRunID string) error {
	var provenance map[string]any
	if err := json.Unmarshal(event.Provenance, &provenance); err != nil {
		return fmt.Errorf("%w: decode canonical provenance: %v", ErrInvalidLunaOutput, err)
	}
	if recordedRawID, _ := provenance["raw_event_id"].(string); recordedRawID != rawEventID {
		return fmt.Errorf("%w: canonical provenance raw_event_id does not match the envelope", ErrInvalidLunaOutput)
	}
	if recordedRunID, _ := provenance["model_run_id"].(string); recordedRunID != modelRunID {
		return fmt.Errorf("%w: canonical provenance model_run_id does not match Luna model run", ErrInvalidLunaOutput)
	}
	return nil
}

func containsID(values []any, want string) bool {
	for _, value := range values {
		if candidate, ok := value.(string); ok && candidate == want {
			return true
		}
	}
	return false
}
