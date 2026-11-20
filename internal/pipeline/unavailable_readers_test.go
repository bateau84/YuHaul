package pipeline

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bateau84/YuHaul/internal/state"
	"github.com/bateau84/YuHaul/internal/store/storetest"
)

// TestO2UnavailableOnYouTubeEventPayloadsCarryCounter: run.completed payloads
// expose videos_unavailable under its own key.
func TestO2UnavailableOnYouTubeEventPayloadsCarryCounter(t *testing.T) {
	raw, err := json.Marshal(completedPayload{VideosUnavailable: 2, VideosRejected: 1, VideosFailed: 1})
	require.NoError(t, err)
	var m map[string]any
	require.NoError(t, json.Unmarshal(raw, &m))
	assert.EqualValues(t, 2, m["videos_unavailable"])
	assert.EqualValues(t, 1, m["videos_rejected"])
	assert.EqualValues(t, 1, m["videos_failed"])
}

// TestO2UnfinishedRunMembersTreatsUnavailableAsFinished: a stop, discard or
// failure of the receiving run must never re-queue a video whose run outcome
// is unavailable_on_youtube (ARS-005 §6.4), while an unfinished one still is.
func TestO2UnfinishedRunMembersTreatsUnavailableAsFinished(t *testing.T) {
	ctx := context.Background()
	db := storetest.Open(t)
	st, err := state.NewStore(db)
	require.NoError(t, err)
	runID, err := st.CreateRun(ctx, &state.Run{StartedAt: time.Now().UTC(), RunType: state.RunTypeManual, Status: state.RunStatusRunning})
	require.NoError(t, err)

	ids := map[string]int64{}
	for _, name := range []string{"done", "open"} {
		id, err := st.CreateVideo(ctx, &state.Video{VideoID: name, ChannelID: "c", DiscoveredAt: time.Now().UTC(),
			DiscoveredInRunID: runID, ProcessingState: state.VideoStateEvaluated, FinalOutcome: state.FinalOutcomeNotYetEvaluated})
		require.NoError(t, err)
		ids[name] = id
	}
	_, err = db.Exec(`INSERT INTO run_video_outcome(run_id,video_id,final_outcome,updated_at) VALUES (?,?,?,?)`,
		runID, ids["done"], string(state.FinalOutcomeUnavailableOnYouTube), time.Now().UTC().Format(time.RFC3339Nano))
	require.NoError(t, err)

	tx, err := db.BeginTx(ctx, nil)
	require.NoError(t, err)
	defer tx.Rollback()
	members, err := unfinishedRunMembersTx(ctx, tx, runID)
	require.NoError(t, err)
	require.Len(t, members, 1)
	assert.Equal(t, ids["open"], members[0].videoID)
}
