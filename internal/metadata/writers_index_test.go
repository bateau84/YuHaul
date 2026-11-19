package metadata_test

import (
	"context"
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/bateau84/YuHaul/internal/metadata"
	"github.com/bateau84/YuHaul/internal/titlekey"
	"github.com/stretchr/testify/require"
)

// R3 (ARS-001 §10.3): Upsert, Refresh and UpsertFetched leave exactly one entry
// with k = Key(title) for an Indexable row, add nothing in the non-add cases,
// and roll the title write back when the index update fails.
func TestR3StoreWritersKeepIndexCurrent(t *testing.T) {
	ctx := context.Background()
	now := time.Now().UTC()
	title := "Straße der Lieder"
	blank := "  "

	type write func(ms *metadata.Store, id string, title *string, mod func(*metadata.TransientMetadata)) error
	writers := map[string]write{
		// Upsert judges the row at the current time.
		"Upsert": func(ms *metadata.Store, id string, ttl *string, mod func(*metadata.TransientMetadata)) error {
			m := metadata.TransientMetadata{VideoID: id, Title: ttl, FetchedAt: now, ExpiresAt: now.Add(time.Hour), IsActive: true}
			if mod != nil {
				mod(&m)
			}
			return ms.Upsert(ctx, m)
		},
		"UpsertFetched": func(ms *metadata.Store, id string, ttl *string, _ func(*metadata.TransientMetadata)) error {
			_, err := ms.UpsertFetched(ctx, []metadata.TransientMetadata{{VideoID: id, Title: ttl}}, time.Hour, now)
			return err
		},
		"Refresh": func(ms *metadata.Store, id string, ttl *string, _ func(*metadata.TransientMetadata)) error {
			return ms.Refresh(ctx, []metadata.TransientMetadata{{VideoID: id, Title: ttl}}, time.Hour, now)
		},
	}
	// seed stores a row that Refresh can update and that has no entry.
	seed := func(t *testing.T, ms *metadata.Store, id string) {
		t.Helper()
		_, err := ms.DB().ExecContext(ctx, `INSERT INTO transient_metadata (video_id, title, fetched_at, expires_at, is_active)
			VALUES (?, 'old', ?, ?, 1)`, id, now.Add(-time.Hour), now.Add(time.Hour))
		require.NoError(t, err)
	}

	for name, w := range writers {
		t.Run(name+" adds one entry for a held title", func(t *testing.T) {
			ms, _ := openIndexDB(t)
			seed(t, ms, "v1")
			require.NoError(t, w(ms, "v1", &title, nil))
			require.Equal(t, map[string]string{"v1": titlekey.Key(title)}, entries(t, ms.DB()))

			// Writing again replaces, never duplicates.
			other := "Other"
			require.NoError(t, w(ms, "v1", &other, nil))
			require.Equal(t, map[string]string{"v1": titlekey.Key(other)}, entries(t, ms.DB()))
			require.Equal(t, 1, count(t, ms.DB(), "title_search_doc"))
			require.Equal(t, 1, count(t, ms.DB(), "title_search"))
		})
		t.Run(name+" adds nothing for a blank or missing title", func(t *testing.T) {
			ms, _ := openIndexDB(t)
			seed(t, ms, "v1")
			require.NoError(t, w(ms, "v1", &blank, nil))
			require.Empty(t, entries(t, ms.DB()))
			require.NoError(t, w(ms, "v1", nil, nil))
			require.Empty(t, entries(t, ms.DB()))
		})
		t.Run(name+" injected index failure rolls back the title write", func(t *testing.T) {
			ms, _ := openIndexDB(t)
			seed(t, ms, "v1")
			for _, trg := range []string{"insert", "update", "delete"} {
				_, err := ms.DB().Exec(`DROP TRIGGER transient_metadata_search_purge_` + trg)
				require.NoError(t, err)
			}
			_, err := ms.DB().Exec(`DROP TABLE title_search`)
			require.NoError(t, err)

			require.Error(t, w(ms, "v1", &title, nil))
			got, err := ms.Get(ctx, "v1")
			require.NoError(t, err)
			require.Equal(t, "old", *got.Title)
		})
	}

	t.Run("Upsert adds nothing for inactive, expired, short-ttl or past rows", func(t *testing.T) {
		ms, _ := openIndexDB(t)
		for id, mod := range map[string]func(*metadata.TransientMetadata){
			"inactive": func(m *metadata.TransientMetadata) { m.IsActive = false },
			"marker":   func(m *metadata.TransientMetadata) { m.ExpiredAt = &now },
			"short":    func(m *metadata.TransientMetadata) { m.ExpiresAt = m.FetchedAt },
			"past": func(m *metadata.TransientMetadata) {
				m.FetchedAt, m.ExpiresAt = now.Add(-2*time.Hour), now.Add(-time.Hour)
			},
		} {
			require.NoError(t, writers["Upsert"](ms, id, &title, mod), id)
		}
		require.Empty(t, entries(t, ms.DB()))
	})

	t.Run("Upsert indexes a fetch time later than now when it is inside its TTL", func(t *testing.T) {
		ms, _ := openIndexDB(t)
		require.NoError(t, writers["Upsert"](ms, "v1", &title, func(m *metadata.TransientMetadata) {
			m.FetchedAt, m.ExpiresAt = now.Add(time.Hour), now.Add(2*time.Hour)
		}))
		require.Contains(t, entries(t, ms.DB()), "v1")
	})

	t.Run("Refresh that matches no row adds nothing", func(t *testing.T) {
		ms, _ := openIndexDB(t)
		require.NoError(t, writers["Refresh"](ms, "missing", &title, nil))
		require.Empty(t, entries(t, ms.DB()))
		require.Zero(t, count(t, ms.DB(), "transient_metadata"))
	})

	t.Run("Refresh with a future fetched_at inside its TTL is indexed", func(t *testing.T) {
		ms, _ := openIndexDB(t)
		seed(t, ms, "v1")
		future := now.Add(time.Hour)
		require.NoError(t, ms.Refresh(ctx, []metadata.TransientMetadata{{VideoID: "v1", Title: &title}}, time.Hour, future))
		require.Contains(t, entries(t, ms.DB()), "v1")
	})

	t.Run("UpsertFetched indexes a row that was missing", func(t *testing.T) {
		ms, _ := openIndexDB(t)
		n, err := ms.UpsertFetched(ctx, []metadata.TransientMetadata{{VideoID: "new", Title: &title}}, time.Hour, now)
		require.NoError(t, err)
		require.Equal(t, 1, n)
		require.Equal(t, map[string]string{"new": titlekey.Key(title)}, entries(t, ms.DB()))
	})
}

