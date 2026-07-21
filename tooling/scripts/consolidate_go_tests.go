// Command consolidate-go-tests merges redundant Go test files into their
// production-owner test file while preserving declarations and imports.
//
// Usage: go run consolidate_go_tests.go destination_test.go source_test.go...
package main

import (
	"fmt"
	"go/ast"
	"go/format"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

func main() {
	if len(os.Args) > 1 && os.Args[1] == "--" {
		os.Args = append(os.Args[:1], os.Args[2:]...)
	}
	if len(os.Args) < 3 {
		fmt.Fprintln(os.Stderr, "usage: consolidate_go_tests.go destination_test.go source_test.go...")
		os.Exit(2)
	}
	destination := resolveTestPath(os.Args[1])
	sources := make([]string, 0, len(os.Args)-2)
	for _, arg := range os.Args[2:] {
		sources = append(sources, resolveTestPath(arg))
	}
	paths := append([]string{destination}, sources...)
	fset := token.NewFileSet()
	var merged *ast.File
	imports := make(map[string]*ast.ImportSpec)

	for _, path := range paths {
		file, err := parser.ParseFile(fset, path, nil, parser.ParseComments)
		if err != nil {
			fatalf("parse %s: %v", path, err)
		}
		decls := append([]ast.Decl(nil), file.Decls...)
		if merged == nil {
			merged = file
			merged.Decls = nil
		} else if file.Name.Name != merged.Name.Name {
			fatalf("package mismatch: %s uses %s, expected %s", path, file.Name.Name, merged.Name.Name)
		}
		for _, spec := range file.Imports {
			key := spec.Path.Value
			if spec.Name != nil {
				key = spec.Name.Name + " " + key
			}
			imports[key] = spec
		}
		for _, decl := range decls {
			gen, isImport := decl.(*ast.GenDecl)
			if isImport && gen.Tok == token.IMPORT {
				continue
			}
			merged.Decls = append(merged.Decls, decl)
		}
	}

	keys := make([]string, 0, len(imports))
	for key := range imports {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	if len(keys) > 0 {
		decl := &ast.GenDecl{Tok: token.IMPORT, Lparen: 1}
		for _, key := range keys {
			spec := imports[key]
			copySpec := &ast.ImportSpec{Path: &ast.BasicLit{Kind: token.STRING, Value: strconv.Quote(unquote(spec.Path.Value))}}
			if spec.Name != nil {
				copySpec.Name = ast.NewIdent(spec.Name.Name)
			}
			decl.Specs = append(decl.Specs, copySpec)
		}
		merged.Decls = append([]ast.Decl{decl}, merged.Decls...)
	}

	tmp, err := os.CreateTemp(filepath.Dir(destination), ".consolidate-*.go")
	if err != nil {
		fatalf("create temporary file: %v", err)
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)
	if err := format.Node(tmp, fset, merged); err != nil {
		if closeErr := tmp.Close(); closeErr != nil {
			fatalf("format merged file: %v (close temporary file: %v)", err, closeErr)
		}
		fatalf("format merged file: %v", err)
	}
	if err := tmp.Close(); err != nil {
		fatalf("close merged file: %v", err)
	}
	// #nosec G703 -- destination passed through resolveTestPath, which rejects
	// any path outside the working directory or not named *_test.go.
	if err := os.Rename(tmpName, destination); err != nil {
		fatalf("replace %s: %v", destination, err)
	}
	for _, path := range sources {
		// #nosec G703 -- sources passed through resolveTestPath, which rejects
		// any path outside the working directory or not named *_test.go.
		if err := os.Remove(path); err != nil {
			fatalf("remove %s: %v", path, err)
		}
	}
}

// resolveTestPath validates a caller-supplied path before the tool reads,
// replaces, or deletes it. The tool consolidates Go test files inside the
// repository it runs in, so a path that escapes the working directory or does
// not name a test file is a malformed invocation. Rejecting it here keeps the
// later Rename and Remove calls off attacker- or typo-controlled locations.
func resolveTestPath(path string) string {
	root, err := os.Getwd()
	if err != nil {
		fatalf("resolve working directory: %v", err)
	}
	absolute, err := filepath.Abs(filepath.Clean(path))
	if err != nil {
		fatalf("resolve %s: %v", path, err)
	}
	relative, err := filepath.Rel(root, absolute)
	if err != nil {
		fatalf("resolve %s against working directory: %v", path, err)
	}
	if relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		fatalf("refusing path outside the working directory: %s", path)
	}
	if !strings.HasSuffix(absolute, "_test.go") {
		fatalf("refusing path that is not a Go test file: %s", path)
	}
	return absolute
}

func unquote(value string) string {
	unquoted, err := strconv.Unquote(value)
	if err != nil {
		fatalf("unquote import %s: %v", value, err)
	}
	return unquoted
}

func fatalf(format string, args ...any) {
	fmt.Fprintf(os.Stderr, format+"\n", args...)
	os.Exit(1)
}
