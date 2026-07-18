// Package dataset validates frozen synthetic scenario artifacts.
package dataset

import (
	"bytes"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"time"

	"mosaic.local/mosaic/internal/ontology"
)

const DomesticDisturbance = "domestic-disturbance"

type rawEventsDocument struct {
	Version           string            `json:"version"`
	DatasetManifestID string            `json:"dataset_manifest_id"`
	RawEvents         []json.RawMessage `json:"raw_events"`
}

type outcomesDocument struct {
	Version         string            `json:"version"`
	ScenarioID      string            `json:"scenario_id"`
	LunaResults     []json.RawMessage `json:"luna_results"`
	CanonicalEvents []json.RawMessage `json:"canonical_events"`
	Insights        []json.RawMessage `json:"insights"`
	Recommendations []json.RawMessage `json:"recommendations"`
	AuditRecords    []json.RawMessage `json:"audit_records"`
	Checks          outcomeChecks     `json:"checks"`
}

type outcomeChecks struct {
	BaselineRawEventIDs []string   `json:"baseline_raw_event_ids"`
	Repaired            eventCheck `json:"repaired"`
	Quarantined         eventCheck `json:"quarantined"`
	LateDelivery        eventCheck `json:"late_delivery"`
	RoadCorrection      struct {
		CanonicalEventID  string `json:"canonical_event_id"`
		SupersedesEventID string `json:"supersedes_event_id"`
	} `json:"road_correction"`
	TerraObsolescence struct {
		ActiveInsightID       string `json:"active_insight_id"`
		ObsoleteInsightID     string `json:"obsolete_insight_id"`
		AfterCanonicalEventID string `json:"after_canonical_event_id"`
	} `json:"terra_obsolescence"`
	SolRequest struct {
		AuditRecordID    string `json:"audit_record_id"`
		BriefingID       string `json:"briefing_id"`
		RecommendationID string `json:"recommendation_id"`
		RequestedBy      string `json:"requested_by"`
	} `json:"sol_request"`
	SupervisorAction struct {
		AuditRecordID    string `json:"audit_record_id"`
		RecommendationID string `json:"recommendation_id"`
		ActorID          string `json:"actor_id"`
		Action           string `json:"action"`
	} `json:"supervisor_action"`
}

type eventCheck struct {
	RawEventID       string `json:"raw_event_id"`
	LunaResultID     string `json:"luna_result_id"`
	CanonicalEventID string `json:"canonical_event_id"`
}

type recordIndex map[string]map[string]any

// Validate compiles the P02 ontology and validates the frozen dataset. It never
// invokes llama.cpp, a remote service, or a model download.
func Validate(root string) error {
	schemas, err := ontology.CompileDir(filepath.Join(root, "ontology"))
	if err != nil {
		return err
	}
	return ValidateArtifacts(schemas, filepath.Join(root, "datasets", DomesticDisturbance))
}

