package pipeline

import (
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// K16 (ARS-006 §10.3, invariant 5): the count of production `succeeded` rows
// for a video never decreases. This is the grep-maintained list of every
// statement that writes executed_action. A new writer fails this test until it
// is reviewed and listed here: it must be an INSERT that cannot overwrite a
// row, or an upsert whose update is gated on the existing row not being
// `succeeded`. No UPDATE, DELETE or REPLACE may exist.
var executedActionWriters = []string{
	"internal/pipeline/executor.go",    // recordActionEvidenceTx: upsert gated on outcome != 'succeeded'
	"internal/state/playlist_guard.go", // ResolvePlaylistSendTx: upsert gated on outcome != 'succeeded'
	"internal/state/store.go",          // CreateExecutedAction: plain INSERT, a conflict is an error
}

func TestExecutedActionWritersCannotReverseASuccessK16(t *testing.T) {
	writer := regexp.MustCompile("(?is)\\b(insert\\s+(or\\s+\\w+\\s+)?into|update|delete\\s+from|replace\\s+into)\\s+executed_action\\b[^`]*")
	root := filepath.Join("..", "..")
	var found []string
	err := filepath.WalkDir(filepath.Join(root, "internal"), func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		ext := filepath.Ext(path)
		if (ext != ".go" || strings.HasSuffix(path, "_test.go")) && ext != ".sql" {
			return nil
		}
		src, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(root, path)
		rel = filepath.ToSlash(rel)
		for _, stmt := range writer.FindAllString(string(src), -1) {
			lower := strings.ToLower(stmt)
			switch {
			case strings.HasPrefix(lower, "update"), strings.HasPrefix(lower, "delete"), strings.HasPrefix(lower, "replace"), strings.Contains(lower, "insert or replace"):
				t.Errorf("%s: %q can change or remove a succeeded row", rel, firstLine(stmt))
			case strings.Contains(lower, "on conflict"):
				if !strings.Contains(stmt, "executed_action.outcome != 'succeeded'") {
					t.Errorf("%s: upsert is not gated on the existing row not being succeeded: %q", rel, firstLine(stmt))
				}
			}
			found = append(found, rel)
		}
		return nil
	})
	require.NoError(t, err)
	sort.Strings(found)
	found = dedupe(found)
	want := append([]string(nil), executedActionWriters...)
	sort.Strings(want)
	require.Equal(t, want, found, "the set of executed_action writers changed; review it against K16 and update the list")
}

func firstLine(s string) string {
	line, _, _ := strings.Cut(strings.TrimSpace(s), "\n")
	return line
}

func dedupe(in []string) []string {
	var out []string
	for _, s := range in {
		if len(out) == 0 || out[len(out)-1] != s {
			out = append(out, s)
		}
	}
	return out
}
