package youtube

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

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

func newAccountedTestClient(t *testing.T, handler http.HandlerFunc) *Client {
	t.Helper()
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)
	return NewAuthorizedClient(context.Background(), nil, WithBaseURL(srv.URL), WithHTTPClient(srv.Client()), WithQuotaReads(),
		WithRequestDispatcher(func(_ context.Context, req *http.Request, rt http.RoundTripper) (*http.Response, RequestAdmission, error) {
			resp, err := rt.RoundTrip(req)
			return resp, RequestAdmission{AdmittedAt: time.Now(), Finalize: func(context.Context, string, int) error { return nil }}, err
		}))
}

func TestGetVideosWithReturnedIDs(t *testing.T) {
	tests := []struct {
		name, body string
		invalid    bool
		ids        []string
		details    int
	}{
		{"empty", `{"items":[]}`, false, []string{}, 0},
		{"unusable returned", `{"items":[{"id":"v1","snippet":{"publishedAt":"bad"}}]}`, false, []string{"v1"}, 0},
		{"valid fields", `{"items":[{"id":"v1","snippet":{"publishedAt":"2024-01-01T00:00:00Z","title":"","channelId":"c","channelTitle":"","description":""},"contentDetails":{"duration":"PT0S"}}]}`, false, []string{"v1"}, 1},
		{"missing fields", `{"items":[{"id":"v1","snippet":{"publishedAt":"2024-01-01T00:00:00Z"},"contentDetails":{"duration":"bad"}}]}`, false, []string{"v1"}, 1},
		{"invalid field types", `{"items":[{"id":"v1","snippet":{"publishedAt":"2024-01-01T00:00:00Z","title":null,"description":12},"contentDetails":{"duration":"PT999999999999999999999H"}}]}`, false, []string{"v1"}, 1},
		{"missing items", `{}`, true, nil, 0},
		{"null envelope", `null`, true, nil, 0},
		{"null items", `{"items":null}`, true, nil, 0},
		{"non-array", `{"items":{}}`, true, nil, 0},
		{"null item", `{"items":[null]}`, true, nil, 0},
		{"missing id", `{"items":[{}]}`, true, nil, 0},
		{"bad id type", `{"items":[{"id":3}]}`, true, nil, 0},
		{"invalid id", `{"items":[{"id":"v 1"}]}`, true, nil, 0},
		{"duplicate", `{"items":[{"id":"v1"},{"id":"v1"}]}`, true, nil, 0},
		{"unrequested", `{"items":[{"id":"v2"}]}`, true, nil, 0},
		{"truncated", `{"items":[{"id":"v1"}`, true, nil, 0},
		{"continuation", `{"items":[],"nextPageToken":"more"}`, true, nil, 0},
		{"null continuation", `{"items":[],"nextPageToken":null}`, true, nil, 0},
		{"invalid continuation", `{"items":[],"nextPageToken":1}`, true, nil, 0},
		{"empty continuation", `{"items":[],"nextPageToken":""}`, false, []string{}, 0},
		{"repeated items", `{"items":[{"id":"v1"}],"items":[]}`, true, nil, 0},
		{"repeated token", `{"items":[],"nextPageToken":"more","nextPageToken":""}`, true, nil, 0},
		{"repeated id", `{"items":[{"id":"v2","id":"v1"}]}`, true, nil, 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			calls := 0
			c := newAccountedTestClient(t, func(w http.ResponseWriter, r *http.Request) {
				calls++
				if r.URL.Query().Get("id") != "v1" {
					t.Errorf("unexpected request ID")
				}
				_, _ = io.WriteString(w, tt.body)
			})
			details, returned, err := c.GetVideosWithReturnedIDs(context.Background(), []string{"v1"})
			if calls != 1 {
				t.Fatalf("requests = %d", calls)
			}
			if tt.invalid {
				var invalid *InvalidResponseError
				if !errors.As(err, &invalid) || invalid.Operation() != OpVideosList || returned.Complete || len(details) != 0 {
					t.Fatalf("invalid response: details=%v returned=%v err=%v", details, returned, err)
				}
				return
			}
			if err != nil || !returned.Complete {
				t.Fatalf("returned=%v err=%v", returned, err)
			}
			assert.Equal(t, tt.ids, returned.IDs)
			require.Len(t, details, tt.details)
			if tt.name == "valid fields" {
				assert.Equal(t, VideoDetailPresence{true, true, true, true, true}, details[0].Present)
				assert.Empty(t, details[0].Description)
				assert.Zero(t, details[0].DurationSeconds)
			}
			if tt.name == "missing fields" || tt.name == "invalid field types" {
				assert.Equal(t, VideoDetailPresence{}, details[0].Present)
			}
		})
	}
}