// ValidateArtifacts validates a staged domestic-disturbance artifact directory.
func ValidateArtifacts(schemas map[string]ontology.Schema, dir string) error {
	if err := validateArtifactDirectory(dir); err != nil {
		return err
	}
	manifest, err := readObject(filepath.Join(dir, "manifest.json"))
	if err != nil {
		return fmt.Errorf("manifest: %w", err)
	}
	if err := validateSchema(schemas, "dataset-manifest.schema.json", manifest); err != nil {
		return fmt.Errorf("manifest: %w", err)
	}
	manifestID := stringField(manifest, "dataset_manifest_id")
	if manifestID == "" {
		return errors.New("manifest: dataset_manifest_id is required")
	}
	if err := validateManifestVersions(schemas, manifest); err != nil {
		return fmt.Errorf("manifest: %w", err)
	}
	idMap, err := stringMap(manifest, "id_map")
	if err != nil {
		return fmt.Errorf("manifest: %w", err)
	}
	if err := validateIDMap(idMap); err != nil {
		return fmt.Errorf("manifest: %w", err)
	}
	if idMap["dataset_manifest"] != manifestID {
		return errors.New("manifest: id_map.dataset_manifest does not match dataset_manifest_id")
	}

	scenario, err := readObject(filepath.Join(dir, "scenario.json"))
	if err != nil {
		return fmt.Errorf("scenario: %w", err)
	}
	if err := validateSchema(schemas, "scenario.schema.json", scenario); err != nil {
		return fmt.Errorf("scenario: %w", err)
	}
	scenarioID := stringField(scenario, "scenario_id")
	if scenarioID == "" || idMap["scenario"] != scenarioID {
		return errors.New("scenario: scenario_id is absent from manifest id_map")
	}
	if stringField(scenario, "dataset_manifest_id") != manifestID {
		return errors.New("scenario: dataset_manifest_id does not reference manifest")
	}

	rawDoc, err := readStrict[rawEventsDocument](filepath.Join(dir, "raw-events.json"))
	if err != nil {
		return fmt.Errorf("raw-events: %w", err)
	}
	if rawDoc.Version != "1.0.0" || rawDoc.DatasetManifestID != manifestID || len(rawDoc.RawEvents) == 0 {
		return errors.New("raw-events: require version 1.0.0, manifest reference, and events")
	}
	rawRecords, err := indexRecords(schemas, "raw-event.schema.json", rawDoc.RawEvents, "raw_event_id")
	if err != nil {
		return fmt.Errorf("raw-events: %w", err)
	}
	for id, raw := range rawRecords {
		if !containsID(idMap, id) {
			return fmt.Errorf("raw-events: raw_event_id %q is absent from manifest id_map", id)
		}
		if err := validateRawIntegrity(raw); err != nil {
			return fmt.Errorf("raw-events %s: %w", id, err)
		}
	}

	outcomes, err := readStrict[outcomesDocument](filepath.Join(dir, "expected-outcomes.json"))
	if err != nil {
		return fmt.Errorf("expected-outcomes: %w", err)
	}
	if outcomes.Version != "1.0.0" || outcomes.ScenarioID != scenarioID {
		return errors.New("expected-outcomes: version or scenario reference is invalid")
	}
	canonical, err := indexRecords(schemas, "canonical-event.schema.json", outcomes.CanonicalEvents, "canonical_event_id")
	if err != nil {
		return fmt.Errorf("expected-outcomes canonical_events: %w", err)
	}
	luna, err := indexRecords(schemas, "luna-result.schema.json", outcomes.LunaResults, "luna_result_id")
	if err != nil {
		return fmt.Errorf("expected-outcomes luna_results: %w", err)
	}
	insights, err := indexRecords(schemas, "insight.schema.json", outcomes.Insights, "insight_id")
	if err != nil {
		return fmt.Errorf("expected-outcomes insights: %w", err)
	}
	recommendations, err := indexRecords(schemas, "recommendation.schema.json", outcomes.Recommendations, "recommendation_id")
	if err != nil {
		return fmt.Errorf("expected-outcomes recommendations: %w", err)
	}
	audit, err := indexRecords(schemas, "audit-record.schema.json", outcomes.AuditRecords, "audit_record_id")
	if err != nil {
		return fmt.Errorf("expected-outcomes audit_records: %w", err)
	}
	for _, records := range []recordIndex{canonical, luna, insights, recommendations, audit} {
		for id := range records {
			if !containsID(idMap, id) {
				return fmt.Errorf("expected-outcomes: record id %q is absent from manifest id_map", id)
			}
		}
	}
	if err := validateScenarioBeats(scenario, rawRecords); err != nil {
		return fmt.Errorf("scenario: %w", err)
	}
	if err := validateCanonicalEvents(canonical, rawRecords); err != nil {
		return fmt.Errorf("expected-outcomes canonical_events: %w", err)
	}
	if err := validateLunaResults(luna, rawRecords, canonical); err != nil {
		return fmt.Errorf("expected-outcomes luna_results: %w", err)
	}
	if err := validateEvidence(insights, rawRecords, canonical, insights); err != nil {
		return fmt.Errorf("expected-outcomes insights: %w", err)
	}
	if err := validateEvidence(recommendations, rawRecords, canonical, insights); err != nil {
		return fmt.Errorf("expected-outcomes recommendations: %w", err)
	}
	if err := validateOutcomeChecks(outcomes.Checks, scenario, rawRecords, luna, canonical, insights, recommendations, audit); err != nil {
		return fmt.Errorf("expected-outcomes checks: %w", err)
	}
	return nil
}

