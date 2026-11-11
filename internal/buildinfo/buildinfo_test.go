package buildinfo

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func serve(t *testing.T) (*httptest.ResponseRecorder, map[string]string) {
	t.Helper()
	rec := httptest.NewRecorder()
	Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/version", nil))
	var body map[string]string
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode body %q: %v", rec.Body.String(), err)
	}
	return rec, body
}

func TestHandlerDefaultsToUnknown(t *testing.T) {
	rec, body := serve(t)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	for _, k := range []string{"version", "commit", "built_at"} {
		if body[k] != "unknown" {
			t.Errorf("%s = %q, want unknown", k, body[k])
		}
	}
	if got := rec.Header().Get("Cache-Control"); got != "no-store" {
		t.Errorf("Cache-Control = %q", got)
	}
}

func TestHandlerReportsInjectedValues(t *testing.T) {
	oldV, oldC, oldB := Version, Commit, BuiltAt
	t.Cleanup(func() { Version, Commit, BuiltAt = oldV, oldC, oldB })
	Version, Commit, BuiltAt = "1.2.3", "abc1234", "2026-01-02T03:04:05Z"

	_, body := serve(t)
	if body["version"] != "1.2.3" || body["commit"] != "abc1234" || body["built_at"] != "2026-01-02T03:04:05Z" {
		t.Fatalf("body = %v", body)
	}
}
