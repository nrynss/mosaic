package democast

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"mosaic.local/mosaic/internal/ontology/gen"
	"mosaic.local/mosaic/internal/reference/domesticdisturbance/simulator"
)

// RawEventIndex maps raw_event_id → exact dataset RawEvent (fields verbatim).
type RawEventIndex map[string]gen.RawEvent

// LoadRawEvents loads datasets/<scenario>/raw-events.json under assetRoot and
// indexes by raw_event_id. Fields are taken from the dataset without re-encoding
// payloads by hand so Luna request fingerprints stay stable.
func LoadRawEvents(assetRoot, scenario string) (RawEventIndex, error) {
	scenario = strings.TrimSpace(scenario)
	if scenario == "" {
		scenario = simulator.DomesticDisturbance
	}
	path := filepath.Join(assetRoot, "datasets", scenario, "raw-events.json")
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read raw events %s: %w", path, err)
	}
	var file struct {
		RawEvents []gen.RawEvent `json:"raw_events"`
	}
	if err := json.Unmarshal(data, &file); err != nil {
		return nil, fmt.Errorf("decode raw events %s: %w", path, err)
	}
	if len(file.RawEvents) == 0 {
		return nil, fmt.Errorf("raw events file %s has no events", path)
	}
	out := make(RawEventIndex, len(file.RawEvents))
	for _, ev := range file.RawEvents {
		id := strings.TrimSpace(ev.RawEventID)
		if id == "" {
			return nil, fmt.Errorf("raw event missing raw_event_id in %s", path)
		}
		if _, dup := out[id]; dup {
			return nil, fmt.Errorf("duplicate raw_event_id %q in %s", id, path)
		}
		out[id] = ev
	}
	return out, nil
}

// Get returns the dataset raw event for id.
func (idx RawEventIndex) Get(id string) (gen.RawEvent, error) {
	ev, ok := idx[strings.TrimSpace(id)]
	if !ok {
		return gen.RawEvent{}, fmt.Errorf("raw event %q not found in dataset", id)
	}
	return ev, nil
}
