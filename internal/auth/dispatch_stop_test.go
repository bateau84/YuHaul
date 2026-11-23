package auth

import (
	"context"
	"database/sql"
	"errors"
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/bateau84/YuHaul/internal/quota"
	"github.com/bateau84/YuHaul/internal/store/storetest"
)

func seedStopRun(t *testing.T, db *sql.DB, stored string) int64 {
	t.Helper()
	res, err := db.Exec(`INSERT INTO run (started_at, run_type, status) VALUES (?, 'manual', 'running')`, time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		t.Fatal(err)
	}
	if stored != "" {
		if _, err := db.Exec(`UPDATE run SET dispatch_stop = ? WHERE id = ?`, stored, id); err != nil {
			t.Fatal(err)
		}
	}
	return id
}

func ledgerRows(t *testing.T, db *sql.DB) int {
	t.Helper()
	var n int
	if err := db.QueryRow(`SELECT COUNT(*) FROM quota_ledger`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

// S3: with each level set, a guarded read and an insert through the real
// dispatchScoped make zero requests, zero renewals and zero ledger rows, and
// return the mapped error. The access token is expired, so an unguarded
// dispatch would renew first: a renewal count of zero proves R0 runs before
// Resolve.
func TestDispatchRefusesRunDispatchStopBeforeRenewal(t *testing.T) {
	type tc struct {
		name   string
		stored string
		quota  bool
		// closeDB makes the stop read fail.
		closeDB bool
		check   func(error) bool
		fence   bool // the error must classify as a pre-admission authorization refusal
	}
	cases := []tc{
		{name: "authorization", stored: "authorization", check: func(e error) bool { return errors.Is(e, ErrRunAuthorizationStopped) }, fence: true},
		{name: "quota", quota: true, check: func(e error) bool { return errors.Is(e, ErrQuotaRefused) && !errors.Is(e, ErrQuotaAccounting) }},
		{name: "accounting_or_integrity", stored: "accounting_or_integrity", check: func(e error) bool { return errors.Is(e, ErrQuotaAccounting) }},
		{name: "read_error", closeDB: true, check: func(e error) bool { return errors.Is(e, ErrQuotaAccounting) }},
	}
	for _, c := range cases {
		for _, op := range []string{"read", "insert"} {
			t.Run(c.name+"/"+op, func(t *testing.T) {
				ctx := context.Background()
				db := storetest.Open(t)
				ledger := quota.New(db)
				runID := seedStopRun(t, db, c.stored)
				rctx := quota.WithRun(ctx, runID)
				if c.quota {
					h, err := ledger.Acquire(rctx, quota.OpPlaylistItemsInsert)
					if err != nil || h == nil {
						t.Fatalf("seed acquire: %v, %v", h, err)
					}
					if err := h.Admit(rctx); err != nil {
						t.Fatal(err)
					}
					if err := h.Finalize(rctx, sql.NullInt64{Int64: runID, Valid: true}, quota.OutcomeQuota, sql.NullInt64{Int64: 403, Valid: true}); err != nil {
						t.Fatal(err)
					}
				}
				rowsBefore := ledgerRows(t, db)

				tokenStore := newTestStore(t, newTestKey(t))
				authorityTestSeed(t, tokenStore, time.Now().Add(-time.Hour)) // expired: dispatch would renew
				var renewals, sends atomic.Int32
				s := authorityTestService(t, tokenStore, func(*http.Request) (*http.Response, error) {
					renewals.Add(1)
					return authorityTestResponse(`{"access_token":"renewed","token_type":"Bearer","expires_in":3600}`), nil
				})
				binding, err := s.Bind(ctx)
				if err != nil {
					t.Fatal(err)
				}
				req, err := http.NewRequestWithContext(ctx, http.MethodGet, "https://example.com/", nil)
				if err != nil {
					t.Fatal(err)
				}
				transport := authorityTestTransport(func(*http.Request) (*http.Response, error) {
					sends.Add(1)
					return authorityTestResponse("{}"), nil
				})
				if c.closeDB {
					if err := db.Close(); err != nil {
						t.Fatal(err)
					}
				}

				var resp *http.Response
				var receipt AdmissionReceipt
				var hold *quota.Hold
				if op == "read" {
					resp, receipt, hold, err = s.DispatchWithQuota(rctx, binding, req, transport, ledger, quota.OpVideosList)
				} else {
					scope := func(ctx context.Context, transition func() error) error { return transition() }
					resp, receipt, hold, err = s.DispatchWithQuotaScoped(rctx, binding, req, transport, ledger, quota.OpPlaylistItemsInsert, scope)
				}
				if resp != nil {
					resp.Body.Close()
				}
				if err == nil || !c.check(err) {
					t.Fatalf("err = %v, want the %s mapping", err, c.name)
				}
				if c.fence && !IsPreAdmissionAuthorizationRefusal(err) {
					t.Fatalf("%v is not a pre-admission authorization refusal", err)
				}
				if !c.fence && IsPreAdmissionAuthorizationRefusal(err) {
					t.Fatalf("%v must not classify as an authorization refusal", err)
				}
				if hold != nil || !receipt.AdmittedAt.IsZero() || receipt.AuthorizationID != "" {
					t.Fatalf("a refused dispatch admitted something: hold=%v receipt=%+v", hold, receipt)
				}
				if renewals.Load() != 0 || sends.Load() != 0 {
					t.Fatalf("renewals=%d sends=%d, want 0 and 0", renewals.Load(), sends.Load())
				}
				if !c.closeDB {
					if got := ledgerRows(t, db); got != rowsBefore {
						t.Fatalf("ledger rows %d -> %d, want unchanged", rowsBefore, got)
					}
				} else if got, _ := quota.New(storetest.Open(t)).RunDispatchStop(rctx); got != quota.StopAccountingOrIntegrity {
					t.Fatalf("a stop read error must latch accounting_or_integrity for the execution, got %v", got)
				}
			})
		}
	}
}

// S3: another run, and an unscoped call, in the same bucket are admitted while
// a stopped run is refused.
func TestDispatchRunDispatchStopIsPerRun(t *testing.T) {
	ctx := context.Background()
	db := storetest.Open(t)
	ledger := quota.New(db)
	stopped := seedStopRun(t, db, "authorization")
	other := seedStopRun(t, db, "")

	tokenStore := newTestStore(t, newTestKey(t))
	authorityTestSeed(t, tokenStore, time.Now().Add(time.Hour))
	s := authorityTestService(t, tokenStore, nil)
	binding, err := s.Bind(ctx)
	if err != nil {
		t.Fatal(err)
	}
	var sends atomic.Int32
	transport := authorityTestTransport(func(*http.Request) (*http.Response, error) {
		sends.Add(1)
		return authorityTestResponse("{}"), nil
	})
	dispatch := func(c context.Context) error {
		req, err := http.NewRequestWithContext(c, http.MethodGet, "https://example.com/", nil)
		if err != nil {
			t.Fatal(err)
		}
		resp, _, hold, err := s.DispatchWithQuota(c, binding, req, transport, ledger, quota.OpVideosList)
		if resp != nil {
			resp.Body.Close()
		}
		if hold != nil {
			if ferr := hold.Finalize(c, sql.NullInt64{}, quota.OutcomeSuccess, sql.NullInt64{Int64: 200, Valid: true}); ferr != nil {
				t.Fatal(ferr)
			}
		}
		return err
	}
	if err := dispatch(quota.WithRun(ctx, stopped)); !errors.Is(err, ErrRunAuthorizationStopped) {
		t.Fatalf("stopped run: %v", err)
	}
	if err := dispatch(quota.WithRun(ctx, other)); err != nil {
		t.Fatalf("other run: %v", err)
	}
	if err := dispatch(ctx); err != nil {
		t.Fatalf("unscoped: %v", err)
	}
	if sends.Load() != 2 {
		t.Fatalf("sends = %d, want 2 (other run and unscoped)", sends.Load())
	}
}

// W3 at the admission seam: a local accounting fault during admission latches
// accounting_or_integrity for the run's execution, so the next guarded request
// of that run makes no renewal, hold or send even though the store is healthy
// again. Another run is still admitted.
func TestDispatchAccountingFaultStopsLaterRequestsOfTheRun(t *testing.T) {
	ctx := context.Background()
	db := storetest.Open(t)
	ledger := quota.New(db)
	runID := seedStopRun(t, db, "")
	other := seedStopRun(t, db, "")
	rctx := quota.WithRun(ctx, runID)

	tokenStore := newTestStore(t, newTestKey(t))
	authorityTestSeed(t, tokenStore, time.Now().Add(time.Hour))
	s := authorityTestService(t, tokenStore, nil)
	binding, err := s.Bind(ctx)
	if err != nil {
		t.Fatal(err)
	}
	var sends atomic.Int32
	transport := authorityTestTransport(func(*http.Request) (*http.Response, error) {
		sends.Add(1)
		return authorityTestResponse("{}"), nil
	})
	request := func(c context.Context) *http.Request {
		req, err := http.NewRequestWithContext(c, http.MethodGet, "https://example.com/", nil)
		if err != nil {
			t.Fatal(err)
		}
		return req
	}

	// The writer scope refuses with a local storage fault: admission accounting fails.
	faulty := func(context.Context, func() error) error { return errors.New("writer unavailable") }
	resp, _, hold, err := s.DispatchWithQuotaScoped(rctx, binding, request(rctx), transport, ledger, quota.OpPlaylistItemsInsert, faulty)
	if resp != nil {
		resp.Body.Close()
	}
	if !errors.Is(err, ErrQuotaAccounting) || hold != nil || sends.Load() != 0 {
		t.Fatalf("first dispatch: err=%v hold=%v sends=%d; want an accounting fault, no hold, no send", err, hold, sends.Load())
	}

	// The store is healthy again; the latch alone refuses the next request.
	resp, _, hold, err = s.DispatchWithQuota(rctx, binding, request(rctx), transport, ledger, quota.OpVideosList)
	if resp != nil {
		resp.Body.Close()
	}
	if !errors.Is(err, ErrQuotaAccounting) || hold != nil || sends.Load() != 0 {
		t.Fatalf("second dispatch: err=%v hold=%v sends=%d; want the accounting stop, no hold, no send", err, hold, sends.Load())
	}
	if got := ledgerRows(t, db); got != 0 {
		t.Fatalf("ledger rows = %d, want 0", got)
	}

	octx := quota.WithRun(ctx, other)
	resp, _, hold, err = s.DispatchWithQuota(octx, binding, request(octx), transport, ledger, quota.OpVideosList)
	if resp != nil {
		resp.Body.Close()
	}
	if err != nil || hold == nil || sends.Load() != 1 {
		t.Fatalf("another run: err=%v hold=%v sends=%d; want admitted", err, hold, sends.Load())
	}
	if err := hold.Finalize(octx, sql.NullInt64{}, quota.OutcomeSuccess, sql.NullInt64{Int64: 200, Valid: true}); err != nil {
		t.Fatal(err)
	}
}

// S3 (critic advisory): R0 guards dispatchScoped when a ledger is present. The
// paths that bypass it are an unguarded token read (ActiveToken), the
// ledger-less Dispatch, and the legacy discovery Run. This test pins the exact
// set of non-test callers so a new one cannot appear without being reviewed.
// None is reachable for a production run: cmd/server wires an auth service, so
// Pipeline.Execute binds a production run (manual or scheduled) and takes the
// bound branch, and the executor only calls ActiveToken when it has no binding
// or a test factory (WithYouTubeFactory, never set by cmd/server).
func TestUnguardedAuthorizationPathsHaveNoProductionCaller(t *testing.T) {
	root := filepath.Join("..", "..")
	want := map[string]int{
		// legacy Run: reached only when the pipeline has no execution binding.
		"internal/discovery/engine.go: ActiveToken":    1,
		"internal/pipeline/executor.go: ActiveToken":   1,
		"internal/pipeline/pipeline.go: discovery.Run": 1,
	}
	got := map[string]int{}
	fset := token.NewFileSet()
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			switch d.Name() {
			case "node_modules", ".git", "web", "e2e":
				return fs.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		rel, _ := filepath.Rel(root, path)
		rel = filepath.ToSlash(rel)
		if strings.HasPrefix(rel, "internal/auth/") {
			return nil
		}
		src, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		f, err := parser.ParseFile(fset, path, src, 0)
		if err != nil {
			return err
		}
		ast.Inspect(f, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok {
				return true
			}
			sel, ok := call.Fun.(*ast.SelectorExpr)
			if !ok {
				return true
			}
			switch sel.Sel.Name {
			case "ActiveToken":
				got[rel+": ActiveToken"]++
			case "Dispatch":
				got[rel+": Dispatch"]++
			case "Run":
				if x, ok := sel.X.(*ast.SelectorExpr); ok && x.Sel.Name == "discovery" {
					got[rel+": discovery.Run"]++
				}
			}
			return true
		})
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	var diff []string
	for k, v := range got {
		if want[k] != v {
			diff = append(diff, k)
		}
	}
	for k := range want {
		if _, ok := got[k]; !ok {
			diff = append(diff, k)
		}
	}
	sort.Strings(diff)
	if len(diff) > 0 {
		t.Fatalf("callers of the unguarded authorization paths changed: %v\n got  %v\n want %v\nReview each against R0 (run-initiation ARS-005 section 6.1.3) before extending this list.", diff, got, want)
	}
}
