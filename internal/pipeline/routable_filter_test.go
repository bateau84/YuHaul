package pipeline

import (
	"context"
	"testing"

	"github.com/bateau84/YuHaul/internal/state"
	"github.com/stretchr/testify/require"
)

// TestRoutableFilterAudit audits withoutUnroutableVideos for the final
// outcomes (run-initiation ARS-005 test 33): its run-membership 'deferred'
// clause is deferred-specific and must not let a final unavailable_on_youtube
// video, or any other final video, back into the main loop.
func TestRoutableFilterAudit(t *testing.T) {
	ctx := context.Background()
	f := newGuardedProductionFixture(t)
	defer f.db.Close()
	p := &Pipeline{db: f.db, stateStore: f.state, logger: discardLogger()}
	machine := state.NewMachine(f.state)

	keep := func(id int64) bool {
		t.Helper()
		v, err := f.state.GetVideo(ctx, id)
		require.NoError(t, err)
		got, err := p.withoutUnroutableVideos(ctx, f.runID, []*state.Video{v})
		require.NoError(t, err)
		return len(got) == 1
	}
	// A native, undecided video stays routable (the fixture's video has a
	// matched decision but no outcome yet).
	require.True(t, keep(f.videoID), "native input of the run")

	for _, outcome := range []string{"accepted", "rejected", "excluded", "unavailable_on_youtube"} {
		v, err := machine.DiscoverVideo(ctx, f.runID, "final-"+outcome, "fixture-channel")
		require.NoError(t, err)
		_, err = f.db.ExecContext(ctx, `UPDATE video SET processing_state='completed', final_outcome=?, completed_at=CURRENT_TIMESTAMP WHERE id=?`, outcome, v.ID)
		require.NoError(t, err)
		require.False(t, keep(v.ID), "final %s is never routed again", outcome)
	}

	// The run's own committed deferred outcome keeps its native input out too,
	// even with no queue row left.
	_, err := f.db.ExecContext(ctx, `UPDATE run_video_outcome SET final_outcome='deferred', deferred_reason='quota_exhausted' WHERE run_id=? AND video_id=?`, f.runID, f.videoID)
	require.NoError(t, err)
	require.False(t, keep(f.videoID), "committed deferred outcome of the run's own native input")
}
