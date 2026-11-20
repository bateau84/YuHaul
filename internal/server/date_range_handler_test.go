package server

import (
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bateau84/YuHaul/internal/pipeline"
	"github.com/bateau84/YuHaul/internal/state"
)

func rangeTestServer(t *testing.T) (*Server, *fakePipeline) {
	t.Helper()
	fp := newFakePipeline(&state.Run{ID: 5, StartedAt: time.Now().UTC(), RunType: state.RunTypeBacktest, Status: state.RunStatusRunning})
	srv := NewServer(fp, pipeline.NewBroadcaster(), WithServerLogger(slog.New(slog.NewTextHandler(io.Discard, nil))))
	return srv, fp
}

func post(srv *Server, target, body string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodPost, target, strings.NewReader(body))
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	return rec
}

func str(s string) *string { return &s }

// D5: every invalid range answers 400 invalid_date_range and reaches neither
// Backtest nor Replay, so no run, snapshot or config lookup can happen.
func TestHandleDateRange_InvalidRangeRejectedBeforeAnyRun(t *testing.T) {
	cases := []struct {
		name, body, wantMsg string
		wantField           any
	}{
		{"reversed", `{"published_from":"2026-11-02","published_to":"2026-11-01"}`,
			"invalid date range: the start day must not be after the end day", nil},
		{"impossible day", `{"published_to":"2026-02-30"}`,
			"invalid date range: published_to must be a valid calendar date (YYYY-MM-DD)", "published_to"},
		{"non canonical", `{"published_from":"2026-1-05"}`,
			"invalid date range: published_from must be a valid calendar date (YYYY-MM-DD)", "published_from"},
		{"number", `{"published_from":20261001}`,
			"invalid date range: published_from must be a valid calendar date (YYYY-MM-DD)", "published_from"},
		{"object", `{"published_to":{}}`,
			"invalid date range: published_to must be a valid calendar date (YYYY-MM-DD)", "published_to"},
		{"empty string", `{"published_from":""}`,
			"invalid date range: published_from must be a valid calendar date (YYYY-MM-DD)", "published_from"},
	}
	for _, target := range []string{"/api/backtest", "/api/runs/replay"} {
		for _, c := range cases {
			t.Run(target+" "+c.name, func(t *testing.T) {
				srv, fp := rangeTestServer(t)
				rec := post(srv, target, c.body)

				require.Equal(t, http.StatusBadRequest, rec.Code)
				var got map[string]any
				require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &got))
				assert.Equal(t, "invalid_date_range", got["code"])
				assert.Equal(t, c.wantMsg, got["error"])
				assert.Equal(t, c.wantField, got["field"])
				assert.Zero(t, fp.backtestCalls)
				assert.Zero(t, fp.replayCalls)
			})
		}
	}
}

func TestHandleReplay_RangeInQueryIsRejectedNotIgnored(t *testing.T) {
	for _, q := range []string{"published_from=2026-10-01", "published_to=2026-10-31", "config_version_id=3&published_from=2026-10-01"} {
		srv, fp := rangeTestServer(t)
		rec := post(srv, "/api/runs/replay?"+q, `{"published_from":"2026-10-01"}`)

		require.Equal(t, http.StatusBadRequest, rec.Code, q)
		assert.Contains(t, rec.Body.String(), `"code":"invalid_date_range"`)
		assert.Contains(t, rec.Body.String(), "send published_from/published_to in the request body")
		assert.Zero(t, fp.replayCalls)
	}
}

// Valid ranges, absent members and explicit nulls reach the pipeline exactly as sent.
func TestHandleDateRange_ValidRangeReachesPipelineUnchanged(t *testing.T) {
	for _, target := range []string{"/api/backtest", "/api/runs/replay"} {
		t.Run(target, func(t *testing.T) {
			cases := []struct {
				body     string
				from, to *string
			}{
				{`{"published_from":"2026-10-01","published_to":"2026-10-31"}`, str("2026-10-01"), str("2026-10-31")},
				{`{"published_from":"2026-10-01","published_to":null}`, str("2026-10-01"), nil},
				{`{"published_to":"2026-10-31"}`, nil, str("2026-10-31")},
				{`{}`, nil, nil},
			}
			for _, c := range cases {
				srv, fp := rangeTestServer(t)
				rec := post(srv, target, c.body)
				require.Equal(t, http.StatusAccepted, rec.Code, c.body)
				got := fp.backtestRange
				if target == "/api/runs/replay" {
					got = fp.replayRange
				}
				assert.Equal(t, state.PublishedRange{From: c.from, To: c.to}, got, c.body)
			}
		})
	}
}

// D8 (handler half): the range is read from the body whether the config
// version comes from the query or from the body.
func TestHandleReplay_RangeWithConfigVersionInQueryAndBody(t *testing.T) {
	srv, fp := rangeTestServer(t)
	rec := post(srv, "/api/runs/replay?config_version_id=9", `{"published_from":"2026-10-01","published_to":"2026-10-31"}`)
	require.Equal(t, http.StatusAccepted, rec.Code)
	require.NotNil(t, fp.replayConfigID)
	assert.Equal(t, int64(9), *fp.replayConfigID)
	assert.Equal(t, state.PublishedRange{From: str("2026-10-01"), To: str("2026-10-31")}, fp.replayRange)

	srv, fp = rangeTestServer(t)
	rec = post(srv, "/api/runs/replay", `{"config_version_id":7,"published_from":"2026-10-01","published_to":"2026-10-31"}`)
	require.Equal(t, http.StatusAccepted, rec.Code)
	require.NotNil(t, fp.replayConfigID)
	assert.Equal(t, int64(7), *fp.replayConfigID)
	assert.Equal(t, state.PublishedRange{From: str("2026-10-01"), To: str("2026-10-31")}, fp.replayRange)
}
