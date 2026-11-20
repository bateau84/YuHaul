package queue

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/bateau84/YuHaul/internal/metadata"
	"github.com/stretchr/testify/require"
)

type fakeDisplayReader struct {
	values map[string]metadata.LocalDisplay
	err    error
	calls  [][]string
}

func (f *fakeDisplayReader) ReadLocalDisplay(_ context.Context, ids []string) (map[string]metadata.LocalDisplay, error) {
	f.calls = append(f.calls, append([]string(nil), ids...))
	if f.err != nil {
		return nil, f.err
	}
	out := map[string]metadata.LocalDisplay{}
	for _, id := range ids {
		if v, ok := f.values[id]; ok {
			out[id] = v
		}
	}
	return out, nil
}

func titleOf(s string, availability string) metadata.LocalDisplay {
	return metadata.LocalDisplay{Title: &s, TitleAvailability: availability}
}

func getList(t *testing.T, h http.Handler) (*httptest.ResponseRecorder, map[string]any) {
	t.Helper()
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	return rec, decodeMap(t, rec.Body.Bytes())
}

func TestListTitlesOnlyCurrentLocalAndNoStore(t *testing.T) {
	s := newTestStore(t)
	run := seedRun(t, s.db)
	held := seedVideo(t, s.db, run, "held")
	expired := seedVideo(t, s.db, run, "expired")
	none := seedVideo(t, s.db, run, "none")
	for _, v := range []int64{held, expired, none} {
		exec(t, s.db, `INSERT INTO error_entry(video_id,run_id,error_category,error_message,status) VALUES(?,?,'runtime','m','active')`, v, run)
		seedPendingDeferred(t, s, v, run)
	}
	reader := &fakeDisplayReader{values: map[string]metadata.LocalDisplay{
		"held":    titleOf("A held title", "current_local"),
		"expired": titleOf("must not show", "known_expired"),
	}}
	svc := NewService(s, nil).WithLocalDisplayReader(reader)

	for name, h := range map[string]http.Handler{"errors": svc.ErrorHandler(), "deferred": svc.DeferredHandler()} {
		t.Run(name, func(t *testing.T) {
			rec, body := getList(t, h)
			require.Equal(t, "no-store", rec.Header().Get("Cache-Control"))
			key := map[string]string{"errors": "errors", "deferred": "deferred"}[name]
			got := map[string]any{}
			for _, raw := range body[key].([]any) {
				row := raw.(map[string]any)
				got[row["external_video_id"].(string)] = row["video_title"]
			}
			require.Equal(t, "A held title", got["held"])
			require.Nil(t, got["expired"], "a title that is not current_local is never sent")
			require.Nil(t, got["none"])
			require.NotContains(t, rec.Body.String(), "must not show")
		})
	}
}

func TestListTitlesReadInChunksOfAtMost100(t *testing.T) {
	s := newTestStore(t)
	run := seedRun(t, s.db)
	reader := &fakeDisplayReader{values: map[string]metadata.LocalDisplay{}}
	for i := range 150 {
		id := fmt.Sprintf("v%03d", i)
		reader.values[id] = titleOf("T"+id, "current_local")
		v := seedVideo(t, s.db, run, id)
		exec(t, s.db, `INSERT INTO error_entry(video_id,run_id,error_category,error_message,status) VALUES(?,?,'runtime','m','active')`, v, run)
	}
	_, body := getList(t, NewService(s, nil).WithLocalDisplayReader(reader).ErrorHandler())
	require.Len(t, reader.calls, 2)
	for _, c := range reader.calls {
		require.LessOrEqual(t, len(c), 100)
	}
	titled := 0
	for _, raw := range body["errors"].([]any) {
		if raw.(map[string]any)["video_title"] != nil {
			titled++
		}
	}
	require.Equal(t, 150, titled)
}

func TestListTitlesReadFailureStillReturnsList(t *testing.T) {
	s := newTestStore(t)
	run := seedRun(t, s.db)
	v := seedVideo(t, s.db, run, "x")
	exec(t, s.db, `INSERT INTO error_entry(video_id,run_id,error_category,error_message,status) VALUES(?,?,'runtime','m','active')`, v, run)
	seedPendingDeferred(t, s, v, run)
	svc := NewService(s, nil).WithLocalDisplayReader(&fakeDisplayReader{err: errors.New("db locked")})
	for name, h := range map[string]http.Handler{"errors": svc.ErrorHandler(), "deferred": svc.DeferredHandler()} {
		t.Run(name, func(t *testing.T) {
			rec, body := getList(t, h)
			require.Equal(t, "no-store", rec.Header().Get("Cache-Control"))
			require.Len(t, body[name].([]any), 1)
			require.NotContains(t, rec.Body.String(), "video_title")
		})
	}
}

func TestListWithoutReaderOmitsTitles(t *testing.T) {
	s := newTestStore(t)
	run := seedRun(t, s.db)
	v := seedVideo(t, s.db, run, "x")
	exec(t, s.db, `INSERT INTO error_entry(video_id,run_id,error_category,error_message,status) VALUES(?,?,'runtime','m','active')`, v, run)
	rec, _ := getList(t, NewService(s, nil).ErrorHandler())
	require.False(t, strings.Contains(rec.Body.String(), "video_title"))
}

func decodeMap(t *testing.T, b []byte) map[string]any {
	t.Helper()
	var m map[string]any
	require.NoError(t, json.Unmarshal(b, &m))
	return m
}

func seedPendingDeferred(t *testing.T, s *Store, video, run int64) {
	t.Helper()
	_, err := s.CreateDeferredItem(context.Background(), &DeferredItem{VideoID: video, RunID: run, Reason: DeferredReasonQuotaExhausted, Status: DeferredStatusPending, CreatedAt: time.Now().UTC()})
	require.NoError(t, err)
}
