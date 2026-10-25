package server

// B7 / ADR-012: dry-run drafts and backtest proposals carry public UUID route
// identities. Malformed, missing, numeric-alias, non-v4, and duplicate route
// IDs are rejected with 400 before any run starts; valid IDs reach the
// pipeline in canonical lowercase form for its single proposal-ingest
// transaction.

import (
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

const proposalUUID = "3f1d2c8e-9a67-4b52-8f0c-1d2e3f4a5b6c"

func proposalRouteJSON(idJSON string) string {
	return `{"id":` + idJSON + `,"name":"Music","evaluation_order":1,"is_final":false,"is_enabled":true,` +
		`"condition_composition":"AND",` +
		`"conditions":[{"condition_index":0,"field":"title","operator":"contains","value":"jazz"}],` +
		`"actions":[{"action_index":0,"action_type":"add_to_playlist","target_playlist_id":"PL1"}],"exclusions":[]}`
}

func newProposalServer(t *testing.T) (*fakePipeline, http.Handler) {
	t.Helper()
	run := &state.Run{StartedAt: time.Now().UTC(), RunType: state.RunTypeDryRun, Status: state.RunStatusRunning}
	fp := newFakePipeline(run)
	srv := NewServer(fp, pipeline.NewBroadcaster())
	return fp, srv.Handler()
}

func TestProposalUUID_InvalidRouteIDsRejected(t *testing.T) {
	tests := []struct {
		name string
		id   string
	}{
		{name: "numeric alias", id: `17`},
		{name: "malformed", id: `"not-a-uuid"`},
		{name: "null id", id: `null`},
		{name: "non-v4 version", id: `"00000000-0000-1000-8000-000000000000"`},
		{name: "non-RFC variant", id: `"00000000-0000-4000-0000-000000000000"`},
		{name: "bare hex", id: `"3f1d2c8e9a674b528f0c1d2e3f4a5b6c"`},
		{name: "braced", id: `"{3f1d2c8e-9a67-4b52-8f0c-1d2e3f4a5b6c}"`},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Run("dry-run", func(t *testing.T) {
				fp, h := newProposalServer(t)
				body := `{"draft_configuration":{"duplicate_threshold":0.5,"duplicate_behavior":"skip","routes":[` +
					proposalRouteJSON(tt.id) + `]}}`
				req := httptest.NewRequest(http.MethodPost, "/api/runs/dry-run", strings.NewReader(body))
				rec := httptest.NewRecorder()
				h.ServeHTTP(rec, req)

				assert.Equal(t, http.StatusBadRequest, rec.Code, rec.Body.String())
				assert.Contains(t, rec.Body.String(), "invalid route id")
				assert.Zero(t, fp.execute, "a rejected draft must not start a run")
				assert.Nil(t, fp.draft, "a rejected draft must not reach the pipeline")
			})

			t.Run("backtest", func(t *testing.T) {
				fp, h := newProposalServer(t)
				body := `{"proposed_configuration":{"duplicate_threshold":0.5,"duplicate_behavior":"skip","routes":[` +
					proposalRouteJSON(tt.id) + `]}}`
				req := httptest.NewRequest(http.MethodPost, "/api/backtest", strings.NewReader(body))
				rec := httptest.NewRecorder()
				h.ServeHTTP(rec, req)

				assert.Equal(t, http.StatusBadRequest, rec.Code, rec.Body.String())
				assert.Contains(t, rec.Body.String(), "invalid route id")
				assert.Zero(t, fp.execute, "a rejected proposal must not start a run")
				assert.Empty(t, fp.backtestProposed.Routes, "a rejected proposal must not reach the pipeline")
			})
		})
	}
}

