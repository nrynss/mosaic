package cassette

import "fmt"

// Mode selects how a cassette decorator handles StructuredClient calls.
type Mode int

const (
	// ModePassthrough calls the inner client only and never reads or writes
	// recordings. Fixture wiring can wrap clients in this mode or skip the
	// decorator entirely.
	ModePassthrough Mode = iota

	// ModeRecord calls the inner client, persists the response under the
	// request key, then returns the live response.
	ModeRecord

	// ModeReplay returns a previously recorded response for the request key
	// without calling the inner client. A missing entry is an error.
	ModeReplay
)

// String returns a stable lowercase name for config and logs.
func (m Mode) String() string {
	switch m {
	case ModePassthrough:
		return "passthrough"
	case ModeRecord:
		return "record"
	case ModeReplay:
		return "replay"
	default:
		return fmt.Sprintf("mode(%d)", int(m))
	}
}

// ParseMode accepts passthrough|off, record|live, and replay|recorded.
func ParseMode(s string) (Mode, error) {
	switch s {
	case "", "passthrough", "off", "fixture":
		return ModePassthrough, nil
	case "record", "live":
		return ModeRecord, nil
	case "replay", "recorded":
		return ModeReplay, nil
	default:
		return 0, fmt.Errorf("unknown cassette mode %q (want passthrough, record, or replay)", s)
	}
}
