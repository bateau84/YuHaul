package youtube

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"golang.org/x/oauth2"
)

func TestInsertGuardedPlaylistItemClassifiesAndBindsDispatch(t *testing.T) {
	tests := []struct {
		name        string
		status      int
		body        string
		admission   DispatchAdmission
		notAdmit    bool
		dispatchErr error
		want        MutationApplication
		wantRefusal DispatchRefusalKind
		wantCall    bool
	}{
		{name: "validated success", status: 200, body: `{"id":"item","snippet":{"playlistId":"target","resourceId":{"videoId":"external"}}}`, admission: validTestAdmission(), want: MutationSucceeded, wantCall: true},
		{name: "quota rejection", status: 403, body: `{"error":{"errors":[{"reason":"quotaExceeded"}]}}`, admission: validTestAdmission(), want: MutationNotApplied, wantCall: true},
		{name: "mixed quota and auth reasons remain uncertain", status: 403, body: `{"error":{"errors":[{"reason":"quotaExceeded"},{"reason":"authError"}]}}`, admission: validTestAdmission(), want: MutationUncertain, wantCall: true},
		{name: "truncated quota response remains uncertain", status: 403, body: `{"error":{"errors":[{"reason":"quotaExceeded"}`, admission: validTestAdmission(), want: MutationUncertain, wantCall: true},
		{name: "malformed quota response remains uncertain", status: 403, body: `not-json quotaExceeded`, admission: validTestAdmission(), want: MutationUncertain, wantCall: true},
		{name: "other forbidden remains uncertain", status: 403, body: `{"error":{"errors":[{"reason":"forbidden"}]}}`, admission: validTestAdmission(), want: MutationUncertain, wantCall: true},
		{name: "refused before admission", notAdmit: true, dispatchErr: context.Canceled, want: MutationNotIssued, wantCall: true},
		{name: "positive authorization refusal", notAdmit: true, dispatchErr: DispatchRefusalError{Kind: DispatchRefusalAuthorization}, want: MutationNotIssued, wantRefusal: DispatchRefusalAuthorization, wantCall: true},
		{name: "quota refusal", notAdmit: true, dispatchErr: DispatchRefusalError{Kind: DispatchRefusalQuota}, want: MutationNotIssued, wantRefusal: DispatchRefusalQuota, wantCall: true},
		{name: "local validation refusal is not auth pending", notAdmit: true, dispatchErr: DispatchRefusalError{Kind: DispatchRefusalLocalValidation}, want: MutationNotIssued, wantRefusal: DispatchRefusalLocalValidation, wantCall: true},
		{name: "missing receipt is not positive refusal", notAdmit: true, want: MutationNotIssued, wantCall: true},
		{name: "transport failure after admission", admission: validTestAdmission(), dispatchErr: io.ErrUnexpectedEOF, want: MutationUncertain, wantCall: true},
		{name: "wrong identity receipt", status: 200, body: `{"id":"item","snippet":{"playlistId":"target","resourceId":{"videoId":"external"}}}`, admission: DispatchAdmission{AuthorizationID: "other", CredentialRevision: 1, AdmittedAt: time.Now()}, want: MutationUncertain, wantCall: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			calls := 0
			accounted := 0
			client := NewClient(context.Background(), nil, WithBaseURL("http://example.test"), WithRecorder(func(context.Context, Operation, int) error { accounted++; return nil }))
			result := client.InsertGuardedPlaylistItem(context.Background(), testClaim(), "external", func(_ context.Context, req *http.Request, _ http.RoundTripper) (*http.Response, DispatchAdmission, bool, error) {
				calls++
				if req.Method != http.MethodPost || req.URL.Path != "/playlistItems" || req.URL.RawQuery != "part=snippet" {
					t.Fatalf("unexpected request: %s %s", req.Method, req.URL)
				}
				payload, err := io.ReadAll(req.Body)
				if err != nil {
					t.Fatal(err)
				}
				if !strings.Contains(string(payload), `"playlistId":"target"`) || !strings.Contains(string(payload), `"videoId":"external"`) {
					t.Fatalf("request payload substituted descriptor: %s", payload)
				}
				var response *http.Response
				if tt.dispatchErr == nil {
					response = &http.Response{StatusCode: tt.status, Body: io.NopCloser(strings.NewReader(tt.body)), Header: make(http.Header)}
				}
				return response, tt.admission, tt.notAdmit, tt.dispatchErr
			}, nil)
			if result.Application != tt.want {
				t.Fatalf("application=%q want %q", result.Application, tt.want)
			}
			if result.Refusal != tt.wantRefusal {
				t.Fatalf("refusal=%q want %q", result.Refusal, tt.wantRefusal)
			}
			if calls != 1 {
				t.Fatalf("dispatch calls=%d want 1", calls)
			}
			if (calls > 0) != tt.wantCall {
				t.Fatalf("calls=%d", calls)
			}
			if accounted != 0 {
				t.Fatalf("adapter recorded %d attempts", accounted)
			}
		})
	}
}

