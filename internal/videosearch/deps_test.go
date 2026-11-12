package videosearch

import (
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// T30 (ARS-001 §8.1): search never reaches YouTube or spends quota. The rule is
// enforced on direct imports of the package and of the two handlers. The
// transitive graph cannot be empty of these packages: the permitted
// metadata, state and queue packages themselves import quota or youtube for
// unrelated code, so `go list -deps` would always list them.
var forbiddenImports = []string{
	"github.com/bateau84/YuHaul/internal/youtube",
	"github.com/bateau84/YuHaul/internal/quota",
	"github.com/bateau84/YuHaul/internal/discovery",
	"github.com/bateau84/YuHaul/internal/pipeline",
}

func checkImports(t *testing.T, files []string) {
	t.Helper()
	require.NotEmpty(t, files)
	fset := token.NewFileSet()
	for _, f := range files {
		parsed, err := parser.ParseFile(fset, f, nil, parser.ImportsOnly)
		require.NoError(t, err, f)
		for _, imp := range parsed.Imports {
			path, err := strconv.Unquote(imp.Path.Value)
			require.NoError(t, err)
			for _, bad := range forbiddenImports {
				require.NotEqual(t, bad, path, "%s imports %s", f, path)
			}
		}
		// metadata.Manager's refresh and fetch methods are off limits.
		src, err := os.ReadFile(f)
		require.NoError(t, err)
		for _, bad := range []string{"metadata.Manager", "metadata.NewManager", "ExpireStale", "CleanupExpired", "RefreshRouted"} {
			require.False(t, strings.Contains(string(src), bad), "%s mentions %s", f, bad)
		}
	}
}

func TestSearchPackageHasNoYouTubeOrQuotaImports_T30(t *testing.T) {
	files, err := filepath.Glob("*.go")
	require.NoError(t, err)
	var prod []string
	for _, f := range files {
		if !strings.HasSuffix(f, "_test.go") {
			prod = append(prod, f)
		}
	}
	checkImports(t, prod)
}

func TestSearchHandlersHaveNoYouTubeOrQuotaImports_T30(t *testing.T) {
	checkImports(t, []string{"../server/video_search_handlers.go"})
}