// ARS-005 §6.1 and D7: an element with an empty id in a valid envelope names no
// video. The identified peers keep their own attribution and details, but the
// ID set is not complete, so it proves no omission.
func TestGetVideosWithReturnedIDsAnonymousElementKeepsIdentifiedPeers(t *testing.T) {
	good := func(id string) string {
		return `{"id":"` + id + `","snippet":{"publishedAt":"2024-01-01T00:00:00Z","title":"T ` + id + `"},"contentDetails":{"duration":"PT1M"}}`
	}
	nameless := `{"id":"","snippet":{"publishedAt":"2024-01-01T00:00:00Z","title":"nameless"},"contentDetails":{"duration":"PT1M"}}`
	for _, tc := range []struct {
		name, items string
		ids         []string
		details     []string
	}{
		{"before peers", nameless + "," + good("v1") + "," + good("v3"), []string{"v1", "v3"}, []string{"v1", "v3"}},
		{"between peers", good("v1") + "," + nameless + "," + good("v3"), []string{"v1", "v3"}, []string{"v1", "v3"}},
		{"after peers", good("v1") + "," + good("v3") + "," + nameless, []string{"v1", "v3"}, []string{"v1", "v3"}},
		{"two nameless", nameless + "," + good("v1") + "," + nameless, []string{"v1"}, []string{"v1"}},
		{"only nameless", nameless, []string{}, nil},
		{"unparseable peer still returned", `{"id":"v1","snippet":{"publishedAt":"bad"}},` + nameless + "," + good("v3"), []string{"v1", "v3"}, []string{"v3"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c := newAccountedTestClient(t, func(w http.ResponseWriter, _ *http.Request) {
				_, _ = io.WriteString(w, `{"items":[`+tc.items+`]}`)
			})
			details, returned, err := c.GetVideosWithReturnedIDs(context.Background(), []string{"v1", "v2", "v3"})
			require.NoError(t, err)
			assert.False(t, returned.Complete, "an anonymous element proves no omission")
			assert.Equal(t, tc.ids, returned.IDs)
			var got []string
			for _, d := range details {
				got = append(got, d.ID)
				assert.Equal(t, "T "+d.ID, d.Title)
			}
			assert.Equal(t, tc.details, got)
			assert.Len(t, returned.Live, len(tc.ids), "one live entry per identified element, none for the anonymous one")
			for _, id := range tc.ids {
				assert.Contains(t, returned.Live, id)
			}
		})
	}
}

