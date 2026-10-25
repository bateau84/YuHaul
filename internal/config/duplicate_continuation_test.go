package config

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestDuplicateContinuationCompatibility(t *testing.T) {
	for _, legacy := range []string{"absent", "skip", "flag", "", "arbitrary"} {
		t.Run(legacy, func(t *testing.T) {
			ctx := context.Background()
			db, _, routes, history, svc, cleanup := setupConfigTest(t)
			defer cleanup()
			_, err := db.ExecContext(ctx, `DELETE FROM settings WHERE key = 'duplicate_behavior'`)
			require.NoError(t, err)
			if legacy != "absent" {
				require.NoError(t, history.SetSetting(ctx, "duplicate_behavior", legacy))
			}
			// Recreate the effective service: upgrade/restart must not require a save.
			svc = NewService(routes, history)
			h := NewHandler(svc, nil).Handler()
			for _, path := range []string{"/api/settings", "/api/settings/duplicate-threshold"} {
				rec := httptest.NewRecorder()
				h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
				require.Equal(t, http.StatusOK, rec.Code)
				require.Contains(t, rec.Body.String(), `"duplicate_behavior":"continue"`)
			}
			versions, err := history.List(ctx)
			require.NoError(t, err)
			for _, input := range []string{`"skip"`, `"flag"`, `""`, `"arbitrary"`, `null`, "omitted"} {
				body := `{}`
				if input != "omitted" {
					body = `{"duplicate_behavior":` + input + `}`
				}
				rec := httptest.NewRecorder()
				h.ServeHTTP(rec, httptest.NewRequest(http.MethodPut, "/api/settings", strings.NewReader(body)))
				require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
				require.Contains(t, rec.Body.String(), `"duplicate_behavior":"continue"`)
			}
			require.NoError(t, svc.SetDuplicateBehavior(ctx, "skip"))
			after, err := history.List(ctx)
			require.NoError(t, err)
			require.Equal(t, versions, after, "compatibility-only writes create no history")
			var count int
			require.NoError(t, db.QueryRowContext(ctx, `SELECT COUNT(*) FROM settings WHERE key = 'duplicate_behavior'`).Scan(&count))
			if legacy == "absent" {
				require.Zero(t, count)
			} else {
				require.Equal(t, 1, count)
				raw, err := history.GetSetting(ctx, "duplicate_behavior")
				require.NoError(t, err)
				require.Equal(t, legacy, raw)
			}
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, httptest.NewRequest(http.MethodPut, "/api/settings", strings.NewReader(`{"duplicate_threshold":0.8,"duplicate_behavior":"skip"}`)))
			require.Equal(t, http.StatusOK, rec.Code)
			id, err := svc.ActiveVersionID(ctx)
			require.NoError(t, err)
			cv, err := history.Get(ctx, *id)
			require.NoError(t, err)
			require.Equal(t, "continue", cv.Snapshot.DuplicateBehavior)
			require.Equal(t, 0.8, cv.Snapshot.DuplicateThreshold)
			for _, body := range []string{`{"duplicate_behavior":1}`, `{"duplicate_behavior":{}}`, `{"duplicate_behavior":false}`, `{"duplicate_threshold":"bad","duplicate_behavior":"skip"}`, `{`} {
				rec := httptest.NewRecorder()
				h.ServeHTTP(rec, httptest.NewRequest(http.MethodPut, "/api/settings", strings.NewReader(body)))
				require.Equal(t, http.StatusBadRequest, rec.Code, body)
			}
		})
	}
}

func TestDuplicateContinuationPreservesRecordedHistory(t *testing.T) {
	ctx := context.Background()
	db, _, _, _, svc, cleanup := setupConfigTest(t)
	defer cleanup()
	h := NewHandler(svc, nil).Handler()
	for _, recorded := range []string{`"skip"`, `"flag"`, `""`, "absent"} {
		t.Run(recorded, func(t *testing.T) {
			raw := `{"routes":[],"duplicate_threshold":0.5`
			want := ""
			if recorded != "absent" {
				raw += `,"duplicate_behavior":` + recorded
				require.NoError(t, json.Unmarshal([]byte(recorded), &want))
			}
			raw += `}`
			res, err := db.ExecContext(ctx, `INSERT INTO config_version (snapshot, change_description) VALUES (?, 'legacy')`, raw)
			require.NoError(t, err)
			id, err := res.LastInsertId()
			require.NoError(t, err)
			before := rawSnapshot(t, db, id)
			for _, path := range []string{"/api/config/versions", "/api/config/versions/" + strconv.FormatInt(id, 10)} {
				rec := httptest.NewRecorder()
				h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
				require.Equal(t, http.StatusOK, rec.Code)
				require.Contains(t, rec.Body.String(), `"duplicate_behavior":`+strconv.Quote(want))
			}
			copy, err := svc.Restore(ctx, id, "historical copy")
			require.NoError(t, err)
			require.Equal(t, want, copy.Snapshot.DuplicateBehavior)
			require.NoError(t, svc.SetDuplicateBehavior(ctx, "flag"))
			current, err := svc.ActiveVersionID(ctx)
			require.NoError(t, err)
			diff, err := svc.Diff(ctx, id, *current)
			require.NoError(t, err)
			require.Equal(t, Change{Old: want, New: "continue"}, diff.SettingsChanged["duplicate_behavior"])
			require.Equal(t, before, rawSnapshot(t, db, id), "history source bytes remain immutable")
		})
	}
}

func TestDuplicateContinuationCanonicalSavedSelectionAfterRestart(t *testing.T) {
	ctx := context.Background()
	db, _, routes, history, _, cleanup := setupConfigTest(t)
	defer cleanup()
	require.NoError(t, history.SetSetting(ctx, "duplicate_behavior", "skip"))
	oldID, err := history.Create(ctx, Snapshot{Routes: []SnapshotRoute{}, DuplicateThreshold: .5, DuplicateBehavior: "skip"}, "old saved")
	require.NoError(t, err)
	oldBytes := rawSnapshot(t, db, oldID)
	proposalID, err := history.Create(ctx, Snapshot{Routes: []SnapshotRoute{}, DuplicateThreshold: .99, DuplicateBehavior: "skip"}, "old proposal")
	require.NoError(t, err)
	svc := NewService(routes, history)
	id, err := svc.ActiveVersionID(ctx)
	require.NoError(t, err)
	require.NotEqual(t, oldID, *id)
	require.NotEqual(t, proposalID, *id, "latest history is not effective saved configuration")
	cv, err := history.Get(ctx, *id)
	require.NoError(t, err)
	require.Equal(t, .5, cv.Snapshot.DuplicateThreshold)
	require.Equal(t, "continue", cv.Snapshot.DuplicateBehavior)
	again, err := NewService(routes, history).ActiveVersionID(ctx)
	require.NoError(t, err)
	require.Equal(t, id, again, "canonical saved snapshot uses exact serialized matching")
	require.Equal(t, oldBytes, rawSnapshot(t, db, oldID))
}
