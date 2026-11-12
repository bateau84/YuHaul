package state

import (
	"context"
	"database/sql"
	"errors"
	"testing"
)

// fixture seeds runs and videos on a real migrated SQLite database.
type finalFixture struct {
	t  *testing.T
	db *sql.DB
}

func newFinalFixture(t *testing.T) *finalFixture {
	t.Helper()
	db := openTestDB(t)
	f := &finalFixture{t: t, db: db}
	f.exec(`INSERT INTO route(name,evaluation_order,condition_composition) VALUES('r1',1,'AND'),('r2',2,'AND'),('r3',3,'AND')`)
	return f
}

func (f *finalFixture) exec(q string, args ...any) int64 {
	f.t.Helper()
	res, err := f.db.Exec(q, args...)
	if err != nil {
		f.t.Fatalf("%s: %v", q, err)
	}
	id, _ := res.LastInsertId()
	return id
}

func (f *finalFixture) run(runType string) int64 {
	return f.exec(`INSERT INTO run(started_at,run_type,status) VALUES('2026-11-01T00:00:00Z',?,'completed')`, runType)
}

// video inserts a video; outcome "" stores NULL.
func (f *finalFixture) video(ext, state, outcome string) int64 {
	if outcome == "" {
		return f.exec(`INSERT INTO video(video_id,channel_id,processing_state) VALUES(?,'c',?)`, ext, state)
	}
	return f.exec(`INSERT INTO video(video_id,channel_id,processing_state,final_outcome) VALUES(?,'c',?,?)`, ext, state, outcome)
}

func (f *finalFixture) decision(run, video, route int64, decision string) int64 {
	return f.exec(`INSERT INTO route_decision(video_id,run_id,route_id,evaluation_order,decision) VALUES(?,?,?,?,?)`,
		video, run, route, route, decision)
}

func (f *finalFixture) action(run, video, decision int64, target, outcome string) {
	f.exec(`INSERT INTO executed_action(route_decision_id,run_id,video_id,action_type,target_playlist_id,outcome) VALUES(?,?,?,'playlist_insert',?,?)`,
		decision, run, video, target, outcome)
}

func (f *finalFixture) deferred(video, run int64, status string, retryRun any) {
	f.exec(`INSERT INTO deferred_item(video_id,run_id,deferral_reason,status,retry_run_id) VALUES(?,?,'quota_exhausted',?,?)`, video, run, status, retryRun)
}

func (f *finalFixture) errorEntry(video, run int64, status string, retryRun any) {
	f.exec(`INSERT INTO error_entry(video_id,run_id,error_category,error_message,status,retry_run_id) VALUES(?,?,'runtime','m',?,?)`, video, run, status, retryRun)
}

