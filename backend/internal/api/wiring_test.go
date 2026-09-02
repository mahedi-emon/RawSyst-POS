package api

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"sort"
	"strings"
	"testing"
)

// A service the Server holds must be reachable through at least one route.
//
// This exists because of a defect that no other test could have caught. The
// shift package was complete, correct and covered by ten integration tests, and
// it was mounted nowhere: `NewServer` did not take it, no route called it, and
// `shift.NewService` appeared exactly once in the repository — in the test
// harness. Every one of those tests passed by calling the service in-process.
//
// The consequence was not cosmetic. `sales.resolveTerminal` refuses a sale from
// a till with no open cash session, and opening one was unreachable over HTTP,
// so a paired, signed-in, EGS-bound terminal could not ring up a single sale
// against the real API. The suite was green throughout.
//
// It is the third instance of one shape: a component proved in isolation while
// the join to the rest of the system was never made. Rule 11's variance account
// resolved a role no chart mapped, because the test mapped it by hand. P30's
// terminals could never sell, because nothing set `egs_unit_id`. Neither was
// caught by a test of the component, because in both cases the component was
// fine.
//
// TestEveryRouteDeclaresItsAccess catches a route with no permission. Nothing
// caught a service with no route. This does.
//
// # How
//
// The package's own source is parsed rather than a list being maintained by
// hand, because a hand-maintained list is a second place to forget. Routes()
// names its handlers, handlers use `s.<field>`, and helpers called by handlers
// count too — so reachability is computed to a fixed point rather than one
// level deep, or a service reached only through `buildSale` would read as
// unmounted.
func TestEveryServiceTheServerHoldsIsReachableFromARoute(t *testing.T) {
	pkg := parseAPIPackage(t)

	fields := serverFields(t, pkg)
	if len(fields) == 0 {
		t.Fatal("no fields found on Server; the parser is not seeing the source")
	}

	usesField, callsMethod := methodBodies(pkg)
	mounted := handlersNamedInRoutes(t, pkg)
	if len(mounted) == 0 {
		t.Fatal("Routes() names no handlers; the parser is not seeing the source")
	}

	// Everything reachable from a mounted handler, following calls between
	// methods on *Server.
	reached := map[string]bool{}
	queue := append([]string{}, mounted...)
	for len(queue) > 0 {
		m := queue[0]
		queue = queue[1:]
		if reached[m] {
			continue
		}
		reached[m] = true
		queue = append(queue, callsMethod[m]...)
	}

	isField := map[string]bool{}
	for _, f := range fields {
		isField[f] = true
	}

	// A field and a method on the same type cannot share a name in Go, so a
	// `s.foo(...)` whose name is a field is unambiguously a call THROUGH the
	// field — `s.health()` is the case here — and counts as reaching it.
	live := map[string]bool{}
	for m := range reached {
		for _, f := range usesField[m] {
			live[f] = true
		}
		for _, c := range callsMethod[m] {
			if isField[c] {
				live[c] = true
			}
		}
	}

	var orphaned []string
	for _, f := range fields {
		if live[f] || notServedByARoute[f] != "" {
			continue
		}
		orphaned = append(orphaned, f)
	}
	sort.Strings(orphaned)

	if len(orphaned) > 0 {
		t.Fatalf("the Server holds %v, and no route reaches %s.\n"+
			"A service wired into the struct but not onto a route is dead in "+
			"production and alive in the tests, which is how the shift module "+
			"stayed unreachable while ten tests passed. Mount it, or record why "+
			"it is not route-served in notServedByARoute.",
			orphaned, plural(orphaned))
	}
}

// Fields that legitimately have no route, each with the reason. Kept short on
// purpose: an entry here is an exemption from the guard above, so adding one
// should feel like a decision rather than a formality.
var notServedByARoute = map[string]string{
	"mw": "middleware; mounted by Handler() around every route rather than by one of them",
	"authz": "the authorizer the middleware resolves permissions through; " +
		"reached via mw, not from a handler",
	"version": "a build string, not a service",
	"cache": "the shared cache; reached through recoveryLimit, which the public " +
		"recovery and portal routes call. A route of its own would be a way to " +
		"read and write a cache from outside",
	"reporter": "error reporting; registered once with httpx.OnServerError and " +
		"called on the way OUT of a failed request rather than by a handler",
}

func plural(s []string) string {
	if len(s) == 1 {
		return "it"
	}
	return "them"
}

// --- parsing ------------------------------------------------------------

