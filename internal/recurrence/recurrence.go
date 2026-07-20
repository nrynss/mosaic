package recurrence

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"mosaic.local/mosaic/internal/contracts"
	"mosaic.local/mosaic/internal/ontology/gen"
)

// PreparedNote represents a reviewable note prepared when a prior recurrence is detected.
type PreparedNote struct {
	Note               string            `json:"note"`
	Executed           bool              `json:"executed"`
	LinkedPriorRecords []gen.AuditRecord `json:"linked_prior_records"`
}

// RecurrenceResult holds the detection outcome.
type RecurrenceResult struct {
	Detected     bool              `json:"detected"`
	PreparedNote *PreparedNote     `json:"prepared_note,omitempty"`
	PriorRecords []gen.AuditRecord `json:"prior_records,omitempty"`
}

// Detector is a domain-agnostic recurrence detector.
type Detector struct {
	Area   string
	Window time.Duration
	Clock  func() time.Time
}

// NewDetector creates a new Detector with the given area, window, and clock.
func NewDetector(area string, window time.Duration, clock func() time.Time) *Detector {
	if clock == nil {
		clock = time.Now
	}
	return &Detector{
		Area:   area,
		Window: window,
		Clock:  clock,
	}
}

// Detect checks for prior recorded handoffs in the same configured area within the window.
func (d *Detector) Detect(ctx context.Context, history contracts.AdvisoryHistory) (RecurrenceResult, error) {
	now := d.Clock()
	var priorRecords []gen.AuditRecord

	for _, record := range history.AuditRecords {
		// 1. Check if the record is a handoff action/note
		if !isHandoff(record) {
			continue
		}

		// 2. Check if it matches the configured area
		if !matchesArea(record, d.Area, history) {
			continue
		}

		// 3. Check if it's within the window
		if record.CreatedAt == "" {
			continue
		}
		recordTime, err := time.Parse(time.RFC3339, record.CreatedAt)
		if err != nil {
			// Skip malformed timestamps gracefully
			continue
		}

		if d.Window > 0 {
			diff := now.Sub(recordTime)
			if diff < 0 || diff > d.Window {
				continue
			}
		}

		priorRecords = append(priorRecords, record)
	}

	if len(priorRecords) == 0 {
		return RecurrenceResult{
			Detected: false,
		}, nil
	}

	// Prepare the reviewable note
	var priorIDs []string
	for _, pr := range priorRecords {
		if pr.AuditRecordID != "" {
			priorIDs = append(priorIDs, pr.AuditRecordID)
		}
	}

	noteText := fmt.Sprintf(
		"A prior road-condition handoff exists for this area. A new maintenance note has been prepared for review. Prior records: %s",
		strings.Join(priorIDs, ", "),
	)

	return RecurrenceResult{
		Detected: true,
		PreparedNote: &PreparedNote{
			Note:               noteText,
			Executed:           false,
			LinkedPriorRecords: priorRecords,
		},
		PriorRecords: priorRecords,
	}, nil
}

func isHandoff(record gen.AuditRecord) bool {
	action := strings.ToLower(record.Action)
	note := strings.ToLower(record.Note)

	// Actions like briefing_requested, acknowledged, or containing "handoff", "dispatch", "maintenance"
	if action == "briefing_requested" || action == "acknowledged" ||
		strings.Contains(action, "handoff") ||
		strings.Contains(action, "dispatch") ||
		strings.Contains(action, "maintenance") {
		return true
	}

	// Notes containing handoff-related terms
	if strings.Contains(note, "handoff") ||
		strings.Contains(note, "maintenance") ||
		strings.Contains(note, "dispatch") ||
		strings.Contains(note, "road-condition") ||
		strings.Contains(note, "road condition") ||
		strings.Contains(note, "briefing") {
		return true
	}

	return false
}

func matchesArea(record gen.AuditRecord, area string, history contracts.AdvisoryHistory) bool {
	if area == "" {
		return false
	}
	areaLower := strings.ToLower(area)

	// 1. Direct check on audit record note
	if strings.Contains(strings.ToLower(record.Note), areaLower) {
		return true
	}

	// 2. Direct check on target ID
	if strings.Contains(strings.ToLower(record.TargetID), areaLower) {
		return true
	}

	// Helper to check slice of dynamic values (e.g. evidence, assertions)
	matchesSlice := func(slice []any) bool {
		for _, item := range slice {
			data, err := json.Marshal(item)
			if err == nil && strings.Contains(strings.ToLower(string(data)), areaLower) {
				return true
			}
		}
		return false
	}

	// 3. If target is a recommendation, check the recommendation
	if strings.EqualFold(record.TargetKind, "recommendation") || strings.Contains(strings.ToLower(record.TargetID), "rec-") {
		for _, rec := range history.Recommendations {
			if rec.RecommendationID == record.TargetID {
				if strings.Contains(strings.ToLower(rec.Text), areaLower) {
					return true
				}
				if matchesSlice(rec.Evidence) {
					return true
				}

				// Resolve target insight from recommendation evidence
				for _, evAny := range rec.Evidence {
					if evMap, ok := evAny.(map[string]any); ok {
						if kind, ok := evMap["target_kind"].(string); ok && strings.EqualFold(kind, "insight") {
							if targetID, ok := evMap["target_id"].(string); ok {
								for _, ins := range history.Insights {
									if ins.InsightID == targetID {
										if matchesSlice(ins.Assertions) || matchesSlice(ins.Evidence) {
											return true
										}
									}
								}
							}
						}
					}
				}
			}
		}
	}

	// 4. If target is an insight, check the insight
	if strings.EqualFold(record.TargetKind, "insight") || strings.Contains(strings.ToLower(record.TargetID), "ins-") {
		for _, ins := range history.Insights {
			if ins.InsightID == record.TargetID {
				if matchesSlice(ins.Assertions) || matchesSlice(ins.Evidence) {
					return true
				}
			}
		}
	}

	return false
}
