package server

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// stubCorpusImportHandler records the request it received so the test can
// prove the API mux forwards /api/corpus/* to the registered handler.
type stubCorpusImportHandler struct {
	lastPath string
}

func (s *stubCorpusImportHandler) Handler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s.lastPath = r.URL.Path
		w.WriteHeader(http.StatusOK)
	})
}

// TestCorpusImportRoutesMounted proves the ARS-002 corpus import endpoints are
// registered on the real API mux and reach the injected handler with their
// full paths intact (the handler owns its own /api/corpus/* routes).
func TestCorpusImportRoutesMounted(t *testing.T) {
	stub := &stubCorpusImportHandler{}
	srv := NewServer(nil, nil, WithCorpusImportHandler(stub))

	paths := []struct {
		method string
		path   string
	}{
		{http.MethodPost, "/api/corpus/import/preview"},
		{http.MethodPost, "/api/corpus/import"},
		{http.MethodGet, "/api/corpus/import"},
		{http.MethodGet, "/api/corpus/import/some-id"},
		{http.MethodPost, "/api/corpus/import/some-id/cancel"},
	}
	for _, tc := range paths {
		req := httptest.NewRequest(tc.method, tc.path, nil)
		rec := httptest.NewRecorder()
		srv.Handler().ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("%s %s: status = %d, want 200 (routed to the corpus import handler); body = %q",
				tc.method, tc.path, rec.Code, rec.Body.String())
		}
		if stub.lastPath != tc.path {
			t.Fatalf("%s %s: handler saw %q, want the full path %q", tc.method, tc.path, stub.lastPath, tc.path)
		}
	}
}

// TestCorpusImportUnwiredFailsLoud proves an unwired server reports the
// corpus import handler as missing instead of falling through to the SPA or a
// generic 404, so a wiring regression is loud (ARS-002).
func TestCorpusImportUnwiredFailsLoud(t *testing.T) {
	srv := NewServer(nil, nil)
	req := httptest.NewRequest(http.MethodPost, "/api/corpus/import", nil)
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404; body = %q", rec.Code, rec.Body.String())
	}
	if body := rec.Body.String(); !strings.Contains(body, "corpus import handler not configured") {
		t.Fatalf("body = %q, want the not-configured error", body)
	}
}