// parseAPIPackage reads the non-test source of this package. Tests run with the
// package directory as their working directory, so "." is this package.
func parseAPIPackage(t *testing.T) map[string]*ast.File {
	t.Helper()
	fset := token.NewFileSet()
	pkgs, err := parser.ParseDir(fset, ".", func(fi fs.FileInfo) bool {
		return !strings.HasSuffix(fi.Name(), "_test.go")
	}, 0)
	if err != nil {
		t.Fatalf("parse package source: %v", err)
	}
	p, ok := pkgs["api"]
	if !ok {
		t.Fatal("package api not found in the current directory")
	}
	return p.Files
}

// serverFields returns the field names declared on the Server struct.
func serverFields(t *testing.T, files map[string]*ast.File) []string {
	t.Helper()
	var out []string
	for _, f := range files {
		ast.Inspect(f, func(n ast.Node) bool {
			ts, ok := n.(*ast.TypeSpec)
			if !ok || ts.Name.Name != "Server" {
				return true
			}
			st, ok := ts.Type.(*ast.StructType)
			if !ok {
				return true
			}
			for _, fld := range st.Fields.List {
				for _, name := range fld.Names {
					out = append(out, name.Name)
				}
			}
			return false
		})
	}
	sort.Strings(out)
	return out
}

// methodBodies reports, for every method on *Server, which Server fields it
// reads and which other Server methods it calls.
//
// Both are read off `s.<name>` selectors, which is what makes this work without
// type information: a call is `s.foo(...)` and a field read is `s.foo` in any
// other position.
func methodBodies(files map[string]*ast.File) (usesField, callsMethod map[string][]string) {
	usesField = map[string][]string{}
	callsMethod = map[string][]string{}

	for _, f := range files {
		for _, decl := range f.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if !ok || fn.Recv == nil || fn.Body == nil {
				continue
			}
			recv := receiverName(fn)
			if recv == "" {
				continue
			}
			name := fn.Name.Name

			called := map[string]bool{}
			ast.Inspect(fn.Body, func(n ast.Node) bool {
				call, ok := n.(*ast.CallExpr)
				if !ok {
					return true
				}
				if sel, ok := call.Fun.(*ast.SelectorExpr); ok {
					if id, ok := sel.X.(*ast.Ident); ok && id.Name == recv {
						called[sel.Sel.Name] = true
					}
				}
				return true
			})

			seen := map[string]bool{}
			ast.Inspect(fn.Body, func(n ast.Node) bool {
				sel, ok := n.(*ast.SelectorExpr)
				if !ok {
					return true
				}
				id, ok := sel.X.(*ast.Ident)
				if !ok || id.Name != recv {
					return true
				}
				// `s.shift.Open(...)` reads the field and calls a method ON it,
				// so the outer selector is the field. `s.handleX(...)` is a
				// method on the server itself and is followed instead.
				if !called[sel.Sel.Name] && !seen[sel.Sel.Name] {
					seen[sel.Sel.Name] = true
					usesField[name] = append(usesField[name], sel.Sel.Name)
				}
				return true
			})

			for c := range called {
				callsMethod[name] = append(callsMethod[name], c)
			}
		}
	}
	return usesField, callsMethod
}

// receiverName returns the identifier a method's *Server receiver is bound to,
// or "" if the method is on some other type.
func receiverName(fn *ast.FuncDecl) string {
	if len(fn.Recv.List) != 1 || len(fn.Recv.List[0].Names) != 1 {
		return ""
	}
	star, ok := fn.Recv.List[0].Type.(*ast.StarExpr)
	if !ok {
		return ""
	}
	id, ok := star.X.(*ast.Ident)
	if !ok || id.Name != "Server" {
		return ""
	}
	return fn.Recv.List[0].Names[0].Name
}

// handlersNamedInRoutes returns the handler methods Routes() actually mounts.
//
// Read from Routes()'s own body rather than from the returned values, because a
// func value carries no name at runtime and the question here is precisely
// which named methods are wired up.
func handlersNamedInRoutes(t *testing.T, files map[string]*ast.File) []string {
	t.Helper()
	var out []string
	for _, f := range files {
		for _, decl := range f.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if !ok || fn.Name.Name != "Routes" || fn.Recv == nil {
				continue
			}
			recv := receiverName(fn)
			if recv == "" {
				continue
			}
			ast.Inspect(fn.Body, func(n ast.Node) bool {
				sel, ok := n.(*ast.SelectorExpr)
				if !ok {
					return true
				}
				if id, ok := sel.X.(*ast.Ident); ok && id.Name == recv {
					out = append(out, sel.Sel.Name)
				}
				return true
			})
		}
	}
	sort.Strings(out)
	return out
}