func validateArtifactDirectory(dir string) error {
	allowed := map[string]bool{"manifest.json": true, "scenario.json": true, "raw-events.json": true, "expected-outcomes.json": true}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return fmt.Errorf("read dataset directory: %w", err)
	}
	for _, entry := range entries {
		if entry.IsDir() || !allowed[entry.Name()] {
			return fmt.Errorf("unexpected artifact %q", entry.Name())
		}
	}
	for name := range allowed {
		if _, err := os.Stat(filepath.Join(dir, name)); err != nil {
			return fmt.Errorf("missing %s", name)
		}
	}
	return nil
}

func readObject(path string) (map[string]any, error) {
	content, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var value map[string]any
	if err := json.Unmarshal(content, &value); err != nil {
		return nil, err
	}
	if value == nil {
		return nil, errors.New("must be a JSON object")
	}
	return value, nil
}

func readStrict[T any](path string) (T, error) {
	var value T
	content, err := os.ReadFile(path)
	if err != nil {
		return value, err
	}
	decoder := json.NewDecoder(bytes.NewReader(content))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&value); err != nil {
		return value, err
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		return value, errors.New("contains multiple JSON values")
	}
	return value, nil
}

func validateSchema(schemas map[string]ontology.Schema, name string, value any) error {
	schema, ok := schemas[name]
	if !ok {
		return fmt.Errorf("schema %s was not compiled", name)
	}
	if err := schema.Compiled.Validate(value); err != nil {
		return err
	}
	return nil
}

func indexRecords(schemas map[string]ontology.Schema, schemaName string, messages []json.RawMessage, idField string) (recordIndex, error) {
	if len(messages) == 0 {
		return nil, errors.New("must not be empty")
	}
	indexed := make(recordIndex, len(messages))
	for position, message := range messages {
		var record map[string]any
		if err := json.Unmarshal(message, &record); err != nil {
			return nil, fmt.Errorf("record %d: %w", position, err)
		}
		if err := validateSchema(schemas, schemaName, record); err != nil {
			return nil, fmt.Errorf("record %d: %w", position, err)
		}
		id := stringField(record, idField)
		if id == "" {
			return nil, fmt.Errorf("record %d has no %s", position, idField)
		}
		if _, exists := indexed[id]; exists {
			return nil, fmt.Errorf("duplicate %s %q", idField, id)
		}
		indexed[id] = record
	}
	return indexed, nil
}

func validateManifestVersions(schemas map[string]ontology.Schema, manifest map[string]any) error {
	versions, err := stringMap(manifest, "schema_versions")
	if err != nil {
		return err
	}
	for _, name := range ontology.SchemaFiles {
		schema := schemas[name]
		if versions[name] != schema.Version {
			return fmt.Errorf("schema_versions.%s = %q, want %q", name, versions[name], schema.Version)
		}
	}
	return nil
}

func validateIDMap(ids map[string]string) error {
	seen := map[string]string{}
	for name, value := range ids {
		if name == "" || value == "" {
			return errors.New("id_map keys and values must be non-empty")
		}
		if prior, exists := seen[value]; exists {
			return fmt.Errorf("id_map value %q is duplicated by %s and %s", value, prior, name)
		}
		seen[value] = name
	}
	return nil
}