// F3: classification on real migrated SQLite.
func TestClassifyVideoTx(t *testing.T) {
	f := newFinalFixture(t)
	ctx := context.Background()
	prod, sim := f.run("scheduled"), f.run("dry_run")

	partly := func(ext, state, outcome string, runID int64) int64 {
		v := f.video(ext, state, outcome)
		f.action(runID, v, f.decision(runID, v, 1, "matched"), "PL", "succeeded")
		return v
	}
	cases := []struct {
		name    string
		id      int64
		class   VideoClass
		outcome VideoFinalOutcome
	}{
		{"accepted", f.video("a", "completed", "accepted"), VideoClassFinal, "accepted"},
		{"rejected", f.video("b", "completed", "rejected"), VideoClassFinal, "rejected"},
		{"excluded", f.video("c", "completed", "excluded"), VideoClassFinal, "excluded"},
		{"unavailable", f.video("d", "completed", "unavailable_on_youtube"), VideoClassFinal, "unavailable_on_youtube"},
		{"completed state, not_yet_evaluated outcome is not final", f.video("e", "completed", "not_yet_evaluated"), VideoClassOpen, "not_yet_evaluated"},
		{"completed state, NULL outcome is not final", f.video("e2", "completed", ""), VideoClassOpen, ""},
		{"deferred with production success", partly("f", "deferred", "deferred", prod), VideoClassPartlyDone, "deferred"},
		{"failed with production success", partly("g", "failed", "failed", prod), VideoClassPartlyDone, "failed"},
		{"evaluated with production success", partly("h", "evaluated", "not_yet_evaluated", prod), VideoClassPartlyDone, "not_yet_evaluated"},
		{"success in a simulation only", partly("i", "deferred", "deferred", sim), VideoClassOpen, "deferred"},
		{"production attempt that did not succeed", func() int64 {
			v := f.video("j", "deferred", "deferred")
			f.action(prod, v, f.decision(prod, v, 1, "matched"), "PL", "failed")
			return v
		}(), VideoClassOpen, "deferred"},
		{"no success", f.video("k", "deferred", "deferred"), VideoClassOpen, "deferred"},
		{"final wins over a production success", partly("l", "completed", "accepted", prod), VideoClassFinal, "accepted"},
	}
	for _, c := range cases {
		class, outcome, err := ClassifyVideoTx(ctx, f.db, c.id)
		if err != nil {
			t.Fatalf("%s: %v", c.name, err)
		}
		if class != c.class || outcome != c.outcome {
			t.Errorf("%s: got (%s, %q), want (%s, %q)", c.name, class, outcome, c.class, c.outcome)
		}
	}
	if _, _, err := ClassifyVideoTx(ctx, f.db, 9999); err == nil {
		t.Error("an unknown video must return an error")
	}
}

// F5: one seed per RoutableByRun rule, plus a video held only by the run's own
// retrying row (kept).
func TestRoutableByRun(t *testing.T) {
	f := newFinalFixture(t)
	r0, r1, run := f.run("scheduled"), f.run("scheduled"), f.run("manual")
	sim := f.run("backtest")

	type seed struct {
		name string
		id   int64
		want bool
	}
	var seeds []seed
	add := func(name string, want bool, v int64) { seeds = append(seeds, seed{name, v, want}) }

	add("open video", true, f.video("open", "evaluated", "not_yet_evaluated"))
	add("final video", false, f.video("final", "completed", "rejected"))

	other := f.video("other-run-success", "deferred", "deferred")
	f.action(r0, other, f.decision(r0, other, 1, "matched"), "PL", "succeeded")
	add("production success under another run's decision", false, other)

	own := f.video("own-run-success", "deferred", "deferred")
	f.action(run, own, f.decision(run, own, 1, "matched"), "PL", "succeeded")
	add("production success under this run's own decision", true, own)

	simOnly := f.video("sim-success", "deferred", "deferred")
	f.action(sim, simOnly, f.decision(sim, simOnly, 1, "matched"), "PL", "succeeded")
	add("success in a simulation under another run", true, simOnly)

	pend := f.video("pending", "deferred", "deferred")
	f.deferred(pend, r0, "pending", nil)
	add("pending deferred item", false, pend)

	retryOther := f.video("retrying-other", "deferred", "deferred")
	f.deferred(retryOther, r0, "retrying", r1)
	add("deferred item retrying under another run", false, retryOther)

	retryNil := f.video("retrying-nil", "deferred", "deferred")
	f.deferred(retryNil, r0, "retrying", nil)
	add("deferred item retrying with no owner", false, retryNil)

	retryOwn := f.video("retrying-own", "deferred", "deferred")
	f.deferred(retryOwn, r0, "retrying", run)
	add("video held only by the run's own retrying deferred item", true, retryOwn)

	done := f.video("completed-item", "deferred", "deferred")
	f.deferred(done, r0, "completed", nil)
	add("completed deferred item", true, done)

	active := f.video("active-error", "failed", "failed")
	f.errorEntry(active, r0, "active", nil)
	add("active error entry", false, active)

	errOther := f.video("retrying-error-other", "failed", "failed")
	f.errorEntry(errOther, r0, "retrying", r1)
	add("error entry retrying under another run", false, errOther)

	errNil := f.video("retrying-error-nil", "failed", "failed")
	f.errorEntry(errNil, r0, "retrying", nil)
	add("error entry retrying with no owner", false, errNil)

	errOwn := f.video("retrying-error-own", "failed", "failed")
	f.errorEntry(errOwn, r0, "retrying", run)
	add("video held only by the run's own retrying error entry", true, errOwn)

	resolved := f.video("resolved-error", "failed", "failed")
	f.errorEntry(resolved, r0, "dismissed", nil)
	add("dismissed error entry", true, resolved)

	for _, s := range seeds {
		var got bool
		err := f.db.QueryRow(`SELECT `+RoutableByRun+` FROM video v WHERE v.id = :id`,
			sql.Named("run", run), sql.Named("id", s.id)).Scan(&got)
		if err != nil {
			t.Fatalf("%s: %v", s.name, err)
		}
		if got != s.want {
			t.Errorf("%s: RoutableByRun = %v, want %v", s.name, got, s.want)
		}
	}
}

