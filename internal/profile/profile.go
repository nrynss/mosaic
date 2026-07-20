// Package profile defines Mosaic's internal domain-composition boundary.
package profile

import (
	"context"

	"mosaic.local/mosaic/internal/api"
	"mosaic.local/mosaic/internal/store"
)

// Profile supplies one domain-specific implementation to the reusable local
// host. It is intentionally internal: Mosaic does not provide a public SDK or
// dynamic plugin loader in this demo.
type Profile interface {
	ID() string
	Identities() Identities
	Validate(assetRoot string) error
	Compose(context.Context, *store.Store, string) (Runtime, error)
}

// Identities carries the demo actor identity tokens a profile composes into the
// generic public actor resolver. The reusable host holds no identity literal of
// its own; the selected profile provides these values at composition.
type Identities struct {
	Viewer     string
	Supervisor string
}

// Runtime exposes only the domain services needed by the generic executable
// host: deterministic startup/recovery and bounded state-fact evidence.
type Runtime interface {
	api.RecoveryReader
	api.EvidenceResolver
	Run(context.Context) error
}