func validateRawIntegrity(raw map[string]any) error {
	source, ok := raw["source"].(map[string]any)
	if !ok || (stringField(source, "source_record_id") == "" && stringField(source, "idempotency_key") == "") {
		return errors.New("source requires source_record_id or idempotency_key")
	}
	payload, err := base64.StdEncoding.DecodeString(stringField(raw, "payload_bytes_b64"))
	if err != nil {
		return fmt.Errorf("payload_bytes_b64: %w", err)
	}
	sum := sha256.Sum256(payload)
	if hex.EncodeToString(sum[:]) != stringField(raw, "raw_sha256") {
		return errors.New("raw_sha256 does not match payload_bytes_b64")
	}
	return nil
}

func validateScenarioBeats(scenario map[string]any, raw recordIndex) error {
	beats, ok := scenario["beats"].([]any)
	if !ok || len(beats) < 6 {
		return errors.New("requires at least the six baseline beats")
	}
	type beat struct {
		order int
		rawID string
		id    string
	}
	parsed := make([]beat, 0, len(beats))
	for _, value := range beats {
		item, ok := value.(map[string]any)
		if !ok {
			return errors.New("beat must be an object")
		}
		order, ok := item["order"].(float64)
		if !ok || order != float64(int(order)) {
			return errors.New("beat order must be an integer")
		}
		id, rawID := stringField(item, "beat_id"), stringField(item, "raw_event_id")
		if id == "" || rawID == "" {
			return errors.New("beat requires beat_id and raw_event_id")
		}
		if _, exists := raw[rawID]; !exists {
			return fmt.Errorf("beat %q references unknown raw event %q", id, rawID)
		}
		parsed = append(parsed, beat{int(order), rawID, id})
	}
	sort.Slice(parsed, func(i, j int) bool { return parsed[i].order < parsed[j].order })
	for i, beat := range parsed {
		if beat.order != i+1 {
			return fmt.Errorf("beat order %d is not contiguous", beat.order)
		}
	}
	baseline := []string{"baseline-01-911-call", "baseline-02-welfare-check", "baseline-03-weather-alert", "baseline-04-road-closure", "baseline-05-ems-availability", "baseline-06-officer-update"}
	for i, want := range baseline {
		if parsed[i].id != want {
			return fmt.Errorf("baseline beat %d = %q, want %q", i+1, parsed[i].id, want)
		}
	}
	return nil
}

func validateCanonicalEvents(canonical, raw recordIndex) error {
	sequences := make([]int, 0, len(canonical))
	for id, event := range canonical {
		rawID := stringField(event, "raw_event_id")
		if _, exists := raw[rawID]; !exists {
			return fmt.Errorf("%s references unknown raw event %q", id, rawID)
		}
		provenance, ok := event["provenance"].(map[string]any)
		if !ok || stringField(provenance, "raw_event_id") != rawID {
			return fmt.Errorf("%s provenance does not match raw_event_id", id)
		}
		seq, ok := event["canonical_seq"].(float64)
		if !ok || seq != float64(int(seq)) {
			return fmt.Errorf("%s has invalid canonical_seq", id)
		}
		sequences = append(sequences, int(seq))
	}
	sort.Ints(sequences)
	for index, seq := range sequences {
		if seq != index+1 {
			return fmt.Errorf("canonical_seq %d is missing", index+1)
		}
	}
	for id, event := range canonical {
		if superseded := stringField(event, "supersedes_event_id"); superseded != "" {
			prior, exists := canonical[superseded]
			if !exists {
				return fmt.Errorf("%s supersedes unknown event %q", id, superseded)
			}
			if stringField(event, "event_type") != "road_status_changed" || stringField(prior, "event_type") != "road_status_changed" {
				return fmt.Errorf("%s only road corrections are permitted in this fixture", id)
			}
			if payloadID(event, "road_id") != payloadID(prior, "road_id") {
				return fmt.Errorf("%s correction changes road_id", id)
			}
		}
	}
	return nil
}

