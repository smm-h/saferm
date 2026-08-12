package main

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// Declarations in the command package that nothing reaches are a standing
// hazard: an exit code nobody returns is a documented contract saferm does not
// honour (the generated exit-code table reads it straight out of exitcodes.go),
// and an unreachable helper is an invitation to wire it up instead of asking
// whether it should exist. Go's own toolchain reports neither -- unused
// functions and unused package-level constants both compile silently.
//
// These two tests are the structural answer: the package is parsed, every
// top-level func and every Exit* constant is counted against its uses, and a
// declaration with no use fails the build's tests.

// packageIdentCounts parses every non-test Go file in the command package and
// returns how many times each identifier appears, plus the names declared as
// top-level funcs and as Exit* constants.
func packageIdentCounts(t *testing.T) (counts map[string]int, funcs []string, exitCodes []string) {
	t.Helper()

	_, thisFile, _, _ := runtime.Caller(0)
	pkgDir := filepath.Dir(thisFile)

	fset := token.NewFileSet()
	pkgs, err := parser.ParseDir(fset, pkgDir, func(fi fs.FileInfo) bool {
		return !strings.HasSuffix(fi.Name(), "_test.go")
	}, 0)
	if err != nil {
		t.Fatalf("parsing %s: %v", pkgDir, err)
	}

	pkg, ok := pkgs["main"]
	if !ok {
		t.Fatalf("no package main found in %s", pkgDir)
	}

	counts = make(map[string]int)
	for _, file := range pkg.Files {
		ast.Inspect(file, func(n ast.Node) bool {
			if id, ok := n.(*ast.Ident); ok {
				counts[id.Name]++
			}
			return true
		})

		for _, decl := range file.Decls {
			switch d := decl.(type) {
			case *ast.FuncDecl:
				if d.Recv == nil {
					funcs = append(funcs, d.Name.Name)
				}
			case *ast.GenDecl:
				if d.Tok != token.CONST {
					continue
				}
				for _, spec := range d.Specs {
					vs, ok := spec.(*ast.ValueSpec)
					if !ok {
						continue
					}
					for _, name := range vs.Names {
						if strings.HasPrefix(name.Name, "Exit") {
							exitCodes = append(exitCodes, name.Name)
						}
					}
				}
			}
		}
	}

	return counts, funcs, exitCodes
}

func TestEveryExitCodeIsReturnedSomewhere(t *testing.T) {
	counts, _, exitCodes := packageIdentCounts(t)

	if len(exitCodes) == 0 {
		t.Fatal("no Exit* constants found; the extractor has lost sight of exitcodes.go")
	}

	for _, name := range exitCodes {
		// One occurrence is the declaration itself.
		if counts[name] < 2 {
			t.Errorf("exit code %s is declared but never returned; the generated exit-code table documents a value saferm cannot produce", name)
		}
	}
}

func TestEveryPackageFunctionIsReachable(t *testing.T) {
	counts, funcs, _ := packageIdentCounts(t)

	if len(funcs) == 0 {
		t.Fatal("no top-level functions found; the extractor has lost sight of the package")
	}

	for _, name := range funcs {
		// main and init are called by the runtime, not by the package.
		if name == "main" || name == "init" {
			continue
		}
		if counts[name] < 2 {
			t.Errorf("function %s is declared but never called from the package", name)
		}
	}
}
