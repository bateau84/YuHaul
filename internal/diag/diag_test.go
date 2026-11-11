package diag

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"testing"

	"github.com/bateau84/YuHaul/internal/youtube"
	"golang.org/x/oauth2"
)

const markerFreeSecret = "plainASCII-secret-token-9081"

func TestClassifyProviderAndUnknownError(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want string
	}{
		{"wrapped registered provider", fmt.Errorf("private %s: %w", markerFreeSecret, &youtube.APIError{Status: 500, Reason: "backendError"}), "diag:v1 phase=action_execution operation=execute_action code=provider_error status=500 reason=backendError"},
		{"unknown prose", errors.New("failure " + markerFreeSecret), "diag:v1 phase=routing operation=evaluate_video code=unknown_error"},
		{"literal sqlite token is not typed evidence", errors.New("SQLITE_BUSY " + markerFreeSecret), "diag:v1 phase=routing operation=evaluate_video code=unknown_error"},
		{"canceled", context.Canceled, "diag:v1 phase=routing operation=evaluate_video code=canceled"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := Encode(tt.err, "routing", "evaluate_video")
			if tt.name == "wrapped registered provider" {
				got = Encode(tt.err, "action_execution", "execute_action")
			}
			if got != tt.want {
				t.Fatalf("Encode()=%q want %q", got, tt.want)
			}
			if contains(got, markerFreeSecret) {
				t.Fatalf("secret leaked: %q", got)
			}
		})
	}
}

func TestClassifyRealSQLiteBusyPreservesRegisteredCode(t *testing.T) {
	path := filepath.Join(t.TempDir(), "busy.db")
	dsn := "file:" + path + "?_pragma=busy_timeout(0)"
	first, err := sql.Open("sqlite", dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer first.Close()
	second, err := sql.Open("sqlite", dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer second.Close()
	if _, err := first.Exec(`CREATE TABLE busy_test (value INTEGER); INSERT INTO busy_test VALUES (0);`); err != nil {
		t.Fatal(err)
	}
	tx, err := first.Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	if _, err := tx.Exec(`UPDATE busy_test SET value = value + 1`); err != nil {
		t.Fatal(err)
	}
	_, err = second.Exec(`UPDATE busy_test SET value = value + 1`)
	if err == nil {
		t.Fatal("second concurrent writer unexpectedly succeeded")
	}
	want := "diag:v1 phase=routing operation=evaluate_video code=sqlite_error sqlite=SQLITE_BUSY"
	if got := Encode(fmt.Errorf("evaluation wrapper: %w", err), "routing", "evaluate_video"); got != want {
		t.Fatalf("Encode(real SQLITE_BUSY)=%q want %q", got, want)
	}
}

func TestMalformedHTTP200SubscriptionResponseClassifiesAtSource(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = fmt.Fprint(w, "TOKEN_SENTINEL_plain_ascii_7341")
	}))
	defer server.Close()
	client := youtube.NewClient(context.Background(), &oauth2.Token{AccessToken: "test"},
		youtube.WithBaseURL(server.URL), youtube.WithHTTPClient(server.Client()))
	_, err := client.ListSubscriptions(context.Background(), "")
	if err == nil {
		t.Fatal("expected malformed successful response")
	}
	want := "diag:v1 phase=discovery operation=subscriptions_list code=invalid_provider_response"
	if got := Encode(err, "discovery", "unknown"); got != want {
		t.Fatalf("Encode()=%q want %q", got, want)
	}
	if strings.Contains(err.Error(), "TOKEN_SENTINEL_plain_ascii_7341") {
		t.Fatalf("response content leaked in error: %v", err)
	}
}

func TestClassifyTypedNilTransportErrorsDoesNotPanic(t *testing.T) {
	var urlErr *url.Error
	var opErr *net.OpError
	for _, cause := range []error{urlErr, opErr, typedNilWrapper{cause: urlErr}, typedNilWrapper{cause: opErr}} {
		if got, want := Encode(cause, "discovery", "discover"), "diag:v1 phase=discovery operation=discover code=unknown_error"; got != want {
			t.Errorf("Encode(typed nil %T)=%q want %q", cause, got, want)
		}
	}
}

type typedNilWrapper struct{ cause error }

func (e typedNilWrapper) Error() string { return "wrapped transport error" }
func (e typedNilWrapper) Unwrap() error { return e.cause }

