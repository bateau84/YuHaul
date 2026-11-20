package metadata

import (
	"context"
	"database/sql"
	"testing"
)

// TestHooks exposes the transaction-boundary hooks to external tests.
type TestHooks struct {
	BeforeCommit func(ReconcileStats) error
	Rollback     func(ctx context.Context, c *sql.Conn) error
	Commit       func(ctx context.Context, c *sql.Conn) error
}

// SetTestHooks installs hooks until the test ends. Tests that use it must not
// run in parallel.
func SetTestHooks(t testing.TB, h TestHooks) {
	t.Helper()
	hooks = testHooks{beforeCommit: h.BeforeCommit, rollback: h.Rollback, commit: h.Commit}
	t.Cleanup(func() { hooks = testHooks{} })
}
