package advisory

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestHandler_AdvisoryLifecycle(t *testing.T) {
	ctx := context.Background()
	db, _, service, reader, cleanup := setupAdvisoryTest(t)
	defer cleanup()

	seedChannel(t, ctx, db, "UC1", "Channel One")
	reader.channels = []ChannelStats{
		{ChannelID: "UC1", ChannelName: "Channel One", TotalDiscovered: 100, TotalRouted: 40, TotalRetained: 1},
	}

	require.NoError(t, service.GenerateAdvisories(ctx))
	advisories, err := service.ListActive(ctx)
	require.NoError(t, err)
	require.Len(t, advisories, 1)
	advisoryID := advisories[0].ID

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	handler := NewHandler(service, logger).Handler()

	// Dismiss
	req := httptest.NewRequest(http.MethodPost, "/api/analytics/advisories/"+strconv.FormatInt(advisoryID, 10)+"/dismiss", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	assert.Equal(t, http.StatusNoContent, rec.Code)

	// List active should now be empty
	req = httptest.NewRequest(http.MethodGet, "/api/analytics/advisories", nil)
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	assert.Equal(t, http.StatusOK, rec.Code)
	assert.Contains(t, rec.Body.String(), `"advisories":[]`)
}

func TestHandler_Act(t *testing.T) {
	ctx := context.Background()
	db, _, service, reader, cleanup := setupAdvisoryTest(t)
	defer cleanup()

	seedChannel(t, ctx, db, "UC1", "Channel One")
	reader.channels = []ChannelStats{
		{ChannelID: "UC1", ChannelName: "Channel One", TotalDiscovered: 100, TotalRouted: 40, TotalRetained: 1},
	}

	require.NoError(t, service.GenerateAdvisories(ctx))
	advisories, err := service.ListActive(ctx)
	require.NoError(t, err)
	require.Len(t, advisories, 1)
	advisoryID := advisories[0].ID

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	handler := NewHandler(service, logger).Handler()

	body := strings.NewReader(`{"action":"unsubscribed"}`)
	req := httptest.NewRequest(http.MethodPost, "/api/analytics/advisories/"+strconv.FormatInt(advisoryID, 10)+"/act", body)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	assert.Equal(t, http.StatusNoContent, rec.Code)
}