// The anonymous allowance is only for an element that names no ID. A duplicate,
// foreign, malformed, non-string or missing id, or a broken envelope, still
// makes the whole response invalid, with the identified peers discarded.
func TestGetVideosWithReturnedIDsAnonymousElementDoesNotWeakenAmbiguity(t *testing.T) {
	good := `{"id":"v1","snippet":{"publishedAt":"2024-01-01T00:00:00Z"}}`
	nameless := `{"id":""}`
	for name, body := range map[string]string{
		"duplicate peer":         `{"items":[` + good + `,` + nameless + `,` + good + `]}`,
		"foreign id":             `{"items":[` + good + `,` + nameless + `,{"id":"v9"}]}`,
		"malformed id":           `{"items":[` + good + `,` + nameless + `,{"id":"v 3"}]}`,
		"non-string id":          `{"items":[` + good + `,` + nameless + `,{"id":3}]}`,
		"missing id":             `{"items":[` + good + `,` + nameless + `,{}]}`,
		"null id":                `{"items":[` + good + `,` + nameless + `,{"id":null}]}`,
		"null element":           `{"items":[` + good + `,` + nameless + `,null]}`,
		"repeated id member":     `{"items":[` + good + `,{"id":"","id":"v3"}]}`,
		"continuation token":     `{"items":[` + good + `,` + nameless + `],"nextPageToken":"more"}`,
		"repeated items":         `{"items":[` + good + `,` + nameless + `],"items":[]}`,
		"truncated after peer":   `{"items":[` + good + `,` + nameless,
		"items is not an array":  `{"items":{"id":""}}`,
		"missing items envelope": `{}`,
	} {
		t.Run(name, func(t *testing.T) {
			c := newAccountedTestClient(t, func(w http.ResponseWriter, _ *http.Request) { _, _ = io.WriteString(w, body) })
			details, returned, err := c.GetVideosWithReturnedIDs(context.Background(), []string{"v1", "v2", "v3"})
			var invalid *InvalidResponseError
			require.ErrorAs(t, err, &invalid)
			assert.Equal(t, OpVideosList, invalid.Operation())
			assert.Empty(t, details)
			assert.Empty(t, returned.IDs)
			assert.False(t, returned.Complete)
		})
	}
}

func TestGetVideosWithReturnedIDsAmbiguousInputsAndDays(t *testing.T) {
	for _, tc := range []struct {
		name, snippet, content string
		description, duration  bool
		seconds                int
	}{
		{"duplicate description", `"description":"secret","description":""`, `"duration":"PT0S"`, false, true, 0},
		{"day duration", `"description":""`, `"duration":"P1DT2H3M4S"`, true, true, 93784},
		{"days only", `"description":""`, `"duration":"P2D"`, true, true, 172800},
		{"overflow days", `"description":""`, `"duration":"P999999999999999999999D"`, true, false, 0},
		{"duplicate duration", `"description":""`, `"duration":"PT9S","duration":"PT0S"`, true, false, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			body := `{"items":[{"id":"v1","snippet":{"publishedAt":"2024-01-01T00:00:00Z",` + tc.snippet + `},"contentDetails":{` + tc.content + `}}]}`
			c := newAccountedTestClient(t, func(w http.ResponseWriter, _ *http.Request) { _, _ = io.WriteString(w, body) })
			d, ids, err := c.GetVideosWithReturnedIDs(context.Background(), []string{"v1"})
			require.NoError(t, err)
			require.True(t, ids.Complete)
			require.Equal(t, []string{"v1"}, ids.IDs)
			require.Len(t, d, 1)
			assert.Equal(t, tc.description, d[0].Present.Description)
			assert.Equal(t, tc.duration, d[0].Present.Duration)
			if tc.duration {
				assert.Equal(t, tc.seconds, d[0].DurationSeconds)
			}
		})
	}
}

