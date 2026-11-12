package state

import (
	"bufio"
	"context"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// This file is the maintained audit required by run-initiation ARS-005 test 33
// (obligation O2). Every place that lists or switches over final outcomes is
// inventoried below with how it treats 'unavailable_on_youtube', a final
// outcome like accepted/rejected/excluded/failed.
//
// The audit scans the repository's non-test Go and SQL sources for outcome
// literals. It fails when
//   - a scanned site is not covered by an inventory entry (new, unreviewed
//     site: add an entry and decide how it treats unavailable_on_youtube), or
//   - an inventory entry matches nothing (stale entry: remove or update it), or
//   - an entry marked inventoryHandled does not mention unavailable_on_youtube
//     within three lines of its matched lines.
//
// Behaviour for each handled site is proven by the tests of its own package;
// this audit proves the inventory stays complete.

type inventoryStatus string

const (
	// inventoryHandled: the site names unavailable_on_youtube explicitly.
	inventoryHandled inventoryStatus = "handled"
	// inventoryNotApplicable: the value cannot reach, or is correctly
	// excluded by, the site. Why must say which.
	inventoryNotApplicable inventoryStatus = "not-applicable"
	// inventorySuperseded: historical schema text widened by a later migration.
	inventorySuperseded inventoryStatus = "superseded"
)

type outcomeSite struct {
	File    string // slash path relative to the repo root
	Pattern string // regexp selecting the site's scanned lines in File
	Status  inventoryStatus
	Why     string
}

var outcomeInventory = []outcomeSite{
	// Schema: CHECK lists and their migration.
	{"internal/store/store.go", `.`, inventoryHandled, "migration 026 widens the video and run_video_outcome final_outcome CHECKs"},
	{"internal/store/migrations/001_initial_schema.up.sql", `final_outcome IN|'discovered'`, inventorySuperseded, "original video CHECK, widened by widenFinalOutcomeChecks; line 81 is the processing_state list"},
	{"internal/store/migrations/001_initial_schema.up.sql", `decision TEXT|outcome TEXT`, inventoryNotApplicable, "route decision and executed_action outcome domains, not final outcomes"},
	{"internal/store/migrations/015_run_video_outcome.up.sql", `.`, inventorySuperseded, "original run_video_outcome CHECK, widened by widenFinalOutcomeChecks; deferred/failed pairing CHECKs are unaffected"},

	// internal/state
	{"internal/state/models.go", `.`, inventoryHandled, "VideoFinalOutcome.Valid accepts the value"},
	{"internal/state/store.go", `completedAt|FinalOutcomeAccepted \|\| outcome|FinalOutcomeExcluded \|\| outcome|FinalOutcomeUnavailableOnYouTube \{`, inventoryHandled, "updateVideoState sets completed_at for every final outcome"},
	{"internal/state/store.go", `FinalOutcomeDeferred\) != |FinalOutcomeFailed\) != `, inventoryNotApplicable, "deferred_reason and error_message pairing; unavailable carries neither"},
	{"internal/state/store.go", `FinalOutcomeNotYetEvaluated\)`, inventoryNotApplicable, "only not_yet_evaluated may be overwritten, so a recorded unavailable is protected as terminal"},
	{"internal/state/store.go", `THEN 'accepted'|THEN 'rejected'|THEN 'excluded'|ELSE 'not_yet_evaluated'`, inventoryNotApplicable, "derives dry-run/replay/backtest outcomes from decisions; production runs read run_video_outcome as-is"},
	{"internal/state/store.go", `runID, FinalOutcome`, inventoryHandled, "run counters include videos_unavailable"},
	{"internal/state/playlist_guard.go", `final_outcome IN|final_outcome IS NULL|Only unresolved outcomes`, inventoryHandled, "final outcome does not veto; only NULL, not_yet_evaluated and failed do"},

	// internal/pipeline
	{"internal/pipeline/authority.go", `final_outcome IN`, inventoryHandled, "final-outcome list includes the value"},
	{"internal/pipeline/pipeline.go", `case state\.FinalOutcome|FinalOutcomeDeferred && err`, inventoryNotApplicable, "switch over the executor result, which is only accepted, deferred or failed"},
	{"internal/pipeline/pipeline.go", `recorded\.String == string\(state\.FinalOutcomeFailed\)`, inventoryNotApplicable, "failed-specific check for an already recorded failure"},
	{"internal/pipeline/retry.go", `FinalOutcomeUnavailableOnYouTube`, inventoryHandled, "retry accepts the value as a final outcome"},
	{"internal/pipeline/retry.go", `FinalOutcomeNotYetEvaluated|FinalOutcomeDeferred|FinalOutcomeFailed|final_outcome='failed'`, inventoryNotApplicable, "deferred/failed/not_yet_evaluated-specific retry branches"},
	{"internal/pipeline/exclusion.go", `FinalOutcomeExcluded`, inventoryNotApplicable, "excluded-specific check"},

	// internal/queue (owned by a separate change; listed so new sites there fail loudly)
	{"internal/queue/retry.go", `final_outcome = 'not_yet_evaluated'`, inventoryNotApplicable, "requeue resets a video to not_yet_evaluated"},
}

// outcomeSiteLine selects lines that list or compare final outcomes.
var outcomeSiteLine = regexp.MustCompile(strings.Join([]string{
	`'(accepted|rejected|excluded|deferred|unavailable_on_youtube|not_yet_evaluated)'`,
	`final_outcome.*'failed'|'failed'.*final_outcome`,
	`case\s.*FinalOutcome\w+`,
	`(==|!=)\s*(string\()?(state\.)?FinalOutcome\w+`,
	`FinalOutcome\w+.*(\|\||&&)|(\|\||&&).*FinalOutcome\w+`,
	`^\s*(runID, )?(state\.)?FinalOutcome\w+,\s*$`,
	`runID, FinalOutcome\w+`,
	`^\s*FinalOutcome\w+,`,
}, "|"))

func repoRoot(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatal("go.mod not found above test directory")
		}
		dir = parent
	}
}

