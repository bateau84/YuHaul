package main

import (
	"go/ast"
	"go/parser"
	"go/token"
	"testing"

	"github.com/stretchr/testify/require"
)

// ARS-001 §10.4: the title search index is built or repaired before any worker,
// scheduler or listener starts. This pins the order in main's source, because
// the integration composition tests do not run in the default test set.
func TestMainPreparesSearchIndexBeforeWorkersAndListener(t *testing.T) {
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "main.go", nil, 0)
	require.NoError(t, err)

	var body []ast.Stmt
	for _, d := range file.Decls {
		if fn, ok := d.(*ast.FuncDecl); ok && fn.Name.Name == "main" && fn.Recv == nil {
			body = fn.Body.List
		}
	}
	require.NotEmpty(t, body, "main not found")

	// first returns the index of the first top-level statement of main that
	// contains a call of pkg.name, or -1.
	first := func(pkg, name string) int {
		for i, s := range body {
			found := false
			ast.Inspect(s, func(n ast.Node) bool {
				call, ok := n.(*ast.CallExpr)
				if !ok {
					return !found
				}
				if sel, ok := call.Fun.(*ast.SelectorExpr); ok && sel.Sel.Name == name {
					if id, ok := sel.X.(*ast.Ident); ok && (pkg == "" || id.Name == pkg) {
						found = true
					}
				}
				return !found
			})
			if found {
				return i
			}
		}
		return -1
	}
	firstGo := func() int {
		for i, s := range body {
			if _, ok := s.(*ast.GoStmt); ok {
				return i
			}
		}
		return -1
	}

	open := first("store", "Open")
	prepare := first("metadata", "PrepareSearchIndex")
	require.GreaterOrEqual(t, open, 0, "store.Open call")
	require.GreaterOrEqual(t, prepare, 0, "metadata.PrepareSearchIndex call")
	require.Greater(t, prepare, open, "PrepareSearchIndex runs after store.Open")

	// Only Open's error check and the deferred Close may sit between them, and
	// nothing before PrepareSearchIndex, nested or not, may start a goroutine.
	for _, s := range body[open+1 : prepare] {
		switch s.(type) {
		case *ast.IfStmt, *ast.DeferStmt:
		default:
			t.Fatalf("statement at %s sits between store.Open and PrepareSearchIndex", fset.Position(s.Pos()))
		}
	}
	for _, s := range body[:prepare] {
		ast.Inspect(s, func(n ast.Node) bool {
			if g, ok := n.(*ast.GoStmt); ok {
				t.Errorf("goroutine at %s starts before PrepareSearchIndex", fset.Position(g.Pos()))
			}
			return true
		})
	}

	for _, later := range []struct {
		what string
		idx  int
	}{
		{"net.Listen", first("net", "Listen")},
		{"scheduler.NewScheduler", first("scheduler", "NewScheduler")},
		{"the first go statement", firstGo()},
		{"the HTTP server", first("", "Serve")},
	} {
		if later.idx >= 0 {
			require.Less(t, prepare, later.idx, "PrepareSearchIndex before %s", later.what)
		}
	}

	// The statement is `if prep, err := PrepareSearchIndex(...); err != nil {
	// ... os.Exit(1) }`: the condition tests the error that call returned, and
	// the exit is a direct statement of that branch, so it can run.
	ifs, ok := body[prepare].(*ast.IfStmt)
	require.True(t, ok, "PrepareSearchIndex must be the init of an if statement")
	init, ok := ifs.Init.(*ast.AssignStmt)
	require.True(t, ok, "if init must assign the call's results")
	require.Len(t, init.Rhs, 1)
	call, ok := init.Rhs[0].(*ast.CallExpr)
	require.True(t, ok)
	sel, ok := call.Fun.(*ast.SelectorExpr)
	require.True(t, ok)
	require.Equal(t, "PrepareSearchIndex", sel.Sel.Name)
	require.Len(t, init.Lhs, 2)
	errVar, ok := init.Lhs[1].(*ast.Ident)
	require.True(t, ok)
	cond, ok := ifs.Cond.(*ast.BinaryExpr)
	require.True(t, ok, "condition must be `err != nil`")
	require.Equal(t, token.NEQ, cond.Op)
	x, ok := cond.X.(*ast.Ident)
	require.True(t, ok)
	require.Equal(t, errVar.Name, x.Name, "the condition tests the call's error")
	nilID, ok := cond.Y.(*ast.Ident)
	require.True(t, ok)
	require.Equal(t, "nil", nilID.Name)

	exits := false
	for _, st := range ifs.Body.List {
		es, ok := st.(*ast.ExprStmt)
		if !ok {
			continue
		}
		c, ok := es.X.(*ast.CallExpr)
		if !ok || len(c.Args) != 1 {
			continue
		}
		if sel, ok := c.Fun.(*ast.SelectorExpr); ok && sel.Sel.Name == "Exit" {
			if lit, ok := c.Args[0].(*ast.BasicLit); ok && lit.Value == "1" {
				exits = true
			}
		}
	}
	require.True(t, exits, "the failure branch must directly call os.Exit(1)")
}