func TestAdmitStoredCanonicalAndLegacyOnly(t *testing.T) {
	good := []string{"diag:v1 phase=routing operation=evaluate_video code=sqlite_error sqlite=SQLITE_BUSY", "diag:v1 phase=action_execution operation=execute_action code=provider_error status=500 reason=backendError", "discovery phase failed", "panic: unrecoverable error", ""}
	for _, s := range good {
		if _, ok := AdmitStored(s); !ok {
			t.Errorf("AdmitStored rejected %q", s)
		}
	}
	bad := []string{markerFreeSecret, "SQLITE_BUSY", "failure " + markerFreeSecret, "insert playlist item: youtube API returned 500: backendError", "diag:v1 phase=routing operation=evaluate_video code=unknown_error reason=backendError", "diag:v1 phase=routing phase=unknown operation=evaluate_video code=unknown_error", "diag:v2 phase=routing operation=evaluate_video code=unknown_error"}
	for _, s := range bad {
		if _, ok := AdmitStored(s); ok {
			t.Errorf("AdmitStored admitted %q", s)
		}
	}
}

func TestAuthorizationDiagnosticRegistrationsAreClosed(t *testing.T) {
	for _, operation := range []string{"refresh_token", "save_token", "account_status", "disconnect_account", "oauth_callback", "dispatch_request"} {
		t.Run(operation, func(t *testing.T) {
			got := Encode(errors.New("provider prose "+markerFreeSecret), "unknown", operation)
			want := "diag:v1 phase=unknown operation=" + operation + " code=unknown_error"
			if got != want || strings.Contains(got, markerFreeSecret) {
				t.Fatalf("Encode()=%q want %q", got, want)
			}
		})
	}
	for _, code := range []string{"credential_persistence_failed", "authorization_changed", "mutation_application_uncertain"} {
		t.Run(code, func(t *testing.T) {
			f := Facts{Phase: "unknown", Operation: "account_status", Code: code}
			if !f.Valid() {
				t.Fatalf("required diagnostic code %q rejected", code)
			}
			if !Safe(f.String()) || strings.Contains(f.String(), markerFreeSecret) {
				t.Fatalf("safe code not admitted: %q", f.String())
			}
			f.Reason = "private provider prose"
			if f.Valid() {
				t.Fatal("custom code admitted incompatible/raw fields")
			}
		})
	}
	if _, ok := GuardDisposition("authorization_pending", "queue_reason"); !ok {
		t.Fatal("required deferred queue reason rejected")
	}
	for _, raw := range []string{"RetrieveError: " + markerFreeSecret, "custody failed: " + markerFreeSecret, markerFreeSecret} {
		if _, ok := AdmitStored(raw); ok {
			t.Fatalf("admitted raw authorization/custody prose %q", raw)
		}
	}
}

func TestReasonNeverUsesFallbackAsDiagnostic(t *testing.T) {
	got := Reason(errors.New(markerFreeSecret), "playlist insertion failed")
	want := "diag:v1 phase=action_execution operation=execute_action code=unknown_error"
	if got != want {
		t.Fatalf("Reason()=%q want %q", got, want)
	}
	if got = Public(markerFreeSecret, "discovery phase failed"); got != "diag:v1 phase=discovery operation=discover code=unknown_error" {
		t.Fatalf("Public()=%q", got)
	}
}

func contains(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}

func TestWithCodeMarksOnlyRegisteredUncertainMutation(t *testing.T) {
	marked := At(WithCode(errors.New("secret "+markerFreeSecret), "mutation_application_uncertain"), "action_execution", "execute_action")
	got := Encode(fmt.Errorf("wrap: %w", marked), "unknown", "retry")
	want := "diag:v1 phase=action_execution operation=execute_action code=mutation_application_uncertain"
	if got != want {
		t.Fatalf("Encode()=%q want %q", got, want)
	}
	if !Safe(got) {
		t.Fatalf("%q must be admitted", got)
	}
	plain := errors.New("x")
	if WithCode(plain, "provider_error") != plain {
		t.Fatal("an unregistered marker code must be ignored")
	}
}

func TestInterruptReasonKeepsContextCodeAndStepFacts(t *testing.T) {
	tests := []struct {
		name     string
		err      error
		ctxErr   error
		fallback string
		want     string
	}{
		{"manual retry", errors.New("cause"), context.Canceled, "manual retry failed", "diag:v1 phase=action_execution operation=retry code=canceled"},
		{"retry deadline", errors.New("cause"), context.DeadlineExceeded, "manual retry failed", "diag:v1 phase=action_execution operation=retry code=timeout"},
		{"source facts win", At(errors.New("cause"), "discovery", "discover"), context.Canceled, "manual retry failed", "diag:v1 phase=discovery operation=discover code=canceled"},
		{"provider code never overrides the context", &youtube.APIError{Status: 500, Reason: "backendError"}, context.Canceled, "discovery phase failed", "diag:v1 phase=discovery operation=discover code=canceled"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := InterruptReason(tt.err, tt.ctxErr, tt.fallback); got != tt.want {
				t.Fatalf("InterruptReason()=%q want %q", got, tt.want)
			}
		})
	}
}

