package youtube

import (
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"golang.org/x/oauth2"
)

func TestAuthorizedTransportSingleAttemptTLS(t *testing.T) {
	var calls atomic.Int32
	var connections atomic.Int32
	srv := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if r.ProtoMajor != 1 {
			t.Errorf("protocol = %s, want HTTP/1.1", r.Proto)
		}
		if r.Header.Get("Authorization") != "Bearer access" {
			t.Errorf("missing bearer authorization")
		}
		if r.URL.Query().Get("pageToken") == "drop" {
			conn, _, err := w.(http.Hijacker).Hijack()
			if err != nil {
				t.Error(err)
				return
			}
			conn.Close()
			return
		}
		if r.URL.Query().Get("pageToken") == "redirect" {
			http.Redirect(w, r, "/other", http.StatusFound)
			return
		}
		io.WriteString(w, `{"items":[]}`)
	}))
	srv.EnableHTTP2 = true
	srv.Config.ConnState = func(_ net.Conn, s http.ConnState) {
		if s == http.StateNew {
			connections.Add(1)
		}
	}
	srv.StartTLS()
	defer srv.Close()
	c := NewAuthorizedClient(context.Background(), func(context.Context) (*oauth2.Token, error) { return &oauth2.Token{AccessToken: "access"}, nil }, WithBaseURL(srv.URL), WithHTTPClient(srv.Client()))
	if _, err := c.ListSubscriptions(context.Background(), ""); err != nil {
		t.Fatal(err)
	}
	if _, err := c.ListSubscriptions(context.Background(), "drop"); err == nil {
		t.Fatal("expected dropped connection failure")
	}
	if _, err := c.ListSubscriptions(context.Background(), "redirect"); err == nil {
		t.Fatal("expected redirect rejection")
	}
	if got := calls.Load(); got != 3 {
		t.Fatalf("provider requests = %d, want 3", got)
	}
	if got := connections.Load(); got != 3 {
		t.Fatalf("connections = %d, want 3", got)
	}
}

type replayingTransport struct{ calls *int }

func (t replayingTransport) RoundTrip(*http.Request) (*http.Response, error) {
	*t.calls++
	return nil, errors.New("unexpected dispatch")
}
func TestAuthorizedRejectsUncertifiedTransport(t *testing.T) {
	calls := 0
	dispatched := 0
	c := NewAuthorizedClient(context.Background(), nil, WithHTTPClient(&http.Client{Transport: replayingTransport{&calls}}), WithRequestDispatcher(func(context.Context, *http.Request, http.RoundTripper) (*http.Response, RequestAdmission, error) {
		dispatched++
		return nil, RequestAdmission{}, nil
	}))
	if _, err := c.ListSubscriptions(context.Background(), ""); err == nil {
		t.Fatal("expected unsupported transport error")
	}
	if calls != 0 || dispatched != 0 {
		t.Fatalf("transport calls %d dispatch calls %d", calls, dispatched)
	}
}
