package pipeline

import (
	"context"
	"fmt"

	"github.com/bateau84/YuHaul/internal/duplicate"
)

// Resolve once per evaluation, from the immutable configuration selected at
// run initiation rather than settings that may have changed since then.
// Recorded legacy behavior strings are deliberately not consulted: all runs
// continue through eligible routes after a distinct match.
func (p *Pipeline) detectorForRun(ctx context.Context, runID int64) (*duplicate.Detector, error) {
	if p.detector == nil {
		return nil, nil
	}
	run, err := p.stateStore.GetRun(ctx, runID)
	if err != nil {
		return nil, fmt.Errorf("load duplicate configuration run %d: %w", runID, err)
	}
	// Historical simulation callers retain their existing detector fallback;
	// production never executes without its captured configuration.
	if run.ConfigVersionID == nil {
		if p.isProductionRun(run.RunType) {
			return nil, fmt.Errorf("run %d missing captured duplicate configuration", runID)
		}
		return p.detector, nil
	}
	if p.configStore == nil {
		return nil, fmt.Errorf("duplicate config version %d requires a config store", *run.ConfigVersionID)
	}
	cv, err := p.configStore.Get(ctx, *run.ConfigVersionID)
	if err != nil {
		return nil, fmt.Errorf("load duplicate config version %d: %w", *run.ConfigVersionID, err)
	}
	return p.detector.WithThreshold(cv.Snapshot.DuplicateThreshold), nil
}