// A duplicate UUID within one proposal is invalid input (ADR-012 §1), while
// duplicate display names with distinct UUIDs remain valid.
// A route entry with no id field is invalid input too (ADR-012 §1).
func TestProposalUUID_MissingIDRejected(t *testing.T) {
	const noIDRoute = `{"name":"Music","evaluation_order":1,"is_final":false,"is_enabled":true,` +
		`"condition_composition":"AND",` +
		`"conditions":[{"condition_index":0,"field":"title","operator":"contains","value":"jazz"}],` +
		`"actions":[{"action_index":0,"action_type":"add_to_playlist","target_playlist_id":"PL1"}],"exclusions":[]}`

	for _, tc := range []struct {
		name string
		body string
	}{
		{name: "dry-run", body: `{"draft_configuration":{"duplicate_threshold":0.5,"duplicate_behavior":"skip","routes":[` + noIDRoute + `]}}`},
		{name: "backtest", body: `{"proposed_configuration":{"duplicate_threshold":0.5,"duplicate_behavior":"skip","routes":[` + noIDRoute + `]}}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fp, h := newProposalServer(t)
			path := "/api/runs/dry-run"
			if tc.name == "backtest" {
				path = "/api/backtest"
			}
			req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(tc.body))
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, req)
			assert.Equal(t, http.StatusBadRequest, rec.Code, rec.Body.String())
			assert.Contains(t, rec.Body.String(), "invalid route id")
			assert.Zero(t, fp.execute)
		})
	}
}

func TestProposalUUID_DuplicateUUIDsRejectedNamesAllowed(t *testing.T) {
	duplicateUUIDs := `{"draft_configuration":{"duplicate_threshold":0.5,"duplicate_behavior":"skip","routes":[` +
		proposalRouteJSON(`"`+proposalUUID+`"`) + `,` + proposalRouteJSON(`"`+proposalUUID+`"`) + `]}}`
	fp, h := newProposalServer(t)
	req := httptest.NewRequest(http.MethodPost, "/api/runs/dry-run", strings.NewReader(duplicateUUIDs))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	assert.Equal(t, http.StatusBadRequest, rec.Code, rec.Body.String())
	assert.Contains(t, rec.Body.String(), "invalid route id")
	assert.Zero(t, fp.execute)
	assert.Nil(t, fp.draft)

	sameName := `{"proposed_configuration":{"duplicate_threshold":0.5,"duplicate_behavior":"skip","routes":[` +
		proposalRouteJSON(`"aaaaaaaa-1111-4aaa-8bbb-000000000001"`) + `,` +
		proposalRouteJSON(`"bbbbbbbb-2222-4bbb-8ccc-000000000002"`) + `]}}`
	fp2, h2 := newProposalServer(t)
	req2 := httptest.NewRequest(http.MethodPost, "/api/backtest", strings.NewReader(sameName))
	rec2 := httptest.NewRecorder()
	h2.ServeHTTP(rec2, req2)
	assert.Equal(t, http.StatusAccepted, rec2.Code, rec2.Body.String())
	require.Len(t, fp2.backtestProposed.Routes, 2)
	assert.Equal(t, "Music", fp2.backtestProposed.Routes[0].Name)
	assert.Equal(t, "Music", fp2.backtestProposed.Routes[1].Name)
	assert.NotEqual(t, fp2.backtestProposed.Routes[0].ID, fp2.backtestProposed.Routes[1].ID)
}

// A valid proposal route ID reaches the pipeline in canonical lowercase
// form with the rest of the route definition preserved.
func TestProposalUUID_ValidIDsNormalizedForPipeline(t *testing.T) {
	uppercase := `"3F1D2C8E-9A67-4B52-8F0C-1D2E3F4A5B6C"`

	t.Run("dry-run", func(t *testing.T) {
		fp, h := newProposalServer(t)
		body := `{"draft_configuration":{"duplicate_threshold":0.75,"duplicate_behavior":"skip","routes":[` +
			proposalRouteJSON(uppercase) + `]}}`
		req := httptest.NewRequest(http.MethodPost, "/api/runs/dry-run", strings.NewReader(body))
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)

		require.Equal(t, http.StatusAccepted, rec.Code, rec.Body.String())
		require.NotNil(t, fp.draft)
		require.Len(t, fp.draft.Routes, 1)
		assert.Equal(t, proposalUUID, fp.draft.Routes[0].ID,
			"the pipeline receives the canonical lowercase UUID")
		assert.Equal(t, 0.75, fp.draft.DuplicateThreshold)
		assert.Equal(t, "Music", fp.draft.Routes[0].Name)
		assert.Len(t, fp.draft.Routes[0].Conditions, 1)
	})

	t.Run("backtest", func(t *testing.T) {
		fp, h := newProposalServer(t)
		body := `{"proposed_configuration":{"duplicate_threshold":0.5,"duplicate_behavior":"skip","routes":[` +
			proposalRouteJSON(`"`+proposalUUID+`"`) + `]}}`
		req := httptest.NewRequest(http.MethodPost, "/api/backtest", strings.NewReader(body))
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)

		require.Equal(t, http.StatusAccepted, rec.Code, rec.Body.String())
		require.Len(t, fp.backtestProposed.Routes, 1)
		assert.Equal(t, proposalUUID, fp.backtestProposed.Routes[0].ID)
	})
}
