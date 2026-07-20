package recurrence

import (
	"context"
	"strings"
	"testing"
	"time"

	"mosaic.local/mosaic/internal/contracts"
	"mosaic.local/mosaic/internal/ontology/gen"
)

func TestDetector_AreaMatching(t *testing.T) {
	refTime := time.Date(2026, 7, 20, 12, 0, 0, 0, time.UTC)
	clock := func() time.Time { return refTime }

	history := contracts.AdvisoryHistory{
		AuditRecords: []gen.AuditRecord{
			{
				AuditRecordID: "ar-1",
				Action:        "briefing_requested",
				TargetID:      "rec-1",
				TargetKind:    "recommendation",
				CreatedAt:     refTime.Format(time.RFC3339),
			},
			{
				AuditRecordID: "ar-2",
				Action:        "acknowledged",
				TargetID:      "route-66",
				TargetKind:    "road",
				CreatedAt:     refTime.Format(time.RFC3339),
			},
			{
				AuditRecordID: "ar-3",
				Action:        "dispatch_approved",
				Note:          "Handoff for route-101 is ready",
				CreatedAt:     refTime.Format(time.RFC3339),
			},
		},
		Recommendations: []gen.Recommendation{
			{
				RecommendationID: "rec-1",
				Text:             "Clean debris on route-99",
				CreatedAt:        refTime.Format(time.RFC3339),
			},
		},
	}

	tests := []struct {
		name     string
		area     string
		detected bool
		priorIDs []string
	}{
		{
			name:     "match by recommendation text",
			area:     "route-99",
			detected: true,
			priorIDs: []string{"ar-1"},
		},
		{
			name:     "match by target ID",
			area:     "route-66",
			detected: true,
			priorIDs: []string{"ar-2"},
		},
		{
			name:     "match by note substring",
			area:     "route-101",
			detected: true,
			priorIDs: []string{"ar-3"},
		},
		{
			name:     "no match",
			area:     "route-55",
			detected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			detector := NewDetector(tt.area, 24*time.Hour, clock)
			res, err := detector.Detect(context.Background(), history)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if res.Detected != tt.detected {
				t.Errorf("expected detected=%v, got %v", tt.detected, res.Detected)
			}
			if tt.detected {
				if res.PreparedNote == nil {
					t.Fatal("expected prepared note, got nil")
				}
				if res.PreparedNote.Executed {
					t.Error("expected PreparedNote.Executed to be false")
				}
				// Verify note linkage
				for _, expectedID := range tt.priorIDs {
					found := false
					for _, pr := range res.PreparedNote.LinkedPriorRecords {
						if pr.AuditRecordID == expectedID {
							found = true
							break
						}
					}
					if !found {
						t.Errorf("expected linked record %s not found in PreparedNote", expectedID)
					}
				}
			}
		})
	}
}

func TestDetector_WindowFiltering(t *testing.T) {
	refTime := time.Date(2026, 7, 20, 12, 0, 0, 0, time.UTC)
	clock := func() time.Time { return refTime }

	history := contracts.AdvisoryHistory{
		AuditRecords: []gen.AuditRecord{
			{
				AuditRecordID: "ar-within",
				Action:        "briefing_requested",
				Note:          "road-condition handoff for A1",
				CreatedAt:     refTime.Add(-30 * time.Minute).Format(time.RFC3339),
			},
			{
				AuditRecordID: "ar-outside",
				Action:        "briefing_requested",
				Note:          "road-condition handoff for A1",
				CreatedAt:     refTime.Add(-90 * time.Minute).Format(time.RFC3339),
			},
		},
	}

	// 1. With 1 hour window: should only detect ar-within
	detector1Hour := NewDetector("A1", 1*time.Hour, clock)
	res1, err := detector1Hour.Detect(context.Background(), history)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !res1.Detected {
		t.Error("expected to detect handoff within 1 hour window")
	}
	if len(res1.PriorRecords) != 1 || res1.PriorRecords[0].AuditRecordID != "ar-within" {
		t.Errorf("expected only ar-within, got %v", res1.PriorRecords)
	}

	// 2. With 2 hour window: should detect both
	detector2Hour := NewDetector("A1", 2*time.Hour, clock)
	res2, err := detector2Hour.Detect(context.Background(), history)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !res2.Detected {
		t.Error("expected to detect handoffs within 2 hour window")
	}
	if len(res2.PriorRecords) != 2 {
		t.Errorf("expected 2 records, got %d", len(res2.PriorRecords))
	}
}

func TestDetector_PreparedNoteAndNoAutonomousAction(t *testing.T) {
	refTime := time.Date(2026, 7, 20, 12, 0, 0, 0, time.UTC)
	clock := func() time.Time { return refTime }

	history := contracts.AdvisoryHistory{
		AuditRecords: []gen.AuditRecord{
			{
				AuditRecordID: "ar-1",
				Action:        "briefing_requested",
				Note:          "road-condition maintenance for Area-X",
				CreatedAt:     refTime.Format(time.RFC3339),
			},
			{
				AuditRecordID: "ar-2",
				Action:        "acknowledged",
				Note:          "Area-X handoff acknowledged",
				CreatedAt:     refTime.Format(time.RFC3339),
			},
		},
	}

	detector := NewDetector("Area-X", 10*time.Hour, clock)
	res, err := detector.Detect(context.Background(), history)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !res.Detected {
		t.Fatal("expected recurrence to be detected")
	}

	if res.PreparedNote == nil {
		t.Fatal("expected prepared note to be non-nil")
	}

	// Prove nothing is "sent" / no autonomous action
	if res.PreparedNote.Executed {
		t.Error("expected PreparedNote.Executed to be false (no autonomous actions allowed)")
	}

	// Prove prepared-note linkage correctly lists prior records
	expectedSubStr := "A prior road-condition handoff exists for this area. A new maintenance note has been prepared for review."
	if !strings.Contains(res.PreparedNote.Note, expectedSubStr) {
		t.Errorf("expected note to contain %q, got %q", expectedSubStr, res.PreparedNote.Note)
	}

	// Check if IDs are listed
	if !strings.Contains(res.PreparedNote.Note, "ar-1") || !strings.Contains(res.PreparedNote.Note, "ar-2") {
		t.Errorf("expected note to contain prior record IDs 'ar-1' and 'ar-2', got %q", res.PreparedNote.Note)
	}

	if len(res.PreparedNote.LinkedPriorRecords) != 2 {
		t.Errorf("expected 2 linked prior records, got %d", len(res.PreparedNote.LinkedPriorRecords))
	}
}