// Stop and discard reasons (run-initiation ARS-003 §1.3, ARS-002 §1).
func TestStopAndDiscardReasonsRoundTrip(t *testing.T) {
	for _, s := range []string{
		"diag:v1 phase=unknown operation=stop_run code=stopped_by_user",
		"diag:v1 phase=unknown operation=discard_run code=snapshot_unavailable",
		"diag:v1 phase=unknown operation=discard_run code=snapshot_unreadable",
		"diag:v1 phase=unknown operation=discard_run code=checkpoint_unrecognized",
		"diag:v1 phase=unknown operation=end_legacy_run code=ended_by_user",
	} {
		got, ok := AdmitStored(s)
		if !ok || got != s {
			t.Errorf("AdmitStored(%q) = %q, %v", s, got, ok)
		}
		if g := GuardErrorText(s); g != s {
			t.Errorf("GuardErrorText(%q) = %q", s, g)
		}
	}
	for _, f := range []Facts{
		{Phase: "unknown", Operation: "stop_run", Code: "timeout"},
		{Phase: "unknown", Operation: "finalize_run", Code: "stopped_by_user"},
		{Phase: "unknown", Operation: "stop_run", Code: "stopped_by_user", Status: "500"},
		{Phase: "unknown", Operation: "discard_run", Code: "timeout"},
		{Phase: "unknown", Operation: "finalize_run", Code: "snapshot_unavailable"},
		{Phase: "unknown", Operation: "discard_run", Code: "snapshot_unavailable", Status: "500"},
		{Phase: "unknown", Operation: "discard_run", Code: "stopped_by_user"},
		{Phase: "unknown", Operation: "stop_run", Code: "snapshot_unreadable"},
		{Phase: "unknown", Operation: "end_legacy_run", Code: "timeout"},
		{Phase: "unknown", Operation: "end_legacy_run", Code: "stopped_by_user"},
		{Phase: "unknown", Operation: "stop_run", Code: "ended_by_user"},
		{Phase: "unknown", Operation: "discard_run", Code: "ended_by_user"},
		{Phase: "unknown", Operation: "finalize_run", Code: "ended_by_user"},
		{Phase: "unknown", Operation: "end_legacy_run", Code: "ended_by_user", Status: "500"},
		{Phase: "unknown", Operation: "end_legacy_run", Code: "ended_by_user", Reason: "quotaExceeded"},
		{Phase: "unknown", Operation: "end_legacy_run", Code: "ended_by_user", SQLite: "SQLITE_BUSY"},
	} {
		if f.Valid() {
			t.Errorf("%+v must be invalid", f)
		}
	}
	for _, r := range []string{"stopped_by_user", "discarded_by_user"} {
		if _, ok := GuardDisposition(r, "queue_reason"); !ok {
			t.Errorf("queue_reason %s rejected", r)
		}
	}
}

// ARS-005 §1.3: video_unavailable is valid only for routing/load_metadata with
// no other fact, and requeued_by_user is an admitted queue_reason.
func TestVideoUnavailableAndRequeueVocabulary(t *testing.T) {
	const s = "diag:v1 phase=routing operation=load_metadata code=video_unavailable"
	if got, ok := AdmitStored(s); !ok || got != s {
		t.Errorf("AdmitStored(%q) = %q, %v", s, got, ok)
	}
	if g := GuardErrorText(s); g != s {
		t.Errorf("GuardErrorText(%q) = %q", s, g)
	}
	for _, f := range []Facts{
		{Phase: "unknown", Operation: "load_metadata", Code: "video_unavailable"},
		{Phase: "routing", Operation: "evaluate_video", Code: "video_unavailable"},
		{Phase: "routing", Operation: "requeue_stranded", Code: "video_unavailable"},
		{Phase: "routing", Operation: "load_metadata", Code: "video_unavailable", Status: "404"},
		{Phase: "routing", Operation: "load_metadata", Code: "video_unavailable", Reason: "videoNotFound"},
		{Phase: "routing", Operation: "load_metadata", Code: "video_unavailable", SQLite: "SQLITE_BUSY"},
	} {
		if f.Valid() {
			t.Errorf("Facts %+v must be invalid", f)
		}
	}
	if !(Facts{Phase: "unknown", Operation: "requeue_stranded", Code: "unknown_error"}).Valid() {
		t.Error("requeue_stranded must be a registered operation")
	}
	if v, ok := GuardDisposition("requeued_by_user", "queue_reason"); !ok || v != "requeued_by_user" {
		t.Errorf("queue_reason requeued_by_user = %q, %v", v, ok)
	}
}