func TestInsertGuardedPlaylistItemRejectsInvalidClaimWithoutDispatch(t *testing.T) {
	calls := 0
	client := NewClient(context.Background(), nil)
	claim := testClaim()
	claim.TargetPlaylistID = ""
	result := client.InsertGuardedPlaylistItem(context.Background(), claim, "external", func(context.Context, *http.Request, http.RoundTripper) (*http.Response, DispatchAdmission, bool, error) {
		calls++
		return nil, DispatchAdmission{}, false, nil
	}, nil)
	if result.Application != MutationNotIssued || calls != 0 {
		t.Fatalf("result=%+v dispatches=%d", result, calls)
	}
}

func TestGuardedInsertPassesUnderlyingTransportToDispatch(t *testing.T) {
	base := &testRoundTripper{}
	client := NewAuthorizedClient(context.Background(), func(context.Context) (*oauth2.Token, error) {
		t.Fatal("guarded insertion must not resolve credentials outside Dispatch")
		return nil, nil
	}, WithBaseURL("http://example.test"))
	client.httpClient.Transport = authorizationTransport{resolve: func(context.Context) (*oauth2.Token, error) {
		t.Fatal("authorization wrapper must not be dispatched")
		return nil, nil
	}, base: base}
	client.InsertGuardedPlaylistItem(context.Background(), testClaim(), "external", func(_ context.Context, _ *http.Request, transport http.RoundTripper) (*http.Response, DispatchAdmission, bool, error) {
		if transport != base {
			t.Fatalf("dispatch transport=%T, want underlying base transport", transport)
		}
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(`{"id":"item","snippet":{"playlistId":"target","resourceId":{"videoId":"external"}}}`))}, validTestAdmission(), false, nil
	}, nil)
}

type testRoundTripper struct{}

func (*testRoundTripper) RoundTrip(*http.Request) (*http.Response, error) { return nil, nil }

func testClaim() PlaylistClaim {
	return PlaylistClaim{RunID: "run", VideoID: "internal", RouteDecisionID: "decision", ActionType: "playlist_insert", TargetPlaylistID: "target", AuthorizationID: "auth-id"}
}
func validTestAdmission() DispatchAdmission {
	return DispatchAdmission{AuthorizationID: "auth-id", CredentialRevision: 1, AdmittedAt: time.Now().UTC()}
}

type guardedBody struct {
	reader io.Reader
	closed bool
}

func (b *guardedBody) Read(p []byte) (int, error) { return b.reader.Read(p) }
func (b *guardedBody) Close() error               { b.closed = true; return nil }

type failingGuardedReader struct{}

func (failingGuardedReader) Read([]byte) (int, error) { return 0, io.ErrUnexpectedEOF }

func TestGuardedInsertReturnsFactsWithoutSideEffects(t *testing.T) {
	tests := []struct {
		name        string
		status      int
		body        io.Reader
		dispatchErr error
		admission   DispatchAdmission
		category    string
		rejected    bool
		application MutationApplication
	}{
		{"unauthorized", 401, strings.NewReader(`{}`), nil, validTestAdmission(), "auth", true, MutationUncertain},
		{"unauthorized unreadable", 401, failingGuardedReader{}, nil, validTestAdmission(), "auth", true, MutationUncertain},
		{"auth forbidden", 403, strings.NewReader(`{"error":{"errors":[{"reason":"authError"}]}}`), nil, validTestAdmission(), "auth", true, MutationUncertain},
		{"quota", 403, strings.NewReader(`{"error":{"errors":[{"reason":"quotaExceeded"}]}}`), nil, validTestAdmission(), "quota", false, MutationNotApplied},
		{"rate limit", 403, strings.NewReader(`{"error":{"errors":[{"reason":"rateLimitExceeded"}]}}`), nil, validTestAdmission(), "transient", false, MutationUncertain},
		{"malformed success", 200, strings.NewReader(`{}`), nil, validTestAdmission(), "success", false, MutationUncertain},
		{"mismatched receipt", 401, strings.NewReader(`{}`), nil, DispatchAdmission{AuthorizationID: "other", CredentialRevision: 1, AdmittedAt: time.Now()}, "auth", false, MutationUncertain},
		{"response and error", 503, strings.NewReader(`{}`), io.ErrUnexpectedEOF, validTestAdmission(), "transient", false, MutationUncertain},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			body := &guardedBody{reader: tt.body}
			recorded, observed := 0, 0
			c := NewClient(context.Background(), nil, WithBaseURL("http://example.test"), WithRecorder(func(context.Context, Operation, int) error { recorded++; return nil }))
			result := c.InsertGuardedPlaylistItem(context.Background(), testClaim(), "external", func(context.Context, *http.Request, http.RoundTripper) (*http.Response, DispatchAdmission, bool, error) {
				return &http.Response{StatusCode: tt.status, Body: body}, tt.admission, false, tt.dispatchErr
			}, func(context.Context, DispatchAdmission) error { observed++; return nil })
			if !body.closed || recorded != 0 || observed != 0 {
				t.Fatalf("closed=%v recorded=%d observed=%d", body.closed, recorded, observed)
			}
			if result.HTTPStatus != tt.status || result.AccountingCategory != tt.category || result.AccessRejected != tt.rejected || result.Application != tt.application {
				t.Fatalf("result=%+v", result)
			}
		})
	}
}