func TestCleanupExpiredLimitIsClampedToIndexWriteBatch(t *testing.T) {
	setBatch(t, 3)
	ms, _ := openIndexDB(t)
	for _, id := range []string{"a", "b", "c", "d", "e"} {
		put(t, ms, id, "Title "+id, nil)
		indexNow(t, ms.DB(), id, idxNow)
	}
	cutoff := idxNow // every row was fetched an hour before idxNow
	n, err := ms.CleanupExpired(context.Background(), cutoff, 10000)
	require.NoError(t, err)
	require.Equal(t, 3, n, "a large limit is clamped to one batch")
	require.Equal(t, 2, count(t, ms.DB(), "transient_metadata"))
	require.Len(t, entries(t, ms.DB()), 2, "triggers purged the deleted rows' entries")

	n, err = ms.CleanupExpired(context.Background(), cutoff, 0)
	require.NoError(t, err)
	require.Equal(t, 2, n, "a non-positive limit means one batch")
	require.Zero(t, count(t, ms.DB(), "title_search_doc"))
}

// R7 (ARS-001 §10.3): the production functions whose SQL writes
// transient_metadata are exactly the ones listed here. A new writer fails the
// test until it is added, which also requires deciding whether it must index.
// Limit: SQL assembled at run time from fragments is not seen; review must
// catch it, and the triggers still guarantee purges.
// callsIndex reports whether fn's body contains a real call to IndexTitleTx or
// indexTitle, either bare or as a selector (metadata.IndexTitleTx). Text in
// comments or strings does not count.
func callsIndex(fn *ast.FuncDecl) bool {
	found := false
	ast.Inspect(fn.Body, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		name := ""
		switch f := call.Fun.(type) {
		case *ast.Ident:
			name = f.Name
		case *ast.SelectorExpr:
			name = f.Sel.Name
		}
		if name == "IndexTitleTx" || name == "indexTitle" {
			found = true
		}
		return !found
	})
	return found
}

