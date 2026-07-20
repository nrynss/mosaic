// Package usage estimates OpenAI spend for this server process only.
//
// OpenAI does not expose a balance or spend-remaining endpoint for project
// API keys, so there is no way to ask the provider "how much credit is left
// on this key." What this package offers instead is an honest, local
// estimate: it tallies the token counts Mosaic's own live model calls report
// and multiplies them by a hardcoded per-model price table. It is:
//   - in-memory only (never written to SQLite; Cloud Run's /tmp is ephemeral
//     anyway, so a per-process estimate is the honest scope),
//   - reset to zero whenever the process restarts,
//   - based on prices baked into this file, not a live pricing feed, and
//   - limited to calls Mosaic itself makes (Luna/Terra/Sol live invocations).
//
// It is not, and must never be presented as, the caller's actual OpenAI
// account balance.
package usage

import (
	"strings"
	"sync"
	"time"
)

// ModelPricing is an approximate USD price per single token for one model.
type ModelPricing struct {
	InputPerToken  float64
	OutputPerToken float64
}

// priceTable covers the model name(s) Mosaic's live agents actually request
// (see openaimodel.DefaultLunaModel / DefaultTerraModel / DefaultSolModel,
// which all currently resolve to "gpt-5.6"). If Mosaic starts requesting a
// different model, add its price here; otherwise the estimate silently keeps
// using defaultPricing for the new name.
var priceTable = map[string]ModelPricing{
	"gpt-5.6": {
		InputPerToken:  1.25 / 1_000_000,  // $1.25 / 1M input tokens (approximate)
		OutputPerToken: 10.00 / 1_000_000, // $10.00 / 1M output tokens (approximate)
	},
}

// defaultPricing is used for any model name absent from priceTable so an
// unrecognised model still yields a (clearly rough) non-zero estimate rather
// than silently reporting zero spend.
var defaultPricing = ModelPricing{
	InputPerToken:  1.25 / 1_000_000,
	OutputPerToken: 10.00 / 1_000_000,
}

func pricingFor(model string) ModelPricing {
	if pricing, ok := priceTable[strings.TrimSpace(model)]; ok {
		return pricing
	}
	return defaultPricing
}

// Snapshot is the read-only public view of accumulated usage at one instant.
type Snapshot struct {
	InputTokens       int64
	OutputTokens      int64
	EstimatedSpendUSD float64
	LiveRuns          int64
	Since             time.Time
}

// Accumulator is a mutex-guarded, in-memory tally of live OpenAI token usage
// for this process. It never persists to SQLite or any other durable store.
type Accumulator struct {
	mu           sync.Mutex
	inputTokens  int64
	outputTokens int64
	spendUSD     float64
	liveRuns     int64
	since        time.Time
}

// NewAccumulator creates an empty accumulator stamped with the given start
// time (normally the process start time, or a fixed instant in tests).
func NewAccumulator(since time.Time) *Accumulator {
	return &Accumulator{since: since}
}

// Record adds one successful live OpenAI call's token usage to the running
// totals and estimates its USD cost from the hardcoded price table. Negative
// token counts (never expected from OpenAI, but defensive against a decoding
// surprise) are clamped to zero rather than allowed to skew the estimate.
func (a *Accumulator) Record(model string, inputTokens, outputTokens int) {
	if a == nil {
		return
	}
	if inputTokens < 0 {
		inputTokens = 0
	}
	if outputTokens < 0 {
		outputTokens = 0
	}
	pricing := pricingFor(model)
	cost := float64(inputTokens)*pricing.InputPerToken + float64(outputTokens)*pricing.OutputPerToken

	a.mu.Lock()
	defer a.mu.Unlock()
	a.inputTokens += int64(inputTokens)
	a.outputTokens += int64(outputTokens)
	a.spendUSD += cost
	a.liveRuns++
}

// Snapshot returns the current totals. Safe for concurrent use; the returned
// value is an independent copy.
func (a *Accumulator) Snapshot() Snapshot {
	if a == nil {
		return Snapshot{}
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	return Snapshot{
		InputTokens:       a.inputTokens,
		OutputTokens:      a.outputTokens,
		EstimatedSpendUSD: a.spendUSD,
		LiveRuns:          a.liveRuns,
		Since:             a.since,
	}
}

// Global is the process-wide accumulator fed by every live OpenAI call made
// through internal/openaimodel's shared transport (used by the Luna, Terra,
// and Sol clients). Composition roots and the API server read this same
// instance by default. Tests should prefer NewAccumulator to avoid coupling
// to process-wide state.
var Global = NewAccumulator(time.Now().UTC())
