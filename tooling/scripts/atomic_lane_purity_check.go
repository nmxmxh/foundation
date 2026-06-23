//go:build ignore

// atomic_lane_purity_check.go enforces CP-07.17 and CP-11A.3: an AtomicLane
// transaction closure (and, by extension, any open DB transaction body) must
// stay pure database work. Network and coordination calls — HTTP, gRPC, Redis,
// event publication — must not run while a transaction holds query, lock, and
// idle-transaction budgets.
//
// This is an AST gate, not a regex heuristic. It parses each Go file, finds
// calls to a function named AtomicLane whose final argument is a function
// literal, and walks that literal's body for forbidden calls. Because it works
// on syntax only (no type information), it flags two high-precision shapes:
//
//  1. calls qualified by a known network/coordination package identifier
//     (http, grpc, redis, nats, kafka), e.g. http.Get(...), redis.Do(...);
//  2. calls to distinctively networked method names regardless of receiver
//     (Publish, PublishMany, XAdd*, Emit, EmitEvent, Dial, Invoke).
//
// Generic method names such as Get/Set/Do/Post are intentionally NOT flagged
// unless package-qualified, to keep false positives near zero. The known gap is
// indirection: a closure that calls an out-of-line helper which itself performs
// I/O cannot be caught without whole-program type analysis. Document such cases
// and keep the helper out of the transaction.
//
// Waiver: append `// cp:allow-tx-io` to the offending line (or the AtomicLane
// line) to suppress a finding; record the rationale per the CP exception
// process. Usage: go run atomic_lane_purity_check.go [target-dir]
package main

import (
	"bufio"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
)

// forbiddenPkgs are identifiers that, when used as the receiver of a call,
// indicate a network or coordination boundary.
var forbiddenPkgs = map[string]struct{}{
	"http":  {},
	"grpc":  {},
	"redis": {},
	"nats":  {},
	"kafka": {},
}

// forbiddenMethods are method names distinctive enough to flag on any receiver.
var forbiddenMethods = map[string]struct{}{
	"Publish":        {},
	"PublishMany":    {},
	"XAdd":           {},
	"XAddMany":       {},
	"XAddManyField":  {},
	"Emit":           {},
	"EmitEvent":      {},
	"Dial":           {},
	"Invoke":         {},
	"IncrExpireMany": {},
	"SetGetMany":     {},
}

const waiverToken = "cp:allow-tx-io"

type finding struct {
	pos  token.Position
	call string
	line string
}

func main() {
	target := "."
	if len(os.Args) > 1 {
		target = os.Args[1]
	}

	var findings []finding
	fset := token.NewFileSet()

	err := filepath.Walk(target, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() {
			switch info.Name() {
			case "node_modules", ".git", "dist", "target", "vendor", "testdata", "generated":
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") {
			return nil
		}
		if strings.HasSuffix(path, "_test.go") || strings.HasSuffix(path, ".pb.go") {
			return nil
		}

		file, perr := parser.ParseFile(fset, path, nil, 0)
		if perr != nil {
			// Unparseable files are not this check's concern; the compiler and
			// other gates own syntax errors.
			return nil
		}
		lines := readLines(path)

		ast.Inspect(file, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok || !isAtomicLane(call.Fun) || len(call.Args) == 0 {
				return true
			}
			lit, ok := call.Args[len(call.Args)-1].(*ast.FuncLit)
			if !ok {
				return true
			}
			laneLine := fset.Position(call.Pos()).Line
			ast.Inspect(lit.Body, func(bn ast.Node) bool {
				inner, ok := bn.(*ast.CallExpr)
				if !ok {
					return true
				}
				name, bad := forbiddenCall(inner.Fun)
				if !bad {
					return true
				}
				pos := fset.Position(inner.Pos())
				if hasWaiver(lines, pos.Line) || hasWaiver(lines, laneLine) {
					return true
				}
				findings = append(findings, finding{
					pos:  pos,
					call: name,
					line: lineText(lines, pos.Line),
				})
				return true
			})
			return true
		})
		return nil
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "atomic lane purity check: %v\n", err)
		os.Exit(2)
	}

	label := "CP no network/coordination call inside AtomicLane transaction closure"
	if len(findings) == 0 {
		fmt.Printf("[OK] %s\n", label)
		return
	}
	fmt.Printf("[FAIL] %s\n", label)
	for _, f := range findings {
		fmt.Printf("  %s:%d: %s() inside transaction closure\n", f.pos.Filename, f.pos.Line, f.call)
		if f.line != "" {
			fmt.Printf("      %s\n", strings.TrimSpace(f.line))
		}
	}
	fmt.Printf("  CP-07.17/CP-11A.3: keep transaction closures pure DB work; move I/O\n")
	fmt.Printf("  outside the transaction, or annotate the line with // %s\n", waiverToken)
	os.Exit(1)
}

// isAtomicLane reports whether fn names a function called "AtomicLane", whether
// invoked bare, as a package selector, or as a method.
func isAtomicLane(fn ast.Expr) bool {
	switch f := fn.(type) {
	case *ast.Ident:
		return f.Name == "AtomicLane"
	case *ast.SelectorExpr:
		return f.Sel.Name == "AtomicLane"
	}
	return false
}

// forbiddenCall returns a display name and whether the call crosses a
// network/coordination boundary.
func forbiddenCall(fn ast.Expr) (string, bool) {
	sel, ok := fn.(*ast.SelectorExpr)
	if !ok {
		return "", false
	}
	if _, bad := forbiddenMethods[sel.Sel.Name]; bad {
		return selectorName(sel), true
	}
	if pkg, ok := sel.X.(*ast.Ident); ok {
		if _, bad := forbiddenPkgs[pkg.Name]; bad {
			return selectorName(sel), true
		}
	}
	return "", false
}

func selectorName(sel *ast.SelectorExpr) string {
	if pkg, ok := sel.X.(*ast.Ident); ok {
		return pkg.Name + "." + sel.Sel.Name
	}
	return sel.Sel.Name
}

func readLines(path string) []string {
	f, err := os.Open(path)
	if err != nil {
		return nil
	}
	defer f.Close()
	var lines []string
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for sc.Scan() {
		lines = append(lines, sc.Text())
	}
	if sc.Err() != nil {
		// Partial reads only weaken waiver detection, never correctness of the
		// AST findings; return what we have rather than aborting the check.
		return lines
	}
	return lines
}

func lineText(lines []string, n int) string {
	if n >= 1 && n <= len(lines) {
		return lines[n-1]
	}
	return ""
}

func hasWaiver(lines []string, n int) bool {
	return strings.Contains(lineText(lines, n), waiverToken)
}