// F4 (OriginalDecisionTx parts).
func TestOriginalDecisionTx(t *testing.T) {
	ctx := context.Background()

	t.Run("one production run returns its matched decisions in order", func(t *testing.T) {
		f := newFinalFixture(t)
		r := f.run("scheduled")
		v := f.video("v", "deferred", "deferred")
		d2 := f.decision(r, v, 2, "matched")
		d1 := f.decision(r, v, 1, "matched")
		f.decision(r, v, 3, "skipped")
		f.action(r, v, d1, "PL1", "succeeded")
		od, err := OriginalDecisionTx(ctx, f.db, v)
		if err != nil {
			t.Fatal(err)
		}
		want := []RouteDecisionRef{{d1, 1}, {d2, 2}}
		if od.RunID != r || len(od.Matched) != 2 || od.Matched[0] != want[0] || od.Matched[1] != want[1] {
			t.Fatalf("got %+v, want run %d matched %+v", od, r, want)
		}
	})
	t.Run("two runs with successes is unavailable", func(t *testing.T) {
		f := newFinalFixture(t)
		r1, r2 := f.run("scheduled"), f.run("manual")
		v := f.video("v", "deferred", "deferred")
		f.action(r1, v, f.decision(r1, v, 1, "matched"), "PL1", "succeeded")
		f.action(r2, v, f.decision(r2, v, 2, "matched"), "PL2", "succeeded")
		if _, err := OriginalDecisionTx(ctx, f.db, v); !errors.Is(err, ErrOriginalDecisionUnavailable) {
			t.Fatalf("err = %v, want ErrOriginalDecisionUnavailable", err)
		}
	})
	t.Run("R0 not production is unavailable", func(t *testing.T) {
		f := newFinalFixture(t)
		sim := f.run("dry_run")
		v := f.video("v", "deferred", "deferred")
		f.action(sim, v, f.decision(sim, v, 1, "matched"), "PL1", "succeeded")
		if _, err := OriginalDecisionTx(ctx, f.db, v); !errors.Is(err, ErrOriginalDecisionUnavailable) {
			t.Fatalf("err = %v, want ErrOriginalDecisionUnavailable", err)
		}
	})
	t.Run("no production success is unavailable", func(t *testing.T) {
		f := newFinalFixture(t)
		r := f.run("scheduled")
		v := f.video("v", "deferred", "deferred")
		f.decision(r, v, 1, "matched")
		if _, err := OriginalDecisionTx(ctx, f.db, v); !errors.Is(err, ErrOriginalDecisionUnavailable) {
			t.Fatalf("err = %v, want ErrOriginalDecisionUnavailable", err)
		}
	})
	t.Run("empty matched is unavailable", func(t *testing.T) {
		f := newFinalFixture(t)
		r := f.run("scheduled")
		v := f.video("v", "deferred", "deferred")
		// The success references a decision that is not 'matched'.
		f.action(r, v, f.decision(r, v, 1, "skipped"), "PL1", "succeeded")
		if _, err := OriginalDecisionTx(ctx, f.db, v); !errors.Is(err, ErrOriginalDecisionUnavailable) {
			t.Fatalf("err = %v, want ErrOriginalDecisionUnavailable", err)
		}
	})
}