func TestR7TransientMetadataWriterCensus(t *testing.T) {
	const ws = `\s+`
	mode := `(?:or` + ws + `(?:replace|ignore|abort|fail|rollback)` + ws + `)?`
	insertUpdate := regexp.MustCompile(`(?is)\b(?:insert` + ws + mode + `into` + ws + `|replace` + ws + `into` + ws + `|update` + ws + mode + `)transient_metadata\b`)
	deleteFrom := regexp.MustCompile(`(?is)\bdelete` + ws + `from` + ws + `transient_metadata\b`)

	// want maps each writer to whether it can add an entry and so must index.
	want := map[string]bool{
		"internal/discovery/engine.go: Engine.upsertTransientMetadata": true,
		"internal/metadata/store.go: Store.Upsert":                     true,
		"internal/metadata/store.go: Store.UpsertFetched":              true,
		"internal/metadata/store.go: Store.Refresh":                    true,
		"internal/metadata/store.go: Store.Expire":                     false, // triggers purge; adds nothing
		"internal/metadata/store.go: Store.CleanupExpired":             false, // delete only
	}

	root := filepath.Join("..", "..")
	got := map[string]bool{}
	for _, dir := range []string{"internal", "cmd"} {
		require.NoError(t, filepath.WalkDir(filepath.Join(root, dir), func(path string, d fs.DirEntry, err error) error {
			if err != nil || d.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
				return err
			}
			src, err := os.ReadFile(path)
			require.NoError(t, err)
			fset := token.NewFileSet()
			f, err := parser.ParseFile(fset, path, src, 0)
			require.NoError(t, err)
			rel := filepath.ToSlash(strings.TrimPrefix(path, root+string(filepath.Separator)))
			covered := map[[2]int]bool{}
			for _, decl := range f.Decls {
				fn, ok := decl.(*ast.FuncDecl)
				if !ok || fn.Body == nil {
					continue
				}
				from, to := fset.Position(fn.Pos()).Offset, fset.Position(fn.End()).Offset
				covered[[2]int{from, to}] = true
				body := string(src[from:to])
				if !insertUpdate.MatchString(body) && !deleteFrom.MatchString(body) {
					continue
				}
				name := fn.Name.Name
				if fn.Recv != nil && len(fn.Recv.List) == 1 {
					t := fn.Recv.List[0].Type
					if star, ok := t.(*ast.StarExpr); ok {
						t = star.X
					}
					if id, ok := t.(*ast.Ident); ok {
						name = id.Name + "." + name
					}
				}
				got[rel+": "+name] = callsIndex(fn)
			}
			// A statement outside any function (a package-level SQL constant)
			// cannot be attributed to a writer.
			for _, re := range []*regexp.Regexp{insertUpdate, deleteFrom} {
				for _, loc := range re.FindAllIndex(src, -1) {
					inside := false
					for span := range covered {
						if loc[0] >= span[0] && loc[1] <= span[1] {
							inside = true
						}
					}
					require.True(t, inside, "%s: transient_metadata write outside a function; add it to R7", rel)
				}
			}
			return nil
		}))
	}

	keys := func(m map[string]bool) []string {
		var out []string
		for k := range m {
			out = append(out, k)
		}
		sort.Strings(out)
		return out
	}
	require.Equal(t, keys(want), keys(got), "transient_metadata writer set changed; update R7 after deciding whether the writer must call IndexTitleTx")
	for name, mustIndex := range want {
		require.Equal(t, mustIndex, got[name], "%s: IndexTitleTx call presence", name)
	}
}
