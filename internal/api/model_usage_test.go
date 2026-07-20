package api

import (
	"net/http"
	"testing"
	"time"

	"mosaic.local/mosaic/internal/usage"
)

func TestModelUsageDefaultsToZeroWithoutBudget(t *testing.T) {
	fixture := newFixture(t)
	response := request(t, fixture.handler, http.MethodGet, "/api/v1/model-usage", "", "")
	if response.Code != http.StatusOK {
		t.Fatalf("model-usage status = %d, body = %s", response.Code, response.Body.String())
	}
	data := responseData(t, response)
	if data["input_tokens"] != float64(0) || data["output_tokens"] != float64(0) {
		t.Fatalf("expected zero token totals for a fresh fixture: %#v", data)
	}
	if data["estimated_spend_usd"] != float64(0) {
		t.Fatalf("expected zero estimated spend for a fresh fixture: %#v", data)
	}
	if data["live_runs"] != float64(0) {
		t.Fatalf("expected zero live runs for a fresh fixture: %#v", data)
	}
	if _, hasBudget := data["budget_usd"]; hasBudget {
		t.Fatalf("budget_usd should be omitted when no budget is configured: %#v", data)
	}
	if _, hasRemaining := data["estimated_remaining_usd"]; hasRemaining {
		t.Fatalf("estimated_remaining_usd should be omitted when no budget is configured: %#v", data)
	}
	if note, _ := data["note"].(string); note == "" {
		t.Fatalf("expected a non-empty estimate disclaimer note: %#v", data)
	}
}

func TestModelUsageReportsRecordedUsageAndBudget(t *testing.T) {
	fixture := newFixture(t)
	accumulator := usage.NewAccumulator(time.Date(2026, 7, 20, 0, 0, 0, 0, time.UTC))
	accumulator.Record("gpt-5.6", 1000, 500)
	budget := 5.0

	server, err := New(Config{
		Recovery:      fixture.server.recovery,
		Records:       fixture.store,
		Evidence:      fixture.server.evidence,
		Operations:    fixture.server.operations,
		Stream:        fixture.broker,
		Usage:         accumulator,
		DemoBudgetUSD: &budget,
	})
	if err != nil {
		t.Fatalf("new server with usage accumulator: %v", err)
	}

	response := request(t, server.Handler(), http.MethodGet, "/api/v1/model-usage", "", "")
	if response.Code != http.StatusOK {
		t.Fatalf("model-usage status = %d, body = %s", response.Code, response.Body.String())
	}
	data := responseData(t, response)
	if data["input_tokens"] != float64(1000) || data["output_tokens"] != float64(500) {
		t.Fatalf("token totals = %#v", data)
	}
	if data["live_runs"] != float64(1) {
		t.Fatalf("live_runs = %#v, want 1", data["live_runs"])
	}
	spend, _ := data["estimated_spend_usd"].(float64)
	if spend <= 0 {
		t.Fatalf("estimated_spend_usd = %v, want > 0", spend)
	}
	if data["budget_usd"] != budget {
		t.Fatalf("budget_usd = %#v, want %v", data["budget_usd"], budget)
	}
	remaining, ok := data["estimated_remaining_usd"].(float64)
	if !ok {
		t.Fatalf("estimated_remaining_usd missing: %#v", data)
	}
	// 1000 input tokens @ $1.25/1M + 500 output tokens @ $10.00/1M = $0.00625,
	// rounded to 4 decimal places by the handler; remaining = budget - raw spend, also rounded.
	wantRemaining := 4.9938
	if remaining < wantRemaining-0.0001 || remaining > wantRemaining+0.0001 {
		t.Fatalf("estimated_remaining_usd = %v, want ~%v", remaining, wantRemaining)
	}
	if since, _ := data["since"].(string); since == "" {
		t.Fatalf("since missing: %#v", data)
	}
}

func TestModelUsageRejectsNonGet(t *testing.T) {
	fixture := newFixture(t)
	response := request(t, fixture.handler, http.MethodPost, "/api/v1/model-usage", "", "")
	if response.Code != http.StatusMethodNotAllowed {
		t.Fatalf("POST model-usage status = %d, want 405", response.Code)
	}
}
