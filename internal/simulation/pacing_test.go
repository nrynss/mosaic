package simulation_test

import (
	"testing"
	"time"

	"mosaic.local/mosaic/internal/simulation"
)

func TestParseBeatSpacing(t *testing.T) {
	cases := []struct {
		in   string
		want time.Duration
	}{
		{"", simulation.DefaultBeatSpacing},
		{"  ", simulation.DefaultBeatSpacing},
		{"2.5s", 2500 * time.Millisecond},
		{"2500ms", 2500 * time.Millisecond},
		{"2500", 2500 * time.Millisecond},
		{"5s", 5 * time.Second},
		{"0", simulation.DefaultBeatSpacing},
		{"-1", simulation.DefaultBeatSpacing},
		{"0s", simulation.DefaultBeatSpacing},
		{"not-a-duration", simulation.DefaultBeatSpacing},
	}
	for _, tc := range cases {
		if got := simulation.ParseBeatSpacing(tc.in); got != tc.want {
			t.Errorf("ParseBeatSpacing(%q) = %v, want %v", tc.in, got, tc.want)
		}
	}
}

func TestParseBurst(t *testing.T) {
	truthy := []string{"1", "true", "TRUE", "yes", "on", " On "}
	for _, s := range truthy {
		if !simulation.ParseBurst(s) {
			t.Errorf("ParseBurst(%q) = false, want true", s)
		}
	}
	falsy := []string{"", "0", "false", "no", "off", "burst"}
	for _, s := range falsy {
		if simulation.ParseBurst(s) {
			t.Errorf("ParseBurst(%q) = true, want false", s)
		}
	}
}

func TestEqualSpacingDelay(t *testing.T) {
	spacing := 2500 * time.Millisecond
	if got := simulation.EqualSpacingDelay(0, spacing); got != 0 {
		t.Fatalf("index 0 = %v, want 0", got)
	}
	if got := simulation.EqualSpacingDelay(1, spacing); got != spacing {
		t.Fatalf("index 1 = %v, want %v", got, spacing)
	}
	if got := simulation.EqualSpacingDelay(3, spacing); got != 3*spacing {
		t.Fatalf("index 3 = %v, want %v", got, 3*spacing)
	}
	if got := simulation.EqualSpacingDelay(5, 0); got != 0 {
		t.Fatalf("zero spacing = %v, want 0", got)
	}
}

func TestDefaultBeatSpacingMatchesEnvDoc(t *testing.T) {
	if simulation.DefaultBeatSpacing != 2500*time.Millisecond {
		t.Fatalf("DefaultBeatSpacing = %v, want 2.5s", simulation.DefaultBeatSpacing)
	}
}
