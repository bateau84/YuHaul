package youtube

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"golang.org/x/oauth2"
)

// sentinelBody is a fake provider error payload carrying secret-looking text.
// None of it may survive into error strings, storage, events, or logs
// (ARD-001 failure diagnostic boundary).
const sentinelBody = `{"error":{"errors":[{"reason":"backendError","message":"SENTINEL_SECRET_body_text"}],"code":500,"message":"Internal Server Error"}}`

func newTestClient(t *testing.T, handler http.HandlerFunc) *Client {
	t.Helper()
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)
	return NewClient(context.Background(), &oauth2.Token{AccessToken: "test"},
		WithBaseURL(srv.URL), WithHTTPClient(srv.Client()))
}

func TestAPIError_ExcludesRawResponseBody(t *testing.T) {
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = io.WriteString(w, sentinelBody)
	})

	err := c.InsertPlaylistItem(context.Background(), "PL1", "v1")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "500", "safe HTTP status must remain visible")
	assert.Contains(t, err.Error(), "backendError", "vetted provider reason must remain visible")
	assert.NotContains(t, err.Error(), "SENTINEL_SECRET_body_text")
	assert.NotContains(t, err.Error(), "Internal Server Error", "raw message text must not survive")
}

func TestAPIError_VettedReasonIsBounded(t *testing.T) {
	// A hostile or malformed provider reason token is dropped entirely; the
	// status remains.
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		_, _ = io.WriteString(w, `{"error":{"errors":[{"reason":"{\"token\":\"ya29 SECRET\"}"}]}}`)
	})

	err := c.InsertPlaylistItem(context.Background(), "PL1", "v1")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "403")
	assert.NotContains(t, err.Error(), "ya29")
	assert.NotContains(t, err.Error(), "SECRET")
}

func TestAPIError_RejectsMarkerFreeReasonAndInvalidStatus(t *testing.T) {
	const secret = "TOKEN_SENTINEL_plain_ascii_7341"
	assert.NotContains(t, (&APIError{Status: 500, Reason: secret}).Error(), secret)
	assert.Equal(t, "youtube API error", (&APIError{Status: 700, Reason: "backendError"}).Error())
}

func TestListSubscriptionsMarksMalformedSuccessfulResponse(t *testing.T) {
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = io.WriteString(w, "TOKEN_SENTINEL_plain_ascii_7341")
	})
	_, err := c.ListSubscriptions(context.Background(), "")
	if err == nil {
		t.Fatal("expected malformed successful response to fail")
	}
	var malformed *InvalidResponseError
	if !errors.As(err, &malformed) || malformed == nil {
		t.Fatalf("error type=%T want InvalidResponseError", err)
	}
	if strings.Contains(err.Error(), "TOKEN_SENTINEL_plain_ascii_7341") {
		t.Fatalf("response body leaked in error: %v", err)
	}
}

func TestAPIError_QuotaDetectionStillWorks(t *testing.T) {
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		_, _ = io.WriteString(w, `{"error":{"errors":[{"reason":"quotaExceeded"}]}}`)
	})

	err := c.InsertPlaylistItem(context.Background(), "PL1", "v1")
	require.Error(t, err)
	assert.True(t, IsQuotaExceeded(err))
}

func TestTransportError_OmitsRequestURLAndQuery(t *testing.T) {
	// net/url transport errors embed the full request URL (including query
	// parameters); the bounded wrapper must strip it.
	closed := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	url := closed.URL
	closed.Close() // now refusing connections

	c := NewClient(context.Background(), &oauth2.Token{AccessToken: "test"},
		WithBaseURL(url), WithHTTPClient(&http.Client{}))

	err := c.InsertPlaylistItem(context.Background(), "PL1", "v1")
	require.Error(t, err)
	assert.NotContains(t, err.Error(), url, "request URL must not cross the diagnostic boundary")
	assert.NotContains(t, err.Error(), "part=snippet", "query data must not cross the diagnostic boundary")
	assert.Contains(t, err.Error(), "execute request", "operation label must remain visible")
}
