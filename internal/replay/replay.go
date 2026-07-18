// Package replay provides deterministic checkpoint recovery over the stable
// P02 projector and P03 repository contracts.
package replay

import (
	"context"
	"fmt"
	"strings"

	"mosaic.local/mosaic/internal/contracts"
	"mosaic.local/mosaic/internal/ontology/gen"
)

// Runner loads the most recent durable checkpoint and replays only later
// canonical records. It performs no persistence and therefore is safe for
// startup verification and read-only recovery checks.
type Runner struct {
	Canonical   contracts.CanonicalEventRepository
	Checkpoints contracts.CheckpointRepository
	Projector   contracts.Projector
}

// Recover returns a deterministic projection from the most recent checkpoint.
// Before any successful projection it starts at sequence zero.
func (r Runner) Recover(ctx context.Context) (contracts.ProjectionResult, error) {
	if r.Canonical == nil || r.Checkpoints == nil || r.Projector == nil {
		return contracts.ProjectionResult{}, fmt.Errorf("canonical repository, checkpoint repository, and projector are required")
	}
	checkpoint, err := r.Checkpoints.LatestCheckpoint(ctx)
	if err != nil {
		if isMissingCheckpoint(err) {
			events, listErr := r.Canonical.ListCanonicalEventsAfter(ctx, 0)
			if listErr != nil {
				return contracts.ProjectionResult{}, fmt.Errorf("list canonical events: %w", listErr)
			}
			return r.Projector.Replay(ctx, gen.Checkpoint{}, events)
		}
		return contracts.ProjectionResult{}, fmt.Errorf("load latest checkpoint: %w", err)
	}
	events, err := r.Canonical.ListCanonicalEventsAfter(ctx, checkpoint.ThroughCanonicalSeq)
	if err != nil {
		return contracts.ProjectionResult{}, fmt.Errorf("list canonical events after checkpoint: %w", err)
	}
	return r.Projector.Replay(ctx, checkpoint, events)
}

func isMissingCheckpoint(err error) bool {
	message := strings.ToLower(err.Error())
	return strings.Contains(message, "not found") && strings.Contains(message, "checkpoint")
}
