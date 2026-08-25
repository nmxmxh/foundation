package worker

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestRecurringUniqueOptsScopeByState is the TE-14 regression guard for the
// failure recorded in database_practices.md, "The Queue Is Not A Scheduler
// Clock".
//
// rivertype.UniqueOptsByStateDefault() includes `completed`. A recurring
// producer that scopes uniqueness with ByPeriod or ByArgs and leaves ByState at
// its default is therefore blocked by its own previous run until that row is
// reaped: the schedule stops after one pass, with no error and no log line.
// Because the failure is silent, it is caught by shape rather than by behavior.
//
// The check reads the package source instead of the returned []*river.PeriodicJob
// because PeriodicJob keeps its constructor in an unexported field; reaching it
// would need reflection over a third-party struct layout. Only composite
// literals are inspected — UniqueOpts assembled through a variable is not
// matched, which is the accepted blind spot for a guard that needs no
// dependency of its own.
func TestRecurringUniqueOptsScopeByState(t *testing.T) {
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatalf("read worker package directory: %v", err)
	}

	fset := token.NewFileSet()
	var offenders []string

	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}

		file, err := parser.ParseFile(fset, filepath.Join(".", name), nil, parser.SkipObjectResolution)
		if err != nil {
			t.Fatalf("parse %s: %v", name, err)
		}
		if !declaresPeriodicJob(file) {
			continue
		}

		ast.Inspect(file, func(node ast.Node) bool {
			lit, ok := node.(*ast.CompositeLit)
			if !ok || !isUniqueOptsType(lit.Type) {
				return true
			}
			var hasPeriod, hasArgs, hasState bool
			for _, element := range lit.Elts {
				kv, ok := element.(*ast.KeyValueExpr)
				if !ok {
					continue
				}
				key, ok := kv.Key.(*ast.Ident)
				if !ok {
					continue
				}
				switch key.Name {
				case "ByPeriod":
					hasPeriod = true
				case "ByArgs":
					hasArgs = true
				case "ByState":
					hasState = true
				}
			}
			if (hasPeriod || hasArgs) && !hasState {
				offenders = append(offenders, fset.Position(lit.Pos()).String())
			}
			return true
		})
	}

	for _, offender := range offenders {
		t.Errorf("%s: recurring UniqueOpts sets ByPeriod or ByArgs without ByState; "+
			"the default state set includes completed, so the previous run of this schedule "+
			"blocks the next insert and the job silently stops recurring. Scope uniqueness to "+
			"the non-terminal states: available, pending, retryable, running, scheduled", offender)
	}
}

// declaresPeriodicJob reports whether a file builds River periodic jobs. The
// guard is scoped to those files because a one-shot unique insert wanting the
// default state set is a legitimate shape.
func declaresPeriodicJob(file *ast.File) bool {
	found := false
	ast.Inspect(file, func(node ast.Node) bool {
		selector, ok := node.(*ast.SelectorExpr)
		if !ok {
			return true
		}
		if selector.Sel.Name == "NewPeriodicJob" || selector.Sel.Name == "PeriodicJob" {
			found = true
			return false
		}
		return true
	})
	return found
}

func isUniqueOptsType(expr ast.Expr) bool {
	switch typed := expr.(type) {
	case *ast.Ident:
		return typed.Name == "UniqueOpts"
	case *ast.SelectorExpr:
		return typed.Sel.Name == "UniqueOpts"
	}
	return false
}
