package simulation

import (
	"os"
	"strconv"
	"strings"
	"time"
)

// Env vars for interactive simulation presentation pacing. Framework packages
// never read these; only simulation (and composition that wires it) does.
const (
	// EnvBeatSpacing is the env key for equal inter-beat spacing.
	// Value may be a Go duration ("2.5s", "2500ms") or an integer number of
	// milliseconds ("2500"). Empty/unset → DefaultBeatSpacing.
	EnvBeatSpacing = "MOSAIC_SIM_BEAT_SPACING"

	// EnvBurst enables zero-delay append of all scheduled beats (EventLog
	// stress). Accepted truthy values: 1, true, yes, on (case-insensitive).
	EnvBurst = "MOSAIC_SIM_BURST"
)

// DefaultBeatSpacing is the interactive demo spacing between successive beats
// (~2.5s). Fixture scenario delay_ms values are presentation metadata only and
// are ignored under equal-spacing mode (see BeatExecutor pacing rules).
const DefaultBeatSpacing = 2500 * time.Millisecond

// BeatSpacingFromEnv reads MOSAIC_SIM_BEAT_SPACING. Unset/empty/invalid values
// yield DefaultBeatSpacing. A non-positive parsed duration also falls back to
// the default so composition never silently collapses to zero spacing.
func BeatSpacingFromEnv() time.Duration {
	return ParseBeatSpacing(os.Getenv(EnvBeatSpacing))
}

// ParseBeatSpacing parses a spacing config string. Empty or invalid input
// returns DefaultBeatSpacing. Accepts Go durations or integer milliseconds.
func ParseBeatSpacing(raw string) time.Duration {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return DefaultBeatSpacing
	}
	if d, err := time.ParseDuration(raw); err == nil {
		if d <= 0 {
			return DefaultBeatSpacing
		}
		return d
	}
	// Integer milliseconds (e.g. "2500").
	if ms, err := strconv.ParseInt(raw, 10, 64); err == nil {
		if ms <= 0 {
			return DefaultBeatSpacing
		}
		return time.Duration(ms) * time.Millisecond
	}
	return DefaultBeatSpacing
}

// BurstFromEnv reports whether MOSAIC_SIM_BURST is truthy.
func BurstFromEnv() bool {
	return ParseBurst(os.Getenv(EnvBurst))
}

// ParseBurst interprets a burst flag string.
func ParseBurst(raw string) bool {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "1", "true", "yes", "on":
		return true
	default:
		return false
	}
}

// EqualSpacingDelay returns the wait from run start for the beat at sorted
// index i (0-based) under equal-spacing pacing: i * spacing.
//
// Index 0 fires immediately (delay 0). Negative spacing is treated as zero.
func EqualSpacingDelay(index int, spacing time.Duration) time.Duration {
	if index <= 0 || spacing <= 0 {
		return 0
	}
	return time.Duration(index) * spacing
}
