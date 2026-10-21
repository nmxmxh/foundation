//go:build ignore

// Movement accounting guards cover the declared functions and benchmark fixtures only.
// They do not measure hardware traffic or inspect an entire program.
package main

import (
	"bufio"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"go/ast"
	"go/format"
	"go/parser"
	"go/token"
	"math/big"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
)

const movementPackages = "tooling/benchmark_movement_packages.tsv"
const movementBaseline = "tooling/benchmark_movement_baseline.psv"
const movementSources = "tooling/benchmark_movement_sources.psv"

type movementPackage struct {
	module, pkg, bench, tests string
}

type movementBudget struct {
	module, pkg, name, unit string
	ceiling                 uint64
}

var numericCount = regexp.MustCompile(`^[0-9]+(?:\.[0-9]+)?(?:[eE][+-]?[0-9]+)?$`)
var metricUnit = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9_-]*/op$`)
var benchmarkSuffix = regexp.MustCompile(`-[0-9]+$`)
var digestPattern = regexp.MustCompile(`^[a-f0-9]{64}$`)

func main() {
	if err := movementMain(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "[FAIL] movement guard:", err)
		os.Exit(1)
	}
}

func movementMain(args []string) error {
	if len(args) < 2 || len(args) > 3 {
		return fmt.Errorf("usage: checker check|audit|write-audit ROOT [TIMEOUT_HELPER]")
	}
	mode, root := args[0], args[1]
	if mode == "audit" || mode == "write-audit" {
		return auditMovementSources(root, mode == "write-audit")
	}
	if mode != "check" || len(args) != 3 {
		return fmt.Errorf("check requires the timeout helper")
	}
	packages, err := readMovementPackages(root)
	if err != nil {
		return err
	}
	budgets, err := readMovementBudgets(root, packages)
	if err != nil {
		return err
	}
	if err := auditMovementSources(root, false); err != nil {
		return err
	}
	for _, pkg := range packages {
		tests, err := expectedMovementTests(root, pkg, budgets)
		if err != nil {
			return err
		}
		output, err := runMovementPackage(root, args[2], pkg)
		if err != nil {
			return err
		}
		if err := checkMovementTests(output, tests); err != nil {
			return err
		}
		if err := checkMovementOutput(output, pkg, budgets); err != nil {
			return err
		}
	}
	fmt.Printf("movement guard passed: %d exact ceilings across %d packages\n", len(budgets), len(packages))
	return nil
}

func readMovementRows(path, separator string, fields int) ([][]string, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	var rows [][]string
	scanner := bufio.NewScanner(file)
	for line := 1; scanner.Scan(); line++ {
		text := scanner.Text()
		if strings.TrimSpace(text) == "" || strings.HasPrefix(text, "#") {
			continue
		}
		row := strings.Split(text, separator)
		if len(row) != fields {
			return nil, fmt.Errorf("%s:%d requires %d fields", path, line, fields)
		}
		for _, field := range row {
			if field == "" || field != strings.TrimSpace(field) {
				return nil, fmt.Errorf("%s:%d has an empty or padded field", path, line)
			}
		}
		rows = append(rows, row)
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	if len(rows) == 0 {
		return nil, fmt.Errorf("%s has no records", path)
	}
	return rows, nil
}

func relativeMovementPath(path string) bool {
	return path != "." && !filepath.IsAbs(path) && filepath.Clean(path) == path &&
		!strings.HasPrefix(path, "../") && !strings.ContainsAny(path, "\t\r\n|")
}

func readMovementPackages(root string) ([]movementPackage, error) {
	rows, err := readMovementRows(filepath.Join(root, movementPackages), "\t", 4)
	if err != nil {
		return nil, err
	}
	var packages []movementPackage
	seen := make(map[string]bool)
	for _, row := range rows {
		pkg := movementPackage{row[0], row[1], row[2], row[3]}
		key := pkg.module + "|" + pkg.pkg
		if !relativeMovementPath(pkg.module) || !strings.HasPrefix(pkg.pkg, "./") ||
			!relativeMovementPath(strings.TrimPrefix(pkg.pkg, "./")) || seen[key] {
			return nil, fmt.Errorf("invalid or duplicate movement package: %s", key)
		}
		if !strings.HasPrefix(pkg.bench, "^BenchmarkMovement") || !strings.HasPrefix(pkg.tests, "^TestMovement") {
			return nil, fmt.Errorf("movement package requires anchored accounting fixtures: %s", key)
		}
		for _, pattern := range []string{pkg.bench, pkg.tests} {
			if _, err := regexp.Compile(pattern); err != nil {
				return nil, fmt.Errorf("invalid fixture pattern: %w", err)
			}
		}
		if _, err := os.Stat(filepath.Join(root, pkg.module, "go.mod")); err != nil {
			return nil, fmt.Errorf("configured movement module missing: %w", err)
		}
		seen[key] = true
		packages = append(packages, pkg)
	}
	return packages, nil
}

func exactMovementCount(text string) (uint64, error) {
	if len(text) > 32 || !numericCount.MatchString(text) {
		return 0, fmt.Errorf("invalid movement count %q", text)
	}
	if index := strings.LastIndexAny(text, "eE"); index >= 0 {
		exponent, err := strconv.Atoi(text[index+1:])
		if err != nil || exponent < -16 || exponent > 16 {
			return 0, fmt.Errorf("movement exponent exceeds the exact accounting range: %q", text)
		}
	}
	value, ok := new(big.Rat).SetString(text)
	if !ok || !value.IsInt() || value.Sign() < 0 || value.Num().BitLen() > 53 {
		return 0, fmt.Errorf("movement count must be a finite exact integer: %q", text)
	}
	return value.Num().Uint64(), nil
}

func readMovementBudgets(root string, packages []movementPackage) ([]movementBudget, error) {
	rows, err := readMovementRows(filepath.Join(root, movementBaseline), "|", 5)
	if err != nil {
		return nil, err
	}
	configured := make(map[string]*regexp.Regexp)
	counts := make(map[string]int)
	for _, pkg := range packages {
		configured[pkg.module+"|"+pkg.pkg] = regexp.MustCompile(pkg.bench)
	}
	seen := make(map[string]bool)
	var budgets []movementBudget
	for _, row := range rows {
		key := strings.Join(row[:4], "|")
		pkgKey := strings.Join(row[:2], "|")
		pattern := configured[pkgKey]
		if seen[key] || pattern == nil || !pattern.MatchString(row[2]) || !metricUnit.MatchString(row[3]) {
			return nil, fmt.Errorf("invalid or duplicate movement budget: %s", key)
		}
		if row[3] == "ns/op" || row[3] == "B/op" || row[3] == "allocs/op" {
			return nil, fmt.Errorf("ordinary benchmark metric is not movement accounting: %s", row[3])
		}
		ceiling, err := exactMovementCount(row[4])
		if err != nil {
			return nil, fmt.Errorf("%s: %w", key, err)
		}
		budgets = append(budgets, movementBudget{row[0], row[1], row[2], row[3], ceiling})
		seen[key] = true
		counts[pkgKey]++
	}
	for key := range configured {
		if counts[key] == 0 {
			return nil, fmt.Errorf("configured movement package has no ceilings: %s", key)
		}
	}
	return budgets, nil
}

func movementFunctionName(function *ast.FuncDecl) string {
	if function.Recv == nil {
		return function.Name.Name
	}
	var receiver bytes.Buffer
	if err := format.Node(&receiver, token.NewFileSet(), function.Recv.List[0].Type); err != nil {
		return ""
	}
	return "(" + receiver.String() + ")." + function.Name.Name
}

func movementFunctionDigest(root, path, name string) (string, error) {
	if !relativeMovementPath(path) || !strings.HasSuffix(path, ".go") {
		return "", fmt.Errorf("invalid movement source: %s", path)
	}
	file, err := parser.ParseFile(token.NewFileSet(), filepath.Join(root, path), nil, 0)
	if err != nil {
		return "", err
	}
	for _, declaration := range file.Decls {
		function, ok := declaration.(*ast.FuncDecl)
		if !ok || movementFunctionName(function) != name {
			continue
		}
		if function.Body == nil {
			return "", fmt.Errorf("movement source has no body: %s %s", path, name)
		}
		var normalized bytes.Buffer
		// A fresh file set removes source positions, comments, and blank-line differences.
		if err := format.Node(&normalized, token.NewFileSet(), function); err != nil {
			return "", err
		}
		digest := sha256.Sum256(normalized.Bytes())
		return hex.EncodeToString(digest[:]), nil
	}
	return "", fmt.Errorf("covered movement function missing: %s %s", path, name)
}

func auditMovementSources(root string, write bool) error {
	if write && os.Getenv("HUMAN_SUPERVISED_CHECK_UPDATE") != "1" {
		return fmt.Errorf("source digest updates require HUMAN_SUPERVISED_CHECK_UPDATE=1")
	}
	path := filepath.Join(root, movementSources)
	rows, err := readMovementRows(path, "|", 3)
	if err != nil {
		return err
	}
	seen := make(map[string]bool)
	var output strings.Builder
	output.WriteString("# source|function|normalized_function_sha256\n")
	output.WriteString("# Covered signatures and bodies only. Callee changes require movement review.\n")
	for _, row := range rows {
		key := row[0] + "|" + row[1]
		if seen[key] || !digestPattern.MatchString(row[2]) {
			return fmt.Errorf("invalid or duplicate movement source record: %s", key)
		}
		digest, err := movementFunctionDigest(root, row[0], row[1])
		if err != nil {
			return err
		}
		if !write && digest != row[2] {
			return fmt.Errorf("movement source changed: %s; review copy sites and called helpers before refreshing", key)
		}
		fmt.Fprintf(&output, "%s|%s\n", key, digest)
		seen[key] = true
	}
	if write {
		return writeMovementSources(path, output.String())
	}
	fmt.Printf("[OK] movement source audit: %d covered function declarations\n", len(rows))
	return nil
}

func writeMovementSources(path, contents string) error {
	file, err := os.CreateTemp(filepath.Dir(path), ".movement-sources-*")
	if err != nil {
		return err
	}
	defer os.Remove(file.Name())
	if _, err := file.WriteString(contents); err != nil {
		file.Close()
		return err
	}
	if err := file.Close(); err != nil {
		return err
	}
	return os.Rename(file.Name(), path)
}

func runMovementPackage(root, helper string, pkg movementPackage) ([]byte, error) {
	command := exec.Command("bash", "-c", `source "$1"; shift; run_with_timeout "$@"`,
		"movement", helper, "120", "go", "test", pkg.pkg, "-v", "-tags=foundation_movement",
		"-run="+pkg.tests, "-bench="+pkg.bench, "-benchmem", "-benchtime=200x", "-count=1", "-timeout=90s")
	command.Dir = filepath.Join(root, pkg.module)
	output, err := command.CombinedOutput()
	if err != nil {
		return nil, fmt.Errorf("movement process failed: %s %s: %w\n%s", pkg.module, pkg.pkg, err, output)
	}
	fmt.Print(string(output))
	return output, nil
}

func expectedMovementTests(root string, pkg movementPackage, budgets []movementBudget) (map[string]bool, error) {
	rows, err := readMovementRows(filepath.Join(root, movementSources), "|", 3)
	if err != nil {
		return nil, err
	}
	directory := filepath.Join(pkg.module, strings.TrimPrefix(pkg.pkg, "./"))
	pattern := regexp.MustCompile(pkg.tests)
	tests := make(map[string]bool)
	benchmarks := make(map[string]bool)
	for _, row := range rows {
		if filepath.Dir(row[0]) != directory || !strings.HasSuffix(row[0], "_test.go") {
			continue
		}
		name := row[1]
		if strings.HasPrefix(name, "TestMovement") {
			if !pattern.MatchString(name) {
				return nil, fmt.Errorf("test selection omits audited movement test: %s", name)
			}
			tests[name] = true
		}
		if strings.HasPrefix(name, "BenchmarkMovement") {
			benchmarks[name] = true
		}
	}
	if len(tests) == 0 {
		return nil, fmt.Errorf("movement package has no audited tests: %s %s", pkg.module, pkg.pkg)
	}
	for _, budget := range budgets {
		if budget.module == pkg.module && budget.pkg == pkg.pkg && !benchmarks[strings.Split(budget.name, "/")[0]] {
			return nil, fmt.Errorf("movement benchmark is not source-audited: %s", budget.name)
		}
	}
	return tests, nil
}

func checkMovementTests(output []byte, expected map[string]bool) error {
	seen := make(map[string]bool)
	scanner := bufio.NewScanner(bytes.NewReader(output))
	for scanner.Scan() {
		line := scanner.Text()
		if !strings.HasPrefix(line, "--- ") {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) < 3 || !strings.HasPrefix(fields[2], "TestMovement") {
			continue
		}
		name := fields[2]
		if !expected[name] || seen[name] || fields[1] != "PASS:" {
			return fmt.Errorf("movement test must pass once and be source-audited: %s", line)
		}
		seen[name] = true
	}
	if err := scanner.Err(); err != nil {
		return err
	}
	for name := range expected {
		if !seen[name] {
			return fmt.Errorf("movement test PASS evidence missing: %s", name)
		}
	}
	return nil
}

func parseMovementRow(fields []string) (map[string]string, error) {
	if len(fields) < 4 || (len(fields)-2)%2 != 0 {
		return nil, fmt.Errorf("malformed benchmark output: %s", strings.Join(fields, " "))
	}
	iterations, err := strconv.ParseUint(fields[1], 10, 64)
	if err != nil || iterations == 0 {
		return nil, fmt.Errorf("invalid benchmark iteration count: %s", fields[1])
	}
	metrics := make(map[string]string)
	for index := 2; index < len(fields); index += 2 {
		unit := fields[index+1]
		if _, duplicate := metrics[unit]; duplicate {
			return nil, fmt.Errorf("duplicate benchmark metric: %s", unit)
		}
		metrics[unit] = fields[index]
	}
	return metrics, nil
}

func checkMovementOutput(output []byte, pkg movementPackage, budgets []movementBudget) error {
	expected := make(map[string]map[string]uint64)
	for _, budget := range budgets {
		if budget.module != pkg.module || budget.pkg != pkg.pkg {
			continue
		}
		if expected[budget.name] == nil {
			expected[budget.name] = make(map[string]uint64)
		}
		expected[budget.name][budget.unit] = budget.ceiling
	}
	seen := make(map[string]bool)
	scanner := bufio.NewScanner(bytes.NewReader(output))
	for scanner.Scan() {
		fields := strings.Fields(scanner.Text())
		if len(fields) == 0 || !strings.HasPrefix(fields[0], "Benchmark") {
			continue
		}
		name := benchmarkSuffix.ReplaceAllString(fields[0], "")
		// Verbose Go benchmarks print a name before their completed measurement.
		if len(fields) == 1 {
			continue
		}
		if seen[name] || expected[name] == nil {
			return fmt.Errorf("duplicate or unbudgeted movement benchmark: %s", name)
		}
		metrics, err := parseMovementRow(fields)
		if err != nil {
			return err
		}
		if err := compareMovementMetrics(name, metrics, expected[name]); err != nil {
			return err
		}
		seen[name] = true
	}
	if err := scanner.Err(); err != nil {
		return err
	}
	for name := range expected {
		if !seen[name] {
			return fmt.Errorf("recorded movement benchmark missing: %s", name)
		}
	}
	return nil
}

func compareMovementMetrics(name string, measured map[string]string, expected map[string]uint64) error {
	for unit, text := range measured {
		if unit == "ns/op" || unit == "B/op" || unit == "allocs/op" || unit == "MB/s" {
			continue
		}
		if _, exists := expected[unit]; !exists {
			return fmt.Errorf("unbudgeted movement metric: %s %s", name, unit)
		}
		if _, err := exactMovementCount(text); err != nil {
			return fmt.Errorf("%s %s: %w", name, unit, err)
		}
	}
	for unit, ceiling := range expected {
		text, exists := measured[unit]
		if !exists {
			return fmt.Errorf("recorded movement metric missing: %s %s", name, unit)
		}
		value, err := exactMovementCount(text)
		if err != nil {
			return fmt.Errorf("%s %s: %w", name, unit, err)
		}
		if value > ceiling {
			return fmt.Errorf("movement ceiling exceeded: %s %s measured %d ceiling %d", name, unit, value, ceiling)
		}
	}
	return nil
}