func TestGetVideosWithReturnedIDsHTTPCompleteness(t *testing.T) {
	for _, tc := range []struct {
		name                           string
		status                         int
		truncated, admitted, finalized bool
	}{
		{"partial HTTP", 206, false, true, true},
		{"truncated HTTP", 200, true, true, true},
		{"no admission", 200, false, false, false},
		{"finalizer without admission", 200, false, false, true},
		{"missing finalizer", 200, false, true, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			calls, finishes := 0, 0
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls++
				if tc.truncated {
					w.Header().Set("Content-Length", "100")
				}
				w.WriteHeader(tc.status)
				_, _ = io.WriteString(w, `{"items":[]}`)
			}))
			defer srv.Close()
			c := NewAuthorizedClient(context.Background(), nil, WithBaseURL(srv.URL), WithHTTPClient(srv.Client()), WithQuotaReads(),
				WithRequestDispatcher(func(ctx context.Context, req *http.Request, rt http.RoundTripper) (*http.Response, RequestAdmission, error) {
					resp, err := rt.RoundTrip(req)
					var a RequestAdmission
					if tc.admitted {
						a.AdmittedAt = time.Now()
					}
					if tc.finalized {
						a.Finalize = func(context.Context, string, int) error { finishes++; return nil }
					}
					return resp, a, err
				}))
			d, ids, err := c.GetVideosWithReturnedIDs(context.Background(), []string{"v1"})
			require.Error(t, err)
			require.False(t, ids.Complete)
			require.Empty(t, d)
			assert.Equal(t, 1, calls)
			if tc.finalized {
				assert.Equal(t, 1, finishes)
			} else {
				assert.Zero(t, finishes)
			}
			if tc.status == 206 || tc.truncated {
				var invalid *InvalidResponseError
				require.ErrorAs(t, err, &invalid)
				assert.Equal(t, OpVideosList, invalid.Operation())
			}
		})
	}
}

func TestGetVideosWithReturnedIDsAmbiguousContainers(t *testing.T) {
	for _, body := range []string{
		`{"items":[{"id":"v1","snippet":{},"snippet":{"publishedAt":"2024-01-01T00:00:00Z"}}]}`,
		`{"items":[{"id":"v1","snippet":{"publishedAt":"2024-01-01T00:00:00Z","publishedAt":"2024-01-01T00:00:00Z"}}]}`,
	} {
		c := newAccountedTestClient(t, func(w http.ResponseWriter, _ *http.Request) { _, _ = io.WriteString(w, body) })
		details, ids, err := c.GetVideosWithReturnedIDs(context.Background(), []string{"v1"})
		require.NoError(t, err)
		require.True(t, ids.Complete)
		require.Equal(t, []string{"v1"}, ids.IDs)
		require.Empty(t, details)
	}
}

func TestGetVideosWithReturnedIDsReadRecorder(t *testing.T) {
	for _, admitted := range []bool{false, true} {
		t.Run(fmt.Sprintf("admitted=%t", admitted), func(t *testing.T) {
			calls, recorded := 0, 0
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				calls++
				_, _ = io.WriteString(w, `{"items":[]}`)
			}))
			defer srv.Close()
			c := NewAuthorizedClient(context.Background(), nil, WithBaseURL(srv.URL), WithHTTPClient(srv.Client()),
				WithReadAccounting(nil, func(_ context.Context, attempt ReadAttempt) error {
					recorded++
					assert.Equal(t, OpVideosList, attempt.Operation)
					return nil
				}),
				WithRequestDispatcher(func(_ context.Context, req *http.Request, rt http.RoundTripper) (*http.Response, RequestAdmission, error) {
					resp, err := rt.RoundTrip(req)
					var admission RequestAdmission
					if admitted {
						admission.AdmittedAt = time.Now()
					}
					return resp, admission, err
				}))
			_, ids, err := c.GetVideosWithReturnedIDs(context.Background(), []string{"v1"})
			assert.Equal(t, 1, calls)
			if admitted {
				require.NoError(t, err)
				assert.True(t, ids.Complete)
				assert.Equal(t, 1, recorded)
			} else {
				require.Error(t, err)
				assert.False(t, ids.Complete)
				assert.Zero(t, recorded)
			}
		})
	}
}

func TestGetVideosWithReturnedIDsRequiresAccountingConfiguration(t *testing.T) {
	calls := 0
	c := newTestClient(t, func(w http.ResponseWriter, _ *http.Request) {
		calls++
		_, _ = io.WriteString(w, `{"items":[]}`)
	})
	_, ids, err := c.GetVideosWithReturnedIDs(context.Background(), []string{"v1"})
	require.Error(t, err)
	assert.False(t, ids.Complete)
	assert.Zero(t, calls)
}

