//go:build ignore

// transport_ladder_check.go enforces rule 1 of the transport ladder in
// docs/performance_practices.md ("Network and transport performance"):
//
//	Same-process hot dispatch uses direct typed calls, direct frame dispatch,
//	worker channels, or runtime buffer dispatch. It must not use gRPC, HTTP,
//	Redis, or JSON for convenience.
//
// Until this gate existed the rule was doctrine with no enforcement: the ladder
// was the best-designed part of the performance doc and nothing stopped a
// same-process handler from reaching for a network transport.
//
// This is an AST gate, not a regex heuristic. It parses each Go file, finds
// frame-handler registrations (RegisterFrame, RegisterFrameHandler,
// RegisterTypedFrame) whose argument is a function literal, and walks that
// literal's body. A registered frame handler IS the same-process hot dispatch
// lane by definition, which makes it the precise place to enforce rule 1.
//
// Two high-precision shapes are flagged inside a handler body:
//
//  1. calls qualified by a network transport package identifier
//     (http, grpc, redis, nats, kafka), e.g. http.Get(...), redis.Do(...);
//  2. calls into encoding/json (Marshal, Unmarshal, NewEncoder, NewDecoder,
//     MarshalIndent) — the "JSON for convenience" clause. Hot dispatch keeps
//     payloads as bytes or typed structs; see allocation-discipline rules 5-7.
//
// Generic method names (Get/Set/Do/Send) are intentionally NOT flagged unless
// package-qualified, to keep false positives near zero.
//
// Known gap, stated rather than hidden: indirection. A handler that calls an
// out-of-line helper which itself performs a network hop cannot be caught
// without whole-program type analysis. The same limitation applies to
// atomic_lane_purity_check.go. Keep such helpers off the dispatch path.
//
// Waiver: append `// perf:allow-transport-hop` to the offending line (or to the
// registration line) when a handler must legitimately cross a boundary — for
// example when forwarding an externally supplied JSON contract unchanged.
// Record the rationale per the practice-control exception process.
//
// Usage: go run transport_ladder_check.go [target-dir]
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

// registrars name the functions that install a same-process frame handler.
var registrars = map[string]struct{}{
	"RegisterFrame":        {},
	"RegisterFrameHandler": {},
	"RegisterTypedFrame":   {},
}

// forbiddenPkgs are identifiers that, used as a call receiver inside a frame
// handler, indicate the handler left the same-process lane.
var forbiddenPkgs = map[string]struct{}{
	"http":  {},
	"grpc":  {},
	"redis": {},
	"nats":  {},
	"kafka": {},
}

// forbiddenJSON are the encoding/json entry points that constitute "JSON for
// convenience" on a hot dispatch path.
var forbiddenJSON = map[string]struct{}{
	"Marshal":       {},
	"MarshalIndent": {},
	"Unmarshal":     {},
	"NewEncoder":    {},
	"NewDecoder":    {},
}

const waiverToken = "perf:allow-transport-hop"

type finding struct {
	pos    token.Position
	call   string
	reason string
	line   string
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
			if !ok || !isRegistrar(call.Fun) {
				return true
			}
			lit := funcLitArg(call.Args)
			if lit == nil {
				return true
			}
			regLine := fset.Position(call.Pos()).Line
			ast.Inspect(lit.Body, func(bn ast.Node) bool {
				inner, ok := bn.(*ast.CallExpr)
				if !ok {
					return true
				}
				name, reason, bad := forbiddenCall(inner.Fun)
				if !bad {
					return true
				}
				pos := fset.Position(inner.Pos())
				if hasWaiver(lines, pos.Line) || hasWaiver(lines, regLine) {
					return true
				}
				findings = append(findings, finding{
					pos:    pos,
					call:   name,
					reason: reason,
					line:   lineText(lines, pos.Line),
				})
				return true
			})
			return true
		})
		return nil
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "transport ladder check: %v\n", err)
		os.Exit(2)
	}

	label := "transport ladder rule 1: no network transport or JSON inside same-process frame dispatch"
	if len(findings) == 0 {
		fmt.Printf("[OK] %s\n", label)
		return
	}
	fmt.Printf("[FAIL] %s\n", label)
	for _, f := range findings {
		fmt.Printf("  %s:%d: %s() inside a frame handler (%s)\n", f.pos.Filename, f.pos.Line, f.call, f.reason)
		if f.line != "" {
			fmt.Printf("      %s\n", strings.TrimSpace(f.line))
		}
	}
	fmt.Printf("  docs/performance_practices.md, transport ladder rule 1: pick the lowest\n")
	fmt.Printf("  rung that preserves the required process boundary. Move the hop out of the\n")
	fmt.Printf("  handler, or annotate the line with // %s\n", waiverToken)
	os.Exit(1)
}

// isRegistrar reports whether fn names a frame-handler registration, whether
// invoked bare, as a package selector, or as a method.
func isRegistrar(fn ast.Expr) bool {
	switch f := fn.(type) {
	case *ast.Ident:
		_, ok := registrars[f.Name]
		return ok
	case *ast.SelectorExpr:
		_, ok := registrars[f.Sel.Name]
		return ok
	}
	return false
}

// funcLitArg returns the first function-literal argument, which is the handler.
func funcLitArg(args []ast.Expr) *ast.FuncLit {
	for _, arg := range args {
		if lit, ok := arg.(*ast.FuncLit); ok {
			return lit
		}
	}
	return nil
}

// forbiddenCall returns a display name, a human reason, and whether the call
// leaves the same-process lane.
func forbiddenCall(fn ast.Expr) (string, string, bool) {
	sel, ok := fn.(*ast.SelectorExpr)
	if !ok {
		return "", "", false
	}
	pkg, ok := sel.X.(*ast.Ident)
	if !ok {
		return "", "", false
	}
	if _, bad := forbiddenPkgs[pkg.Name]; bad {
		return selectorName(sel), "network transport on a same-process path", true
	}
	if pkg.Name == "json" {
		if _, bad := forbiddenJSON[sel.Sel.Name]; bad {
			return selectorName(sel), "JSON codec on a hot dispatch path", true
		}
	}
	return "", "", false
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