func validateLunaResults(luna, raw, canonical recordIndex) error {
	byRaw := map[string]string{}
	for id, result := range luna {
		rawID := stringField(result, "raw_event_id")
		if _, exists := raw[rawID]; !exists {
			return fmt.Errorf("%s references unknown raw event %q", id, rawID)
		}
		if prior, exists := byRaw[rawID]; exists {
			return fmt.Errorf("raw event %q has Luna results %s and %s", rawID, prior, id)
		}
		byRaw[rawID] = id
		canonicalID, status := stringField(result, "canonical_event_id"), stringField(result, "status")
		if status == "accepted" || status == "repaired" {
			event, exists := canonical[canonicalID]
			if !exists || stringField(event, "raw_event_id") != rawID {
				return fmt.Errorf("%s has invalid canonical_event_id %q", id, canonicalID)
			}
		} else if canonicalID != "" {
			return fmt.Errorf("%s %s result must not name canonical event", id, status)
		}
	}
	if len(byRaw) != len(raw) {
		return errors.New("every raw event requires exactly one Luna result")
	}
	return nil
}

func validateEvidence(records, raw, canonical, insights recordIndex) error {
	for id, record := range records {
		evidence, ok := record["evidence"].([]any)
		if !ok || len(evidence) == 0 {
			return fmt.Errorf("%s requires evidence", id)
		}
		for _, value := range evidence {
			ref, ok := value.(map[string]any)
			if !ok {
				return fmt.Errorf("%s evidence must be an object", id)
			}
			targetID := stringField(ref, "target_id")
			switch stringField(ref, "target_kind") {
			case "raw_event":
				if _, ok := raw[targetID]; !ok {
					return fmt.Errorf("%s cites unknown raw event %q", id, targetID)
				}
			case "canonical_event":
				if _, ok := canonical[targetID]; !ok {
					return fmt.Errorf("%s cites unknown canonical event %q", id, targetID)
				}
			case "insight":
				if _, ok := insights[targetID]; !ok {
					return fmt.Errorf("%s cites unknown insight %q", id, targetID)
				}
			default:
				return fmt.Errorf("%s uses unsupported fixture evidence target %q", id, stringField(ref, "target_kind"))
			}
		}
	}
	return nil
}