func TestStrictMissingDispatcherNeverSends(t *testing.T) {
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls++
		_, _ = io.WriteString(w, `{"items":[]}`)
	}))
	defer srv.Close()
	c := NewAuthorizedClient(context.Background(), func(context.Context) (*oauth2.Token, error) {
		return &oauth2.Token{AccessToken: "test"}, nil
	}, WithBaseURL(srv.URL), WithHTTPClient(srv.Client()), WithQuotaReads())
	_, ids, err := c.GetVideosWithReturnedIDs(context.Background(), []string{"v1"})
	var re *ReadError
	require.ErrorAs(t, err, &re)
	require.Error(t, re.Accounting)
	assert.False(t, ids.Complete)
	assert.Zero(t, calls)
}

func TestStrictMissingAccountingPreservesPrimary(t *testing.T) {
	for _, tc := range []struct {
		name     string
		status   int
		admitted bool
	}{
		{"missing receipt", 200, false},
		{"missing finalizer", 200, true},
		{"invalid and missing finalizer", 206, true},
		{"provider and missing finalizer", 403, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(tc.status)
				if tc.status == 403 {
					_, _ = io.WriteString(w, `{"error":{"errors":[{"reason":"quotaExceeded"}]}}`)
				} else {
					_, _ = io.WriteString(w, `{"items":[]}`)
				}
			}))
			defer srv.Close()
			c := NewAuthorizedClient(context.Background(), nil, WithBaseURL(srv.URL), WithHTTPClient(srv.Client()), WithQuotaReads(),
				WithRequestDispatcher(func(_ context.Context, req *http.Request, rt http.RoundTripper) (*http.Response, RequestAdmission, error) {
					resp, err := rt.RoundTrip(req)
					var a RequestAdmission
					if tc.admitted {
						a.AdmittedAt = time.Now()
					}
					return resp, a, err
				}))
			_, ids, err := c.GetVideosWithReturnedIDs(context.Background(), []string{"v1"})
			var re *ReadError
			require.ErrorAs(t, err, &re)
			require.Error(t, re.Accounting)
			require.False(t, ids.Complete)
			if tc.status == 206 {
				var invalid *InvalidResponseError
				require.ErrorAs(t, err, &invalid)
				assert.Equal(t, OpVideosList, invalid.Operation())
			}
			if tc.status == 403 {
				var api *APIError
				require.ErrorAs(t, err, &api)
				assert.Equal(t, "quotaExceeded", api.Reason)
			}
		})
	}
}

func TestStrictMissingAccountingPreservesRenewalAndRefusal(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = io.WriteString(w, `{"error":{"errors":[{"reason":"authError"}]}}`)
	}))
	defer srv.Close()
	renewal := errors.New("renewal failed")
	c := NewAuthorizedClient(context.Background(), nil, WithBaseURL(srv.URL), WithHTTPClient(srv.Client()), WithQuotaReads(),
		WithRejectionObserver(func(context.Context, RequestAdmission) error { return renewal }),
		WithRequestDispatcher(func(_ context.Context, req *http.Request, rt http.RoundTripper) (*http.Response, RequestAdmission, error) {
			resp, err := rt.RoundTrip(req)
			return resp, RequestAdmission{AuthorizationID: "a", AdmittedAt: time.Now()}, err
		}))
	_, ids, err := c.GetVideosWithReturnedIDs(context.Background(), []string{"v1"})
	var re *ReadError
	require.ErrorAs(t, err, &re)
	require.Error(t, re.Accounting)
	assert.ErrorIs(t, re.Renewal, renewal)
	var api *APIError
	require.ErrorAs(t, err, &api)
	assert.Equal(t, http.StatusUnauthorized, api.Status)
	assert.False(t, ids.Complete)
}

