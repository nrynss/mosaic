// Package registry contains the internal profiles bundled with this demo.
package registry

import (
	"fmt"

	"mosaic.local/mosaic/internal/profile"
	"mosaic.local/mosaic/internal/reference/domesticdisturbance"
)

// DefaultID identifies the reference profile composed by default.
const DefaultID = domesticdisturbance.ID

// Resolve selects one bundled profile. Runtime loading of arbitrary packages is
// intentionally out of scope for the local demo.
func Resolve(id string) (profile.Profile, error) {
	switch id {
	case DefaultID:
		return domesticdisturbance.New(), nil
	default:
		return nil, fmt.Errorf("unknown Mosaic domain profile %q", id)
	}
}
