// SPDX-License-Identifier: AGPL-3.0-or-later

package doctor

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// keyArgs are the arguments of the check builders that name texts.
var keyArgs = map[string][]int{"New": {2, 3}, "NewSite": {3, 4}, "okT": {1}, "warnT": {1, 2}, "failT": {1, 2}, "Socket": {4}, "Text": {0}}

// Every text key a check builder is given is in the catalog: Text panics
// on an unknown one, and many checks only run on a real server.
func TestKeysKnown(t *testing.T) {
	n := 0

	err := filepath.WalkDir("..", func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return err
		}

		f, err := parser.ParseFile(token.NewFileSet(), path, nil, 0)
		if err != nil {
			return err
		}

		inDoctor := f.Name.Name == "doctor"

		// String values given to names in this file: key := "x", const hint = "x".
		assigned := map[string][]string{}

		ast.Inspect(f, func(node ast.Node) bool {
			switch x := node.(type) {
			case *ast.AssignStmt:
				for i, l := range x.Lhs {
					if id, ok := l.(*ast.Ident); ok && i < len(x.Rhs) {
						if s, ok := literal(x.Rhs[i]); ok {
							assigned[id.Name] = append(assigned[id.Name], s)
						}
					}
				}
			case *ast.ValueSpec:
				for i, id := range x.Names {
					if i < len(x.Values) {
						if s, ok := literal(x.Values[i]); ok {
							assigned[id.Name] = append(assigned[id.Name], s)
						}
					}
				}
			}

			return true
		})

		ast.Inspect(f, func(node ast.Node) bool {
			call, ok := node.(*ast.CallExpr)
			if !ok {
				return true
			}

			name := ""

			switch fn := call.Fun.(type) {
			case *ast.Ident:
				if inDoctor {
					name = fn.Name
				}
			case *ast.SelectorExpr:
				if pkg, ok := fn.X.(*ast.Ident); ok && pkg.Name == "doctor" {
					name = fn.Sel.Name
				}
			}

			for _, i := range keyArgs[name] {
				if i >= len(call.Args) {
					continue
				}

				var keys []string
				if s, ok := literal(call.Args[i]); ok {
					keys = []string{s}
				} else if id, ok := call.Args[i].(*ast.Ident); ok {
					keys = assigned[id.Name]
				}

				for _, k := range keys {
					n++

					if _, ok := texts[k]; !ok && k != "" {
						t.Errorf("%s: %s(...) uses the unknown text %q", path, name, k)
					}
				}
			}

			return true
		})

		return nil
	})
	if err != nil {
		t.Fatal(err)
	}

	if n < 100 {
		t.Errorf("found only %d text keys; is the walk broken?", n)
	}
}

func literal(e ast.Expr) (string, bool) {
	b, ok := e.(*ast.BasicLit)
	if !ok || b.Kind != token.STRING {
		return "", false
	}

	s, err := strconv.Unquote(b.Value)

	return s, err == nil
}
