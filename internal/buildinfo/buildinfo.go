// Package buildinfo exposes the identity of the running binary. The values are
// injected at link time (see the Dockerfile) so an operator can tell which
// build a deployment is actually running.
package buildinfo

import (
	"encoding/json"
	"net/http"
)

// Injected with -ldflags "-X github.com/bateau84/YuHaul/internal/buildinfo.<Name>=<value>".
// They stay "unknown" for a plain `go build`.
var (
	Version = "unknown"
	Commit  = "unknown"
	BuiltAt = "unknown"
)

// Info is the JSON body served by GET /api/version.
type Info struct {
	Version string `json:"version"`
	Commit  string `json:"commit"`
	BuiltAt string `json:"built_at"`
}

// Current returns the injected build identity.
func Current() Info {
	return Info{Version: Version, Commit: Commit, BuiltAt: BuiltAt}
}

// Handler serves the build identity. It is never cacheable: its purpose is to
// show which build is running right now.
func Handler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Cache-Control", "no-store")
		_ = json.NewEncoder(w).Encode(Current())
	})
}