func TestStrictPreAdmissionRefusalIsNotAccountingFailure(t *testing.T) {
	refusal := errors.New("refused before send")
	c := NewAuthorizedClient(context.Background(), nil, WithQuotaReads(),
		WithRequestDispatcher(func(context.Context, *http.Request, http.RoundTripper) (*http.Response, RequestAdmission, error) {
			return nil, RequestAdmission{}, refusal
		}))
	_, ids, err := c.GetVideosWithReturnedIDs(context.Background(), []string{"v1"})
	require.Error(t, err)
	var re *ReadError
	assert.False(t, errors.As(err, &re))
	assert.False(t, ids.Complete)
}

type notifyingBody struct {
	io.ReadCloser
	started chan struct{}
}

func (b notifyingBody) Read(p []byte) (int, error) {
	select {
	case b.started <- struct{}{}:
	default:
	}
	return b.ReadCloser.Read(p)
}

func TestStrictCanceledBodyPreservesInterruption(t *testing.T) {
	started := make(chan struct{})
	reading := make(chan struct{}, 1)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = io.WriteString(w, `{"items":[`)
		w.(http.Flusher).Flush()
		close(started)
		<-r.Context().Done()
	}))
	defer srv.Close()
	finalized := 0
	c := NewAuthorizedClient(context.Background(), nil, WithBaseURL(srv.URL), WithHTTPClient(srv.Client()), WithQuotaReads(),
		WithRequestDispatcher(func(_ context.Context, req *http.Request, rt http.RoundTripper) (*http.Response, RequestAdmission, error) {
			resp, err := rt.RoundTrip(req)
			if resp != nil {
				resp.Body = notifyingBody{ReadCloser: resp.Body, started: reading}
			}
			return resp, RequestAdmission{AdmittedAt: time.Now(), Finalize: func(context.Context, string, int) error { finalized++; return nil }}, err
		}))
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { _, _, err := c.GetVideosWithReturnedIDs(ctx, []string{"v1"}); done <- err }()
	<-started
	<-reading
	cancel()
	err := <-done
	require.ErrorIs(t, err, context.Canceled)
	var invalid *InvalidResponseError
	assert.False(t, errors.As(err, &invalid))
	assert.Equal(t, 1, finalized)
}

func TestStrictCanceledRejectionRetainsIndependentFacts(t *testing.T) {
	for _, status := range []int{http.StatusUnauthorized, http.StatusForbidden} {
		t.Run(fmt.Sprint(status), func(t *testing.T) {
			started := make(chan struct{})
			reading := make(chan struct{}, 1)
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(status)
				_, _ = io.WriteString(w, `{"error":{"errors":[{"reason":"authError"`)
				w.(http.Flusher).Flush()
				close(started)
				<-r.Context().Done()
			}))
			defer srv.Close()
			accounting, renewal := errors.New("accounting"), errors.New("renewal")
			finalized, observed := 0, 0
			c := NewAuthorizedClient(context.Background(), nil, WithBaseURL(srv.URL), WithHTTPClient(srv.Client()), WithQuotaReads(),
				WithRejectionObserver(func(context.Context, RequestAdmission) error {
					observed++
					if finalized != 1 {
						t.Error("rejection observed before finalization")
					}
					return renewal
				}),
				WithRequestDispatcher(func(_ context.Context, req *http.Request, rt http.RoundTripper) (*http.Response, RequestAdmission, error) {
					resp, err := rt.RoundTrip(req)
					if resp != nil {
						resp.Body = notifyingBody{ReadCloser: resp.Body, started: reading}
					}
					return resp, RequestAdmission{AuthorizationID: "a", AdmittedAt: time.Now(), Finalize: func(context.Context, string, int) error {
						finalized++
						return accounting
					}}, err
				}))
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			type result struct {
				ids VideoIDs
				err error
			}
			done := make(chan result, 1)
			go func() { _, ids, err := c.GetVideosWithReturnedIDs(ctx, []string{"v1"}); done <- result{ids, err} }()
			<-started
			<-reading
			cancel()
			got := <-done
			var re *ReadError
			require.ErrorAs(t, got.err, &re)
			assert.ErrorIs(t, re.Interruption, context.Canceled)
			assert.ErrorIs(t, re.Accounting, accounting)
			var api *APIError
			require.ErrorAs(t, got.err, &api)
			assert.Equal(t, status, api.Status)
			assert.False(t, api.Complete)
			assert.False(t, got.ids.Complete)
			assert.Equal(t, 1, finalized)
			if status == http.StatusUnauthorized {
				assert.Equal(t, 1, observed)
				assert.ErrorIs(t, re.Renewal, renewal)
			} else {
				assert.Zero(t, observed)
				assert.NoError(t, re.Renewal)
			}
		})
	}
}