func readLines(t *testing.T, path string) []string {
	t.Helper()
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	var lines []string
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 1<<20), 1<<20)
	for sc.Scan() {
		lines = append(lines, sc.Text())
	}
	if err := sc.Err(); err != nil {
		t.Fatal(err)
	}
	return lines
}

func TestOutcomeSiteInventoryIsComplete(t *testing.T) {
	root := repoRoot(t)
	type compiled struct {
		site    outcomeSite
		re      *regexp.Regexp
		matched map[int]bool
	}
	byFile := map[string][]*compiled{}
	var all []*compiled
	for _, s := range outcomeInventory {
		if s.Status != inventoryHandled && s.Why == "" {
			t.Errorf("inventory entry %s /%s/ must explain why it is %s", s.File, s.Pattern, s.Status)
		}
		c := &compiled{site: s, re: regexp.MustCompile(s.Pattern), matched: map[int]bool{}}
		byFile[s.File] = append(byFile[s.File], c)
		all = append(all, c)
	}

	skipDir := map[string]bool{".git": true, "node_modules": true, "web": true, "docs": true, "testdata": true}
	linesOf := map[string][]string{}
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if skipDir[d.Name()] {
				return filepath.SkipDir
			}
			return nil
		}
		name := d.Name()
		isGo := strings.HasSuffix(name, ".go") && !strings.HasSuffix(name, "_test.go")
		isSQL := strings.HasSuffix(name, ".sql") && !strings.HasSuffix(name, ".down.sql")
		if !isGo && !isSQL {
			return nil
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		rel = filepath.ToSlash(rel)
		if rel == "internal/state/outcome_inventory_test.go" {
			return nil
		}
		lines := readLines(t, path)
		linesOf[rel] = lines
		for i, line := range lines {
			trimmed := strings.TrimSpace(line)
			if strings.HasPrefix(trimmed, "//") && !strings.Contains(trimmed, "'") {
				continue // prose comment without a quoted literal
			}
			if !outcomeSiteLine.MatchString(line) {
				continue
			}
			covered := false
			for _, c := range byFile[rel] {
				if c.re.MatchString(line) {
					c.matched[i] = true
					covered = true
				}
			}
			if !covered {
				t.Errorf("%s:%d is an outcome list/switch not in the inventory (decide how it treats unavailable_on_youtube, then add it to outcomeInventory):\n\t%s", rel, i+1, trimmed)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}

	mentions := regexp.MustCompile(`unavailable_on_youtube|UnavailableOnYouTube|videos_unavailable`)
	for _, c := range all {
		if len(c.matched) == 0 {
			t.Errorf("stale inventory entry %s /%s/ matches no scanned line", c.site.File, c.site.Pattern)
			continue
		}
		if c.site.Status != inventoryHandled {
			continue
		}
		lines := linesOf[c.site.File]
		lo, hi := len(lines), -1
		for i := range c.matched {
			lo, hi = min(lo, i), max(hi, i)
		}
		lo, hi = max(lo-3, 0), min(hi+3, len(lines)-1)
		if !mentions.MatchString(strings.Join(lines[lo:hi+1], "\n")) {
			t.Errorf("%s lines %d-%d are marked handled but never mention unavailable_on_youtube", c.site.File, lo+1, hi+1)
		}
	}
}

// Every final outcome except deferred and not_yet_evaluated stamps completed_at
// on the video row, including unavailable_on_youtube.
func TestUpdateVideoStateSetsCompletedAtForFinalOutcomes(t *testing.T) {
	ctx := context.Background()
	m := newTestMachine(t)
	runID := seedRun(t, m)
	want := map[VideoFinalOutcome]bool{
		FinalOutcomeAccepted: true, FinalOutcomeRejected: true, FinalOutcomeExcluded: true,
		FinalOutcomeFailed: true, FinalOutcomeUnavailableOnYouTube: true,
	}
	for _, o := range allFinalOutcomes {
		v, err := m.DiscoverVideo(ctx, runID, "ca-"+string(o), "UCchannel")
		if err != nil {
			t.Fatal(err)
		}
		var reason, message *string
		switch o {
		case FinalOutcomeDeferred:
			r := "quota_exhausted"
			reason = &r
		case FinalOutcomeFailed:
			msg := "action execution failed"
			message = &msg
		}
		if err := m.store.UpdateVideoState(ctx, v.ID, VideoStateEvaluated, o, reason, message); err != nil {
			t.Fatalf("%s: %v", o, err)
		}
		got, err := m.store.GetVideo(ctx, v.ID)
		if err != nil {
			t.Fatal(err)
		}
		if has := got.CompletedAt != nil; has != want[o] {
			t.Errorf("%s: completed_at set = %v, want %v", o, has, want[o])
		}
	}
}
