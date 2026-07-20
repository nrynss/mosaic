package openaimodel

import (
	"fmt"
	"reflect"
	"strings"

	"mosaic.local/mosaic/internal/contracts"
	"mosaic.local/mosaic/internal/sol"
	"mosaic.local/mosaic/internal/terra"
)

// Agent keys accepted by AgentProviderSelection.
const (
	AgentLuna  = "luna"
	AgentTerra = "terra"
	AgentSol   = "sol"
)

// Clients is the per-agent structured client set selected for a process.
type Clients struct {
	Luna  LunaStructuredClient
	Terra terra.StructuredClient
	Sol   sol.StructuredClient
}

// SelectConfig routes each agent to a live or fixture structured client.
// Live is used only when selection[agent] == ProviderLive and APIKey is non-empty.
// Fixture clients are required whenever that agent falls back to fixture.
// Pre-built Live* clients are required when that agent is selected live with a key.
type SelectConfig struct {
	Selection contracts.AgentProviderSelection
	APIKey    string

	LiveLuna  LunaStructuredClient
	LiveTerra terra.StructuredClient
	LiveSol   sol.StructuredClient

	FixtureLuna  LunaStructuredClient
	FixtureTerra terra.StructuredClient
	FixtureSol   sol.StructuredClient
}

// Select chooses live vs fixture clients per agent. A nil Selection defaults
// every agent to fixture. Missing fixtures when fallback is required are errors.
func Select(cfg SelectConfig) (Clients, error) {
	selection := cfg.Selection
	if selection == nil {
		selection = contracts.AgentProviderSelection{}
	}
	apiKey := strings.TrimSpace(cfg.APIKey)

	luna, err := pickClient(AgentLuna, selection, apiKey, cfg.LiveLuna, cfg.FixtureLuna)
	if err != nil {
		return Clients{}, err
	}
	terraClient, err := pickClient(AgentTerra, selection, apiKey, cfg.LiveTerra, cfg.FixtureTerra)
	if err != nil {
		return Clients{}, err
	}
	solClient, err := pickClient(AgentSol, selection, apiKey, cfg.LiveSol, cfg.FixtureSol)
	if err != nil {
		return Clients{}, err
	}
	return Clients{
		Luna:  luna,
		Terra: terraClient,
		Sol:   solClient,
	}, nil
}

func pickClient[T any](agent string, selection contracts.AgentProviderSelection, apiKey string, live, fixture T) (T, error) {
	var zero T
	useLive := selection[agent] == contracts.ProviderLive && apiKey != ""
	if useLive {
		if isNilClient(live) {
			return zero, fmt.Errorf("openaimodel select: live %s client is required when provider is live", agent)
		}
		return live, nil
	}
	if isNilClient(fixture) {
		return zero, fmt.Errorf("openaimodel select: fixture %s client is required", agent)
	}
	return fixture, nil
}

func isNilClient[T any](value T) bool {
	v := reflect.ValueOf(value)
	if !v.IsValid() {
		return true
	}
	switch v.Kind() {
	case reflect.Chan, reflect.Func, reflect.Interface, reflect.Map, reflect.Pointer, reflect.Slice:
		return v.IsNil()
	default:
		return false
	}
}
