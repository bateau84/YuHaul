//go:build integration

package e2e

import (
	"fmt"
	"net"
	"net/http"
	"testing"
)

// loopbackOnlyTransport fails the test when any request targets a non-loopback
// host, so a mis-wired fixture can never reach a live provider.
type loopbackOnlyTransport struct {
	t    testing.TB
	base http.RoundTripper
}

func (l loopbackOnlyTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	host := req.URL.Hostname()
	if ip := net.ParseIP(host); host != "localhost" && (ip == nil || !ip.IsLoopback()) {
		err := fmt.Errorf("e2e isolation: non-loopback request to host %q", host)
		l.t.Errorf("%v", err)
		return nil, err
	}
	return l.base.RoundTrip(req)
}

func guardTransport(t testing.TB, base http.RoundTripper) http.RoundTripper {
	if base == nil {
		base = http.DefaultTransport
	}
	return loopbackOnlyTransport{t: t, base: base}
}

func guardClient(t testing.TB, c *http.Client) *http.Client {
	guarded := *c
	guarded.Transport = guardTransport(t, c.Transport)
	return &guarded
}
