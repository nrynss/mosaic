// Package state implements Mosaic's deterministic, source-derived COP
// projection. It deliberately has no clock, network, model, or random input.
package state

import (
	"context"
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

const schemaVersion = "1.0.0"

var (
	// ErrUnsupportedEventType is a deliberate, deterministic outcome for a
	// record outside the v0.1 canonical vocabulary. The event remains durable,
	// but does not create a partial projection or a checkpoint.
	ErrUnsupportedEventType = errors.New("unsupported canonical event type")

	// ErrInvalidSequence means the supplied durable log cannot establish one
	// unambiguous projection order.
	ErrInvalidSequence = errors.New("invalid canonical event sequence")

	// ErrProjectionOrder prevents a later canonical record from being marked as
	// projected while an earlier durable record has no atomic receipt and
	// checkpoint. The dispatcher must process the append log in sequence.
	ErrProjectionOrder = errors.New("canonical event is outside projection order")

	// ErrCheckpointUnavailable distinguishes a store failure from an empty
	// store. P03 currently exposes this distinction as an error rather than a
	// sentinel in the cross-package contract.
	ErrCheckpointUnavailable = errors.New("checkpoint unavailable")
)

// Projector implements contracts.Projector and owns its in-memory read model.
// The durable canonical log and checkpoints remain the source of recovery.
type Projector struct {
	canonical    contracts.CanonicalEventRepository
	checkpoints  contracts.CheckpointRepository
	transactions contracts.TransactionRunner

	mu      sync.Mutex
	current *contracts.ProjectionResult
}

var _ contracts.Projector = (*Projector)(nil)

// NewProjector wires the P03 persistence seams required for atomic projection
// receipts and checkpoints. All arguments must refer to the same durable store.
func NewProjector(
	canonical contracts.CanonicalEventRepository,
	checkpoints contracts.CheckpointRepository,
	transactions contracts.TransactionRunner,
) (*Projector, error) {
	if canonical == nil || checkpoints == nil || transactions == nil {
		return nil, errors.New("canonical repository, checkpoint repository, and transaction runner are required")
	}
	return &Projector{
		canonical:    canonical,
		checkpoints:  checkpoints,
		transactions: transactions,
	}, nil
}

// ApplyCanonicalEvent projects one already-committed event. It reconstructs the
// state from the durable log through the event's database-assigned sequence, so
// corrections are evaluated from evidence rather than from mutable source data.
// The checkpoint, projection receipt, and result are written in one transaction.
func (p *Projector) ApplyCanonicalEvent(ctx context.Context, event gen.CanonicalEvent) (contracts.ProjectionResult, error) {
	if strings.TrimSpace(event.CanonicalEventID) == "" {
		return contracts.ProjectionResult{}, errors.New("canonical event ID is required")
	}

	p.mu.Lock()
	defer p.mu.Unlock()

	var result contracts.ProjectionResult
	err := p.transactions.WithinTransaction(ctx, func(txCtx context.Context) error {
		checkpoint, found, err := latestCheckpoint(txCtx, p.checkpoints)
		if err != nil {
			return err
		}
		if found && checkpoint.ThroughCanonicalSeq >= event.CanonicalSeq && event.CanonicalSeq > 0 {
			result, err = resultFromCheckpoint(checkpoint)
			return err
		}

		events, err := p.canonical.ListCanonicalEventsAfter(txCtx, 0)
		if err != nil {
			return fmt.Errorf("list committed canonical events: %w", err)
		}
		persisted, ok := findEvent(events, event.CanonicalEventID)
		if !ok {
			return fmt.Errorf("canonical event %q is not committed", event.CanonicalEventID)
		}
		if persisted.CanonicalSeq < 1 {
			return fmt.Errorf("%w: canonical event %q has no persisted sequence", ErrInvalidSequence, persisted.CanonicalEventID)
		}
		expectedSequence := int64(1)
		if found {
			expectedSequence = checkpoint.ThroughCanonicalSeq + 1
		}
		if persisted.CanonicalSeq != expectedSequence {
			return fmt.Errorf("%w: event %q is sequence %d, want %d", ErrProjectionOrder, persisted.CanonicalEventID, persisted.CanonicalSeq, expectedSequence)
		}
		prefix := throughSequence(events, persisted.CanonicalSeq)
		result, err = projectFromScratch(prefix)
		if err != nil {
			return err
		}
		checkpoint = result.Checkpoint
		if err := p.canonical.MarkCanonicalEventProjected(txCtx, persisted.CanonicalEventID, result.StateRevision); err != nil {
			return fmt.Errorf("record projection receipt: %w", err)
		}
		if err := p.checkpoints.AppendCheckpoint(txCtx, checkpoint); err != nil {
			return fmt.Errorf("append projection checkpoint: %w", err)
		}
		return nil
	})
	if err != nil {
		return contracts.ProjectionResult{}, err
	}
	cloned := cloneResult(result)
	p.current = &cloned
	return cloneResult(result), nil
}

// Replay rebuilds the COP from a supplied checkpoint plus later committed
// canonical events. It never writes a receipt or checkpoint; recovery is a
// pure function of durable artifacts.
func (p *Projector) Replay(_ context.Context, checkpoint gen.Checkpoint, events []gen.CanonicalEvent) (contracts.ProjectionResult, error) {
	p.mu.Lock()
	defer p.mu.Unlock()

	result, err := replay(checkpoint, events)
	if err != nil {
		return contracts.ProjectionResult{}, err
	}
	cloned := cloneResult(result)
	p.current = &cloned
	return cloneResult(result), nil
}

func latestCheckpoint(ctx context.Context, repository contracts.CheckpointRepository) (gen.Checkpoint, bool, error) {
	checkpoint, err := repository.LatestCheckpoint(ctx)
	if err == nil {
		return checkpoint, true, nil
	}
	// P03's contract intentionally has no exported not-found sentinel. Its
	// implementation's stable message is the only portable v0.1 signal for the
	// expected pre-first-projection state; all other failures remain failures.
	message := strings.ToLower(err.Error())
	if strings.Contains(message, "not found") && strings.Contains(message, "checkpoint") {
		return gen.Checkpoint{}, false, nil
	}
	return gen.Checkpoint{}, false, fmt.Errorf("%w: %v", ErrCheckpointUnavailable, err)
}

func findEvent(events []gen.CanonicalEvent, id string) (gen.CanonicalEvent, bool) {
	for _, event := range events {
		if event.CanonicalEventID == id {
			return event, true
		}
	}
	return gen.CanonicalEvent{}, false
}

func throughSequence(events []gen.CanonicalEvent, sequence int64) []gen.CanonicalEvent {
	prefix := make([]gen.CanonicalEvent, 0, len(events))
	for _, event := range events {
		if event.CanonicalSeq <= sequence {
			prefix = append(prefix, event)
		}
	}
	return prefix
}

func projectFromScratch(events []gen.CanonicalEvent) (contracts.ProjectionResult, error) {
	ordered, err := orderEvents(events, 0)
	if err != nil {
		return contracts.ProjectionResult{}, err
	}
	if len(ordered) == 0 {
		return contracts.ProjectionResult{}, errors.New("cannot create a checkpoint without canonical events")
	}

	state := newCOP()
	for _, event := range effectiveEvents(ordered) {
		if err := state.apply(event); err != nil {
			return contracts.ProjectionResult{}, err
		}
	}
	last := ordered[len(ordered)-1]
	state.StateRevision = last.CanonicalSeq
	state.ProjectedAt = last.ReceivedAt
	return resultForState(state, last)
}

func replay(checkpoint gen.Checkpoint, events []gen.CanonicalEvent) (contracts.ProjectionResult, error) {
	if checkpoint.StateRevision == 0 && checkpoint.ThroughCanonicalSeq == 0 && len(checkpoint.COP) == 0 {
		return projectFromScratch(events)
	}
	state, err := copFromCheckpoint(checkpoint)
	if err != nil {
		return contracts.ProjectionResult{}, err
	}
	ordered, err := orderEvents(events, checkpoint.ThroughCanonicalSeq)
	if err != nil {
		return contracts.ProjectionResult{}, err
	}
	if len(ordered) == 0 {
		return resultFromCheckpoint(checkpoint)
	}

	for _, event := range ordered {
		if err := state.apply(event); err != nil {
			return contracts.ProjectionResult{}, err
		}
		state.StateRevision = event.CanonicalSeq
		state.ProjectedAt = event.ReceivedAt
	}
	return resultForState(state, ordered[len(ordered)-1])
}

func orderEvents(events []gen.CanonicalEvent, after int64) ([]gen.CanonicalEvent, error) {
	ordered := append([]gen.CanonicalEvent(nil), events...)
	sort.SliceStable(ordered, func(i, j int) bool {
		return ordered[i].CanonicalSeq < ordered[j].CanonicalSeq
	})
	previous := after
	for _, event := range ordered {
		if event.CanonicalSeq <= after {
			continue
		}
		if event.CanonicalSeq <= previous {
			return nil, fmt.Errorf("%w: sequence %d is duplicate or out of range", ErrInvalidSequence, event.CanonicalSeq)
		}
		if strings.TrimSpace(event.CanonicalEventID) == "" || strings.TrimSpace(event.ReceivedAt) == "" {
			return nil, fmt.Errorf("%w: event identity and received_at are required", ErrInvalidSequence)
		}
		previous = event.CanonicalSeq
	}
	filtered := ordered[:0]
	for _, event := range ordered {
		if event.CanonicalSeq > after {
			filtered = append(filtered, event)
		}
	}
	return filtered, nil
}

// effectiveEvents removes superseded branches while keeping the rest of the
// append log. The highest-sequence direct correction wins for a predecessor,
// and that decision is applied recursively through correction chains.
func effectiveEvents(events []gen.CanonicalEvent) []gen.CanonicalEvent {
	byID := make(map[string]gen.CanonicalEvent, len(events))
	children := make(map[string][]gen.CanonicalEvent)
	for _, event := range events {
		byID[event.CanonicalEventID] = event
		if event.SupersedesEventID != "" {
			children[event.SupersedesEventID] = append(children[event.SupersedesEventID], event)
		}
	}
	preferred := make(map[string]string, len(children))
	for parentID, direct := range children {
		sort.Slice(direct, func(i, j int) bool { return direct[i].CanonicalSeq < direct[j].CanonicalSeq })
		preferred[parentID] = direct[len(direct)-1].CanonicalEventID
	}

	effective := make([]gen.CanonicalEvent, 0, len(events))
	for _, event := range events {
		if len(children[event.CanonicalEventID]) != 0 || !preferredAncestry(event, byID, preferred) {
			continue
		}
		effective = append(effective, event)
	}
	sort.Slice(effective, func(i, j int) bool { return effective[i].CanonicalSeq < effective[j].CanonicalSeq })
	return effective
}

func preferredAncestry(event gen.CanonicalEvent, byID map[string]gen.CanonicalEvent, preferred map[string]string) bool {
	current := event
	seen := make(map[string]struct{})
	for current.SupersedesEventID != "" {
		if _, exists := seen[current.CanonicalEventID]; exists {
			return false
		}
		seen[current.CanonicalEventID] = struct{}{}
		if preferred[current.SupersedesEventID] != current.CanonicalEventID {
			return false
		}
		parent, exists := byID[current.SupersedesEventID]
		if !exists {
			return false
		}
		current = parent
	}
	return true
}

type cop struct {
	StateRevision     int64          `json:"state_revision"`
	ProjectedAt       string         `json:"projected_at"`
	EffectiveEventIDs []string       `json:"effective_event_ids"`
	Incidents         []incidentFact `json:"incidents"`
	Units             []unitFact     `json:"units"`
	Resources         []resourceFact `json:"resources"`
	Roads             []roadFact     `json:"roads"`
	WeatherAlerts     []weatherFact  `json:"weather_alerts"`
	ActiveInsightIDs  []string       `json:"active_insight_ids"`
}

type incidentFact struct {
	ClaimClass     string      `json:"claim_class"`
	IncidentID     string      `json:"incident_id"`
	Status         string      `json:"status"`
	Category       string      `json:"category"`
	LocationID     string      `json:"location_id"`
	OpenedAt       string      `json:"opened_at"`
	ResolvedAt     string      `json:"resolved_at,omitempty"`
	LinkedEntities []entityRef `json:"linked_entity_refs,omitempty"`
	SourceEventIDs []string    `json:"source_event_ids"`
}

type entityRef struct {
	Kind string `json:"kind"`
	ID   string `json:"id"`
}

type unitFact struct {
	ClaimClass    string `json:"claim_class"`
	UnitID        string `json:"unit_id"`
	Availability  string `json:"availability"`
	IncidentID    string `json:"incident_id,omitempty"`
	UpdatedAt     string `json:"updated_at"`
	SourceEventID string `json:"source_event_id"`
}

type resourceFact struct {
	ClaimClass    string `json:"claim_class"`
	ResourceID    string `json:"resource_id"`
	ResourceType  string `json:"resource_type"`
	Availability  string `json:"availability"`
	IncidentID    string `json:"incident_id,omitempty"`
	UpdatedAt     string `json:"updated_at"`
	SourceEventID string `json:"source_event_id"`
}

type roadFact struct {
	ClaimClass       string `json:"claim_class"`
	RoadID           string `json:"road_id"`
	Name             string `json:"name"`
	Status           string `json:"status"`
	EffectiveEventID string `json:"effective_event_id"`
	UpdatedAt        string `json:"updated_at"`
}

type weatherFact struct {
	ClaimClass     string `json:"claim_class"`
	WeatherAlertID string `json:"weather_alert_id"`
	Status         string `json:"status"`
	Severity       string `json:"severity,omitempty"`
	Summary        string `json:"summary"`
	UpdatedAt      string `json:"updated_at"`
	SourceEventID  string `json:"source_event_id"`
}

func newCOP() cop {
	return cop{
		EffectiveEventIDs: []string{},
		Incidents:         []incidentFact{},
		Units:             []unitFact{},
		Resources:         []resourceFact{},
		Roads:             []roadFact{},
		WeatherAlerts:     []weatherFact{},
		ActiveInsightIDs:  []string{},
	}
}

func (c *cop) apply(event gen.CanonicalEvent) error {
	if err := requireEventType(event.EventType); err != nil {
		return err
	}
	if event.SupersedesEventID != "" {
		c.removeEffectiveEvent(event.SupersedesEventID)
	}
	switch event.EventType {
	case "incident_reported":
		return c.applyIncidentReported(event)
	case "incident_resolved":
		return c.applyIncidentResolved(event)
	case "unit_status_changed":
		return c.applyUnit(event)
	case "resource_status_changed":
		return c.applyResource(event)
	case "road_status_changed":
		return c.applyRoad(event)
	case "weather_alert_issued", "weather_alert_cleared":
		return c.applyWeather(event)
	default:
		return fmt.Errorf("%w: %q", ErrUnsupportedEventType, event.EventType)
	}
}

func requireEventType(eventType string) error {
	switch eventType {
	case "incident_reported", "incident_resolved", "unit_status_changed", "resource_status_changed", "road_status_changed", "weather_alert_issued", "weather_alert_cleared":
		return nil
	default:
		return fmt.Errorf("%w: %q", ErrUnsupportedEventType, eventType)
	}
}

func (c *cop) applyIncidentReported(event gen.CanonicalEvent) error {
	incidentID, err := payloadString(event, "incident_id")
	if err != nil {
		return err
	}
	category, err := payloadString(event, "category")
	if err != nil {
		return err
	}
	locationID, err := payloadString(event, "location_id")
	if err != nil {
		return err
	}
	index := c.incidentIndex(incidentID)
	if index < 0 {
		c.Incidents = append(c.Incidents, incidentFact{
			ClaimClass:     "reported_fact",
			IncidentID:     incidentID,
			Status:         "open",
			Category:       category,
			LocationID:     locationID,
			OpenedAt:       event.OccurredAt,
			SourceEventIDs: []string{event.CanonicalEventID},
		})
	} else {
		c.Incidents[index].Status = "open"
		c.Incidents[index].Category = category
		c.Incidents[index].LocationID = locationID
		c.Incidents[index].ResolvedAt = ""
		c.Incidents[index].SourceEventIDs = appendUnique(c.Incidents[index].SourceEventIDs, event.CanonicalEventID)
	}
	c.addEffectiveEvent(event.CanonicalEventID)
	return nil
}

func (c *cop) applyIncidentResolved(event gen.CanonicalEvent) error {
	incidentID, err := payloadString(event, "incident_id")
	if err != nil {
		return err
	}
	index := c.incidentIndex(incidentID)
	if index < 0 {
		return fmt.Errorf("incident %q cannot resolve before it is reported", incidentID)
	}
	c.Incidents[index].Status = "resolved"
	c.Incidents[index].ResolvedAt = event.OccurredAt
	c.Incidents[index].SourceEventIDs = appendUnique(c.Incidents[index].SourceEventIDs, event.CanonicalEventID)
	c.addEffectiveEvent(event.CanonicalEventID)
	return nil
}

func (c *cop) applyUnit(event gen.CanonicalEvent) error {
	unitID, err := payloadString(event, "unit_id")
	if err != nil {
		return err
	}
	availability, err := payloadString(event, "availability")
	if err != nil {
		return err
	}
	incidentID, _ := optionalPayloadString(event, "incident_id")
	fact := unitFact{"reported_fact", unitID, availability, incidentID, event.OccurredAt, event.CanonicalEventID}
	if index := c.unitIndex(unitID); index >= 0 {
		c.Units[index] = fact
	} else {
		c.Units = append(c.Units, fact)
	}
	c.linkIncident(incidentID, entityRef{Kind: "unit", ID: unitID})
	c.addEffectiveEvent(event.CanonicalEventID)
	return nil
}

func (c *cop) applyResource(event gen.CanonicalEvent) error {
	resourceID, err := payloadString(event, "resource_id")
	if err != nil {
		return err
	}
	availability, err := payloadString(event, "availability")
	if err != nil {
		return err
	}
	incidentID, _ := optionalPayloadString(event, "incident_id")
	fact := resourceFact{"reported_fact", resourceID, "resource", availability, incidentID, event.OccurredAt, event.CanonicalEventID}
	if index := c.resourceIndex(resourceID); index >= 0 {
		c.Resources[index] = fact
	} else {
		c.Resources = append(c.Resources, fact)
	}
	c.linkIncident(incidentID, entityRef{Kind: "resource", ID: resourceID})
	c.addEffectiveEvent(event.CanonicalEventID)
	return nil
}

func (c *cop) applyRoad(event gen.CanonicalEvent) error {
	roadID, err := payloadString(event, "road_id")
	if err != nil {
		return err
	}
	status, err := payloadString(event, "status")
	if err != nil {
		return err
	}
	fact := roadFact{"reported_fact", roadID, roadID, status, event.CanonicalEventID, event.OccurredAt}
	if index := c.roadIndex(roadID); index >= 0 {
		c.removeEffectiveEvent(c.Roads[index].EffectiveEventID)
		c.Roads[index] = fact
	} else {
		c.Roads = append(c.Roads, fact)
	}
	c.addEffectiveEvent(event.CanonicalEventID)
	return nil
}

func (c *cop) applyWeather(event gen.CanonicalEvent) error {
	weatherID, err := payloadString(event, "weather_alert_id")
	if err != nil {
		return err
	}
	status, err := payloadString(event, "status")
	if err != nil {
		return err
	}
	severity, _ := optionalPayloadString(event, "severity")
	summary, hasSummary := optionalPayloadString(event, "summary")
	index := c.weatherIndex(weatherID)
	if !hasSummary && index >= 0 {
		summary = c.WeatherAlerts[index].Summary
	}
	if summary == "" {
		summary = "weather alert status updated"
	}
	fact := weatherFact{"reported_fact", weatherID, status, severity, summary, event.OccurredAt, event.CanonicalEventID}
	if index >= 0 {
		c.removeEffectiveEvent(c.WeatherAlerts[index].SourceEventID)
		c.WeatherAlerts[index] = fact
	} else {
		c.WeatherAlerts = append(c.WeatherAlerts, fact)
	}
	c.addEffectiveEvent(event.CanonicalEventID)
	return nil
}

func payloadString(event gen.CanonicalEvent, field string) (string, error) {
	value, ok := optionalPayloadString(event, field)
	if !ok {
		return "", fmt.Errorf("canonical event %q requires payload.%s", event.CanonicalEventID, field)
	}
	return value, nil
}

func optionalPayloadString(event gen.CanonicalEvent, field string) (string, bool) {
	value, ok := event.Payload[field].(string)
	return value, ok && strings.TrimSpace(value) != ""
}

func (c *cop) addEffectiveEvent(id string) {
	c.EffectiveEventIDs = appendUnique(c.EffectiveEventIDs, id)
}

func (c *cop) removeEffectiveEvent(id string) {
	for index := len(c.EffectiveEventIDs) - 1; index >= 0; index-- {
		if c.EffectiveEventIDs[index] == id {
			c.EffectiveEventIDs = append(c.EffectiveEventIDs[:index], c.EffectiveEventIDs[index+1:]...)
		}
	}
}

func appendUnique(values []string, value string) []string {
	for _, existing := range values {
		if existing == value {
			return values
		}
	}
	return append(values, value)
}

func (c *cop) linkIncident(incidentID string, link entityRef) {
	if incidentID == "" {
		return
	}
	index := c.incidentIndex(incidentID)
	if index < 0 {
		return
	}
	for _, existing := range c.Incidents[index].LinkedEntities {
		if existing == link {
			return
		}
	}
	c.Incidents[index].LinkedEntities = append(c.Incidents[index].LinkedEntities, link)
	sort.Slice(c.Incidents[index].LinkedEntities, func(i, j int) bool {
		left, right := c.Incidents[index].LinkedEntities[i], c.Incidents[index].LinkedEntities[j]
		if left.Kind == right.Kind {
			return left.ID < right.ID
		}
		return left.Kind < right.Kind
	})
}

func (c *cop) incidentIndex(id string) int { return findIncident(c.Incidents, id) }
func (c *cop) unitIndex(id string) int     { return findUnit(c.Units, id) }
func (c *cop) resourceIndex(id string) int { return findResource(c.Resources, id) }
func (c *cop) roadIndex(id string) int     { return findRoad(c.Roads, id) }
func (c *cop) weatherIndex(id string) int  { return findWeather(c.WeatherAlerts, id) }

func findIncident(values []incidentFact, id string) int {
	for index, value := range values {
		if value.IncidentID == id {
			return index
		}
	}
	return -1
}

func findUnit(values []unitFact, id string) int {
	for index, value := range values {
		if value.UnitID == id {
			return index
		}
	}
	return -1
}

func findResource(values []resourceFact, id string) int {
	for index, value := range values {
		if value.ResourceID == id {
			return index
		}
	}
	return -1
}

func findRoad(values []roadFact, id string) int {
	for index, value := range values {
		if value.RoadID == id {
			return index
		}
	}
	return -1
}

func findWeather(values []weatherFact, id string) int {
	for index, value := range values {
		if value.WeatherAlertID == id {
			return index
		}
	}
	return -1
}

func resultForState(state cop, last gen.CanonicalEvent) (contracts.ProjectionResult, error) {
	sortCOP(&state)
	encodedCOP, err := json.Marshal(state)
	if err != nil {
		return contracts.ProjectionResult{}, fmt.Errorf("marshal COP: %w", err)
	}
	var output map[string]any
	if err := json.Unmarshal(encodedCOP, &output); err != nil {
		return contracts.ProjectionResult{}, fmt.Errorf("decode COP output: %w", err)
	}
	checkpoint := gen.Checkpoint{
		SchemaVersion:       schemaVersion,
		CheckpointID:        checkpointID(state.StateRevision, last.CanonicalSeq),
		StateRevision:       state.StateRevision,
		ThroughCanonicalSeq: last.CanonicalSeq,
		COP:                 encodedCOP,
		CreatedAt:           last.ReceivedAt,
	}
	projectionTime, err := parseProjectedAt(state.ProjectedAt)
	if err != nil {
		return contracts.ProjectionResult{}, err
	}
	return contracts.ProjectionResult{
		StateRevision: state.StateRevision,
		ProjectedAt:   projectionTime,
		COP:           output,
		Checkpoint:    checkpoint,
	}, nil
}

func resultFromCheckpoint(checkpoint gen.Checkpoint) (contracts.ProjectionResult, error) {
	state, err := copFromCheckpoint(checkpoint)
	if err != nil {
		return contracts.ProjectionResult{}, err
	}
	encoded, err := json.Marshal(state)
	if err != nil {
		return contracts.ProjectionResult{}, fmt.Errorf("marshal checkpoint COP: %w", err)
	}
	var output map[string]any
	if err := json.Unmarshal(encoded, &output); err != nil {
		return contracts.ProjectionResult{}, fmt.Errorf("decode checkpoint COP: %w", err)
	}
	projectionTime, err := parseProjectedAt(state.ProjectedAt)
	if err != nil {
		return contracts.ProjectionResult{}, err
	}
	return contracts.ProjectionResult{
		StateRevision: checkpoint.StateRevision,
		ProjectedAt:   projectionTime,
		COP:           output,
		Checkpoint:    checkpoint,
	}, nil
}

func copFromCheckpoint(checkpoint gen.Checkpoint) (cop, error) {
	if checkpoint.StateRevision < 1 || checkpoint.ThroughCanonicalSeq < 0 || len(checkpoint.COP) == 0 {
		return cop{}, errors.New("checkpoint is incomplete")
	}
	var state cop
	if err := json.Unmarshal(checkpoint.COP, &state); err != nil {
		return cop{}, fmt.Errorf("decode checkpoint COP: %w", err)
	}
	if state.StateRevision != checkpoint.StateRevision {
		return cop{}, fmt.Errorf("checkpoint COP revision %d does not match checkpoint revision %d", state.StateRevision, checkpoint.StateRevision)
	}
	ensureSlices(&state)
	sortCOP(&state)
	return state, nil
}

func ensureSlices(state *cop) {
	if state.EffectiveEventIDs == nil {
		state.EffectiveEventIDs = []string{}
	}
	if state.Incidents == nil {
		state.Incidents = []incidentFact{}
	}
	if state.Units == nil {
		state.Units = []unitFact{}
	}
	if state.Resources == nil {
		state.Resources = []resourceFact{}
	}
	if state.Roads == nil {
		state.Roads = []roadFact{}
	}
	if state.WeatherAlerts == nil {
		state.WeatherAlerts = []weatherFact{}
	}
	if state.ActiveInsightIDs == nil {
		state.ActiveInsightIDs = []string{}
	}
}

func sortCOP(state *cop) {
	sort.Strings(state.EffectiveEventIDs)
	sort.Strings(state.ActiveInsightIDs)
	sort.Slice(state.Incidents, func(i, j int) bool { return state.Incidents[i].IncidentID < state.Incidents[j].IncidentID })
	sort.Slice(state.Units, func(i, j int) bool { return state.Units[i].UnitID < state.Units[j].UnitID })
	sort.Slice(state.Resources, func(i, j int) bool { return state.Resources[i].ResourceID < state.Resources[j].ResourceID })
	sort.Slice(state.Roads, func(i, j int) bool { return state.Roads[i].RoadID < state.Roads[j].RoadID })
	sort.Slice(state.WeatherAlerts, func(i, j int) bool {
		return state.WeatherAlerts[i].WeatherAlertID < state.WeatherAlerts[j].WeatherAlertID
	})
	for index := range state.Incidents {
		sort.Strings(state.Incidents[index].SourceEventIDs)
		sort.Slice(state.Incidents[index].LinkedEntities, func(i, j int) bool {
			left, right := state.Incidents[index].LinkedEntities[i], state.Incidents[index].LinkedEntities[j]
			if left.Kind == right.Kind {
				return left.ID < right.ID
			}
			return left.Kind < right.Kind
		})
	}
}

func checkpointID(revision, sequence int64) string {
	return fmt.Sprintf("checkpoint-%020d-%020d", revision, sequence)
}

func parseProjectedAt(value string) (time.Time, error) {
	parsed, err := time.Parse(time.RFC3339Nano, value)
	if err != nil {
		return time.Time{}, fmt.Errorf("invalid deterministic projection timestamp %q: %w", value, err)
	}
	return parsed, nil
}
func cloneResult(result contracts.ProjectionResult) contracts.ProjectionResult {
	encoded, err := json.Marshal(result)
	if err != nil {
		return result
	}
	var clone contracts.ProjectionResult
	if err := json.Unmarshal(encoded, &clone); err != nil {
		return result
	}
	return clone
}