func TestStrictInvalidUTF8RouteInput(t *testing.T) {
	body := []byte(`{"items":[{"id":"v1","snippet":{"publishedAt":"2024-01-01T00:00:00Z","description":"X"},"contentDetails":{"duration":"PT0S"}}]}`)
	body = bytes.Replace(body, []byte(`"X"`), []byte{'"', 0xff, '"'}, 1)
	c := newAccountedTestClient(t, func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write(body) })
	details, ids, err := c.GetVideosWithReturnedIDs(context.Background(), []string{"v1"})
	require.NoError(t, err)
	require.True(t, ids.Complete)
	require.Equal(t, []string{"v1"}, ids.IDs)
	require.Len(t, details, 1)
	assert.False(t, details[0].Present.Description)
	assert.True(t, details[0].Present.Duration)
}

func TestGetVideosLegacyResponseBehaviorUnchanged(t *testing.T) {
	c := newTestClient(t, func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusPartialContent)
		_, _ = io.WriteString(w, `{"items":[{"id":"v1","snippet":{"publishedAt":"2024-01-01T00:00:00Z"},"contentDetails":{"duration":"P1D"}}]}`)
	})
	details, err := c.GetVideos(context.Background(), []string{"v1"})
	require.NoError(t, err)
	require.Len(t, details, 1)
	assert.Equal(t, "v1", details[0].ID)
	assert.Zero(t, details[0].DurationSeconds)
	assert.Equal(t, VideoDetailPresence{}, details[0].Present)
}

func TestGetVideosWithReturnedIDsAccounting(t *testing.T) {
	for _, fail := range []bool{false, true} {
		t.Run(fmt.Sprintf("failure=%t", fail), func(t *testing.T) {
			calls, admitted, finalized := 0, 0, 0
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls++
				_, _ = io.WriteString(w, `{"items":[]}`)
			}))
			defer srv.Close()
			c := NewAuthorizedClient(context.Background(), nil, WithBaseURL(srv.URL), WithHTTPClient(srv.Client()),
				WithQuotaReads(), WithRequestDispatcher(func(ctx context.Context, req *http.Request, transport http.RoundTripper) (*http.Response, RequestAdmission, error) {
					admitted++
					resp, err := transport.RoundTrip(req)
					return resp, RequestAdmission{AdmittedAt: time.Now(), Finalize: func(_ context.Context, category string, status int) error {
						finalized++
						if category != "success" || status != 200 {
							t.Errorf("category=%s status=%d", category, status)
						}
						if fail {
							return errors.New("secret accounting failure")
						}
						return nil
					}}, err
				}))
			details, returned, err := c.GetVideosWithReturnedIDs(context.Background(), []string{"v1"})
			if calls != 1 || admitted != 1 || finalized != 1 {
				t.Fatalf("calls=%d admitted=%d finalized=%d", calls, admitted, finalized)
			}
			if fail {
				var readErr *ReadError
				if !errors.As(err, &readErr) || readErr.Accounting == nil || returned.Complete || details != nil || strings.Contains(err.Error(), "secret") {
					t.Fatalf("details=%v returned=%v err=%v", details, returned, err)
				}
			} else if err != nil || !returned.Complete {
				t.Fatalf("returned=%v err=%v", returned, err)
			}
		})
	}
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