func validateOutcomeChecks(checks outcomeChecks, scenario map[string]any, raw, luna, canonical, insights, recommendations, audit recordIndex) error {
	if len(checks.BaselineRawEventIDs) != 6 {
		return errors.New("baseline_raw_event_ids must contain six entries")
	}
	beats := scenario["beats"].([]any)
	for index, rawID := range checks.BaselineRawEventIDs {
		beat := beats[index].(map[string]any)
		if rawID == "" || stringField(beat, "raw_event_id") != rawID {
			return fmt.Errorf("baseline raw event %d does not match scenario", index+1)
		}
	}
	if err := validateEventCheck(checks.Repaired, "repaired", "repaired", raw, luna, canonical, true); err != nil {
		return err
	}
	if err := validateEventCheck(checks.Quarantined, "quarantined", "quarantined", raw, luna, canonical, false); err != nil {
		return err
	}
	if err := validateEventCheck(checks.LateDelivery, "late delivery", "accepted", raw, luna, canonical, true); err != nil {
		return err
	}
	lateRaw := raw[checks.LateDelivery.RawEventID]
	occurred, err := time.Parse(time.RFC3339, stringField(lateRaw, "source_occurred_at"))
	if err != nil {
		return fmt.Errorf("late delivery occurred_at: %w", err)
	}
	received, err := time.Parse(time.RFC3339, stringField(lateRaw, "received_at"))
	if err != nil {
		return fmt.Errorf("late delivery received_at: %w", err)
	}
	if !received.After(occurred) {
		return errors.New("late delivery received_at must follow source_occurred_at")
	}
	correction, exists := canonical[checks.RoadCorrection.CanonicalEventID]
	if !exists || stringField(correction, "supersedes_event_id") != checks.RoadCorrection.SupersedesEventID || canonical[checks.RoadCorrection.SupersedesEventID] == nil {
		return errors.New("road correction does not supersede its expected event")
	}
	active, activeOK := insights[checks.TerraObsolescence.ActiveInsightID]
	obsolete, obsoleteOK := insights[checks.TerraObsolescence.ObsoleteInsightID]
	if !activeOK || !obsoleteOK || stringField(active, "lifecycle_status") != "active" || stringField(obsolete, "lifecycle_status") != "obsolete" || stringField(obsolete, "supersedes_insight_id") != checks.TerraObsolescence.ActiveInsightID || canonical[checks.TerraObsolescence.AfterCanonicalEventID] == nil {
		return errors.New("Terra obsolescence references are invalid")
	}
	solAudit, solOK := audit[checks.SolRequest.AuditRecordID]
	if !solOK || stringField(solAudit, "action") != "briefing_requested" || stringField(solAudit, "actor_id") != checks.SolRequest.RequestedBy || stringField(solAudit, "target_kind") != "briefing" || stringField(solAudit, "target_id") != checks.SolRequest.BriefingID || recommendations[checks.SolRequest.RecommendationID] == nil {
		return errors.New("Sol request expectation is invalid")
	}
	actionAudit, actionOK := audit[checks.SupervisorAction.AuditRecordID]
	if !actionOK || stringField(actionAudit, "target_kind") != "recommendation" || stringField(actionAudit, "target_id") != checks.SupervisorAction.RecommendationID || stringField(actionAudit, "actor_id") != checks.SupervisorAction.ActorID || stringField(actionAudit, "action") != checks.SupervisorAction.Action || recommendations[checks.SupervisorAction.RecommendationID] == nil {
		return errors.New("supervisor action expectation is invalid")
	}
	return nil
}

func validateEventCheck(check eventCheck, label, status string, raw, luna, canonical recordIndex, requireCanonical bool) error {
	if check.RawEventID == "" || check.LunaResultID == "" || (requireCanonical && check.CanonicalEventID == "") {
		return fmt.Errorf("%s expectation is incomplete", label)
	}
	result, exists := luna[check.LunaResultID]
	if !exists || stringField(result, "raw_event_id") != check.RawEventID || stringField(result, "status") != status || raw[check.RawEventID] == nil {
		return fmt.Errorf("%s expectation does not match Luna result", label)
	}
	if requireCanonical && (stringField(result, "canonical_event_id") != check.CanonicalEventID || canonical[check.CanonicalEventID] == nil) {
		return fmt.Errorf("%s expectation does not match canonical event", label)
	}
	if !requireCanonical && check.CanonicalEventID != "" {
		return fmt.Errorf("%s must not name a canonical event", label)
	}
	return nil
}

func stringMap(object map[string]any, field string) (map[string]string, error) {
	value, ok := object[field].(map[string]any)
	if !ok {
		return nil, fmt.Errorf("%s must be an object", field)
	}
	result := make(map[string]string, len(value))
	for key, item := range value {
		stringValue, ok := item.(string)
		if !ok || stringValue == "" {
			return nil, fmt.Errorf("%s.%s must be a non-empty string", field, key)
		}
		result[key] = stringValue
	}
	return result, nil
}

func stringField(object map[string]any, field string) string {
	value, _ := object[field].(string)
	return value
}
func containsID(ids map[string]string, needle string) bool {
	for _, value := range ids {
		if value == needle {
			return true
		}
	}
	return false
}
func payloadID(event map[string]any, field string) string {
	payload, _ := event["payload"].(map[string]any)
	return stringField(payload, field)
}
