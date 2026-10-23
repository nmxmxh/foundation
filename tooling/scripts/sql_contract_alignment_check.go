//go:build ignore

// sql_contract_alignment_check.go verifies SQL statements, Go row scans,
// positional query arguments, migration schemas, and Hermes projection fields.
//
// This tool runs offline without a database connection. It parses SQL
// migrations and Go AST files under the target directory.
// Add // cp:allow-sql-shape on a call line or scan line to waive an intentional
// custom query shape.
package main

import (
	"bufio"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

const sqlShapeWaiver = "cp:allow-sql-shape"

type columnSchema struct {
	Name         string
	SQLType      string
	Position     int
	NotNull      bool
	HasDefault   bool
	IsPrimaryKey bool
	IsUnique     bool
}

type tableSchema struct {
	Name        string
	File        string
	Columns     []*columnSchema
	ColumnByKey map[string]*columnSchema
}

type migrationCatalog struct {
	Tables   map[string]*tableSchema
	Warnings []string
}

type scanTarget struct {
	Raw  string
	Norm string
}

type scannerSummary struct {
	Pos     token.Position
	Targets []scanTarget
}

type sqlCallInfo struct {
	FuncName   string
	Pos        token.Position
	QueryExpr  ast.Expr
	ScanExpr   ast.Expr
	Args       []ast.Expr
	SpreadArgs bool
}

type finding struct {
	Pos     token.Position
	Message string
	Line    string
}

var (
	placeholderRe = regexp.MustCompile(`\$([0-9]+)`)
	identTokenRe  = regexp.MustCompile(`[A-Za-z_][A-Za-z0-9_]*`)
)

func main() {
	targets := []string{"."}
	if len(os.Args) > 1 {
		targets = os.Args[1:]
	}

	failed := false
	for _, target := range targets {
		if !runTarget(target, len(targets) > 1) {
			failed = true
		}
	}
	if failed {
		os.Exit(1)
	}
}

func runTarget(target string, showPrefix bool) bool {
	catalog, err := loadMigrationCatalog(target)
	if err != nil {
		fmt.Fprintf(os.Stderr, "[FAIL] sql contract alignment check (%s): %v\n", target, err)
		os.Exit(2)
	}

	findings, warnings, checkedCalls, err := analyzeGoPackages(target, catalog)
	if err != nil {
		fmt.Fprintf(os.Stderr, "[FAIL] sql contract alignment check (%s): %v\n", target, err)
		os.Exit(2)
	}

	prefix := ""
	if showPrefix {
		prefix = fmt.Sprintf("[%s] ", filepath.Base(target))
	}

	allWarnings := append([]string{}, catalog.Warnings...)
	allWarnings = append(allWarnings, warnings...)
	for _, w := range allWarnings {
		fmt.Printf("[WARN] %s%s\n", prefix, w)
	}

	label := "SQL schema, scan order, parameter position, and Hermes field alignment"
	if len(findings) == 0 {
		fmt.Printf("[OK] %s%s (%d tables, %d SQL calls verified)\n", prefix, label, len(catalog.Tables), checkedCalls)
		return true
	}

	fmt.Printf("[FAIL] %s%s (%d tables, %d SQL calls verified)\n", prefix, label, len(catalog.Tables), checkedCalls)
	for _, f := range findings {
		fmt.Printf("  %s:%d: %s\n", f.Pos.Filename, f.Pos.Line, f.Message)
		if strings.TrimSpace(f.Line) != "" {
			fmt.Printf("      %s\n", strings.TrimSpace(f.Line))
		}
	}
	fmt.Printf("  Fix the SQL column order, scan destinations, or parameter list, or add // %s with justification.\n", sqlShapeWaiver)
	return false
}

func loadMigrationCatalog(target string) (*migrationCatalog, error) {
	catalog := &migrationCatalog{
		Tables: make(map[string]*tableSchema),
	}
	var migrationDirs []string
	for _, candidate := range []string{
		filepath.Join(target, "migrations"),
		filepath.Join(target, "backend", "migrations"),
	} {
		if info, err := os.Stat(candidate); err == nil && info.IsDir() {
			migrationDirs = append(migrationDirs, candidate)
		}
	}
	if len(migrationDirs) == 0 {
		templateMigrations := filepath.Join(target, "templates", "backend", "migrations")
		if info, err := os.Stat(templateMigrations); err == nil && info.IsDir() {
			migrationDirs = append(migrationDirs, templateMigrations)
		}
	}
	if len(migrationDirs) == 0 {
		return catalog, nil
	}

	var files []string
	var downFiles []string
	for _, dir := range migrationDirs {
		entries, err := os.ReadDir(dir)
		if err != nil {
			return nil, err
		}
		for _, entry := range entries {
			if entry.IsDir() {
				continue
			}
			if strings.HasSuffix(entry.Name(), ".up.sql") {
				files = append(files, filepath.Join(dir, entry.Name()))
			} else if strings.HasSuffix(entry.Name(), ".down.sql") {
				downFiles = append(downFiles, filepath.Join(dir, entry.Name()))
			}
		}
	}
	sort.Strings(files)
	sort.Strings(downFiles)

	for _, file := range files {
		raw, err := os.ReadFile(file)
		if err != nil {
			return nil, err
		}
		rel, relErr := filepath.Rel(target, file)
		if relErr != nil {
			rel = file
		}
		parseMigrationSQL(string(raw), rel, catalog)
	}

	dropTableRe := regexp.MustCompile(`(?is)\bDROP\s+TABLE\s+(?:IF\s+EXISTS\s+)?([A-Za-z0-9_."` + "`" + `]+)`)
	for _, dfile := range downFiles {
		raw, err := os.ReadFile(dfile)
		if err != nil {
			continue
		}
		rel, relErr := filepath.Rel(target, dfile)
		if relErr != nil {
			rel = dfile
		}
		clean := stripSQLComments(string(raw))
		var missingDropped []string
		seenDrop := make(map[string]bool)
		for _, m := range dropTableRe.FindAllStringSubmatch(clean, -1) {
			tName := normalizeTableName(m[1])
			if tName == "" || seenDrop[tName] {
				continue
			}
			seenDrop[tName] = true
			if _, ok := catalog.Tables[tName]; !ok {
				missingDropped = append(missingDropped, tName)
			}
		}
		if len(missingDropped) > 0 {
			preview := missingDropped
			suffix := ""
			if len(preview) > 5 {
				suffix = fmt.Sprintf(" (+%d more)", len(preview)-5)
				preview = preview[:5]
			}
			catalog.Warnings = append(catalog.Warnings, fmt.Sprintf(
				"%s: drops table(s) [%s]%s not created in any .up.sql migration (verify .up.sql was not overwritten)",
				rel, strings.Join(preview, ", "), suffix,
			))
		}
	}
	return catalog, nil
}

func stripSQLComments(sql string) string {
	var out strings.Builder
	out.Grow(len(sql))
	n := len(sql)
	inSingleQuote := false
	inDollarQuote := false
	dollarTag := ""

	for i := 0; i < n; i++ {
		if inSingleQuote {
			out.WriteByte(sql[i])
			if sql[i] == '\'' {
				if i+1 < n && sql[i+1] == '\'' {
					out.WriteByte(sql[i+1])
					i++
				} else {
					inSingleQuote = false
				}
			}
			continue
		}
		if inDollarQuote {
			out.WriteByte(sql[i])
			if strings.HasPrefix(sql[i:], dollarTag) {
				for k := 1; k < len(dollarTag); k++ {
					out.WriteByte(sql[i+k])
				}
				i += len(dollarTag) - 1
				inDollarQuote = false
			}
			continue
		}
		if sql[i] == '-' && i+1 < n && sql[i+1] == '-' {
			for i < n && sql[i] != '\n' {
				i++
			}
			out.WriteByte('\n')
			continue
		}
		if sql[i] == '/' && i+1 < n && sql[i+1] == '*' {
			i += 2
			for i+1 < n && !(sql[i] == '*' && sql[i+1] == '/') {
				if sql[i] == '\n' {
					out.WriteByte('\n')
				}
				i++
			}
			i++
			continue
		}
		if sql[i] == '\'' {
			inSingleQuote = true
			out.WriteByte(sql[i])
			continue
		}
		if sql[i] == '$' {
			if tag := matchDollarTag(sql[i:]); tag != "" {
				inDollarQuote = true
				dollarTag = tag
				out.WriteString(tag)
				i += len(tag) - 1
				continue
			}
		}
		out.WriteByte(sql[i])
	}
	return out.String()
}

func matchDollarTag(s string) string {
	if len(s) < 2 || s[0] != '$' {
		return ""
	}
	if s[1] == '$' {
		return "$$"
	}
	for i := 1; i < len(s); i++ {
		ch := s[i]
		if ch == '$' {
			return s[:i+1]
		}
		if !((ch >= 'a' && ch <= 'z') || (ch >= 'A' && ch <= 'Z') || (ch >= '0' && ch <= '9') || ch == '_') {
			return ""
		}
	}
	return ""
}

func parseMigrationSQL(raw, relFile string, catalog *migrationCatalog) {
	cleaned := stripSQLComments(raw)
	upper := strings.ToUpper(cleaned)
	idx := 0
	for idx < len(cleaned) {
		createPos := indexKeyword(upper, "CREATE", idx)
		alterPos := indexKeyword(upper, "ALTER", idx)
		nextPos := -1
		isCreate := false
		if createPos >= 0 && (alterPos < 0 || createPos < alterPos) {
			nextPos = createPos
			isCreate = true
		} else if alterPos >= 0 {
			nextPos = alterPos
		}
		if nextPos < 0 {
			break
		}
		if isCreate {
			endPos := parseCreateTableAt(cleaned, upper, nextPos, relFile, catalog)
			if endPos > nextPos {
				idx = endPos
			} else {
				idx = nextPos + len("CREATE")
			}
		} else {
			endPos := parseAlterTableAt(cleaned, upper, nextPos, catalog)
			if endPos > nextPos {
				idx = endPos
			} else {
				idx = nextPos + len("ALTER")
			}
		}
	}
}

func indexKeyword(upper, kw string, start int) int {
	for i := start; i <= len(upper)-len(kw); i++ {
		if upper[i:i+len(kw)] != kw {
			continue
		}
		if i > 0 && isIdentChar(upper[i-1]) {
			continue
		}
		after := i + len(kw)
		if after < len(upper) && isIdentChar(upper[after]) {
			continue
		}
		return i
	}
	return -1
}

func isIdentChar(b byte) bool {
	return (b >= 'A' && b <= 'Z') || (b >= 'a' && b <= 'z') || (b >= '0' && b <= '9') || b == '_'
}

func parseCreateTableAt(cleaned, upper string, pos int, relFile string, catalog *migrationCatalog) int {
	rest := cleaned[pos:]
	re := regexp.MustCompile(`(?is)^CREATE\s+TABLE\s+(?:IF\s+NOT\s+EXISTS\s+)?([A-Za-z0-9_."` + "`" + `]+)\s*\(`)
	m := re.FindStringSubmatchIndex(rest)
	if m == nil {
		return pos + len("CREATE")
	}
	rawTableName := rest[m[2]:m[3]]
	tableName := normalizeTableName(rawTableName)
	openParenIdx := pos + m[1] - 1
	closeParenIdx := findMatchingParen(cleaned, openParenIdx)
	if closeParenIdx < 0 {
		return pos + m[1]
	}
	body := cleaned[openParenIdx+1 : closeParenIdx]
	entries := splitTopLevelCSV(body)
	tbl := &tableSchema{
		Name:        tableName,
		File:        relFile,
		ColumnByKey: make(map[string]*columnSchema),
	}

	seenAuditTimestamp := false
	lateDomainCols := []string{}
	for _, entry := range entries {
		trimmed := strings.TrimSpace(entry)
		if trimmed == "" {
			continue
		}
		upperEntry := strings.ToUpper(trimmed)
		if isTableConstraintClause(upperEntry) {
			applyTableConstraint(trimmed, tbl)
			continue
		}
		col := parseColumnDef(trimmed, len(tbl.Columns))
		if col == nil {
			continue
		}
		tbl.Columns = append(tbl.Columns, col)
		tbl.ColumnByKey[col.Name] = col

		if col.Name == "created_at" || col.Name == "updated_at" {
			seenAuditTimestamp = true
		} else if seenAuditTimestamp && !strings.HasSuffix(col.Name, "_at") && col.Name != "deleted_by" {
			lateDomainCols = append(lateDomainCols, col.Name)
		}
	}

	if len(lateDomainCols) > 0 && !isSystemTable(tableName) {
		catalog.Warnings = append(catalog.Warnings, fmt.Sprintf(
			"%s: table %s places domain column(s) [%s] after created_at/updated_at; keep canonical order (id, organization_id, domain columns, created_at, updated_at)",
			relFile, tableName, strings.Join(lateDomainCols, ", "),
		))
	}

	catalog.Tables[tableName] = tbl
	return closeParenIdx + 1
}

func isSystemTable(tableName string) bool {
	if tableName == "governance_state_records" || tableName == "foundation_event_log" || tableName == "schema_migrations" {
		return true
	}
	return strings.HasPrefix(tableName, "river_")
}

func isTableConstraintClause(upperEntry string) bool {
	for _, prefix := range []string{
		"CONSTRAINT ",
		"PRIMARY KEY",
		"UNIQUE ",
		"UNIQUE(",
		"FOREIGN KEY",
		"CHECK ",
		"CHECK(",
		"EXCLUDE ",
		"LIKE ",
	} {
		if strings.HasPrefix(upperEntry, prefix) {
			return true
		}
	}
	return false
}

func applyTableConstraint(clause string, tbl *tableSchema) {
	upper := strings.ToUpper(clause)
	isPK := strings.Contains(upper, "PRIMARY KEY")
	isUnique := strings.Contains(upper, "UNIQUE")
	if !isPK && !isUnique {
		return
	}
	openIdx := strings.IndexByte(clause, '(')
	if openIdx < 0 {
		return
	}
	closeIdx := findMatchingParen(clause, openIdx)
	if closeIdx < 0 {
		return
	}
	cols := splitTopLevelCSV(clause[openIdx+1 : closeIdx])
	for _, rawCol := range cols {
		colName := normalizeIdent(rawCol)
		if col, ok := tbl.ColumnByKey[colName]; ok {
			if isPK {
				col.IsPrimaryKey = true
				col.NotNull = true
			}
			if isUnique && len(cols) == 1 {
				col.IsUnique = true
			}
		}
	}
}

func parseColumnDef(def string, position int) *columnSchema {
	fields := strings.Fields(def)
	if len(fields) < 2 {
		return nil
	}
	name := normalizeIdent(fields[0])
	if name == "" {
		return nil
	}
	sqlType := strings.ToLower(fields[1])
	upper := strings.ToUpper(def)
	isPK := strings.Contains(upper, "PRIMARY KEY")
	notNull := isPK || strings.Contains(upper, "NOT NULL")
	hasDefault := strings.Contains(upper, "DEFAULT ") ||
		strings.Contains(upper, "GENERATED ") ||
		strings.Contains(sqlType, "serial")
	isUnique := strings.Contains(upper, "UNIQUE")

	return &columnSchema{
		Name:         name,
		SQLType:      sqlType,
		Position:     position,
		NotNull:      notNull,
		HasDefault:   hasDefault,
		IsPrimaryKey: isPK,
		IsUnique:     isUnique,
	}
}

func parseAlterTableAt(cleaned, upper string, pos int, catalog *migrationCatalog) int {
	semi := strings.IndexByte(cleaned[pos:], ';')
	if semi < 0 {
		semi = len(cleaned) - pos
	}
	stmt := cleaned[pos : pos+semi]
	re := regexp.MustCompile(`(?is)^ALTER\s+TABLE\s+(?:IF\s+EXISTS\s+)?(?:ONLY\s+)?([A-Za-z0-9_."` + "`" + `]+)\s+(.*)$`)
	m := re.FindStringSubmatch(stmt)
	if m == nil {
		return pos + semi + 1
	}
	tableName := normalizeTableName(m[1])
	tbl, ok := catalog.Tables[tableName]
	if !ok {
		return pos + semi + 1
	}
	actions := splitTopLevelCSV(m[2])
	for _, action := range actions {
		trimmed := strings.TrimSpace(action)
		addRe := regexp.MustCompile(`(?is)^ADD\s+(?:COLUMN\s+)?(?:IF\s+NOT\s+EXISTS\s+)?([A-Za-z0-9_"` + "`" + `]+)\s+(.+)$`)
		if am := addRe.FindStringSubmatch(trimmed); am != nil {
			colName := normalizeIdent(am[1])
			if !isTableConstraintClause(strings.ToUpper(colName + " " + am[2])) {
				if _, exists := tbl.ColumnByKey[colName]; !exists {
					col := parseColumnDef(colName+" "+am[2], len(tbl.Columns))
					if col != nil {
						tbl.Columns = append(tbl.Columns, col)
						tbl.ColumnByKey[col.Name] = col
					}
				}
			}
			continue
		}
		dropRe := regexp.MustCompile(`(?is)^DROP\s+(?:COLUMN\s+)?(?:IF\s+EXISTS\s+)?([A-Za-z0-9_"` + "`" + `]+)`)
		if dm := dropRe.FindStringSubmatch(trimmed); dm != nil {
			colName := normalizeIdent(dm[1])
			delete(tbl.ColumnByKey, colName)
			continue
		}
		renameRe := regexp.MustCompile(`(?is)^RENAME\s+(?:COLUMN\s+)?([A-Za-z0-9_"` + "`" + `]+)\s+TO\s+([A-Za-z0-9_"` + "`" + `]+)`)
		if rm := renameRe.FindStringSubmatch(trimmed); rm != nil {
			oldName := normalizeIdent(rm[1])
			newName := normalizeIdent(rm[2])
			if col, exists := tbl.ColumnByKey[oldName]; exists {
				delete(tbl.ColumnByKey, oldName)
				col.Name = newName
				tbl.ColumnByKey[newName] = col
			}
			continue
		}
		alterColRe := regexp.MustCompile(`(?is)^ALTER\s+(?:COLUMN\s+)?([A-Za-z0-9_"` + "`" + `]+)\s+(.+)$`)
		if ac := alterColRe.FindStringSubmatch(trimmed); ac != nil {
			colName := normalizeIdent(ac[1])
			restUpper := strings.ToUpper(ac[2])
			if col, exists := tbl.ColumnByKey[colName]; exists {
				if strings.Contains(restUpper, "DROP NOT NULL") {
					col.NotNull = false
				} else if strings.Contains(restUpper, "SET NOT NULL") {
					col.NotNull = true
				}
				if strings.Contains(restUpper, "SET DEFAULT") {
					col.HasDefault = true
				} else if strings.Contains(restUpper, "DROP DEFAULT") {
					col.HasDefault = false
				}
			}
		}
	}
	return pos + semi + 1
}

func findMatchingParen(s string, openIdx int) int {
	depth := 0
	inSingleQuote := false
	for i := openIdx; i < len(s); i++ {
		ch := s[i]
		if inSingleQuote {
			if ch == '\'' {
				if i+1 < len(s) && s[i+1] == '\'' {
					i++
				} else {
					inSingleQuote = false
				}
			}
			continue
		}
		switch ch {
		case '\'':
			inSingleQuote = true
		case '(':
			depth++
		case ')':
			depth--
			if depth == 0 {
				return i
			}
		}
	}
	return -1
}

func splitTopLevelCSV(s string) []string {
	var parts []string
	start := 0
	depth := 0
	caseDepth := 0
	inSingleQuote := false
	upper := strings.ToUpper(s)

	for i := 0; i < len(s); i++ {
		ch := s[i]
		if inSingleQuote {
			if ch == '\'' {
				if i+1 < len(s) && s[i+1] == '\'' {
					i++
				} else {
					inSingleQuote = false
				}
			}
			continue
		}
		switch ch {
		case '\'':
			inSingleQuote = true
		case '(':
			depth++
		case ')':
			if depth > 0 {
				depth--
			}
		case ',':
			if depth == 0 && caseDepth == 0 {
				parts = append(parts, s[start:i])
				start = i + 1
			}
		default:
			if depth == 0 {
				if matchWordAt(upper, "CASE", i) {
					caseDepth++
				} else if caseDepth > 0 && matchWordAt(upper, "END", i) {
					caseDepth--
				}
			}
		}
	}
	parts = append(parts, s[start:])
	return parts
}

func matchWordAt(upper, word string, i int) bool {
	if i+len(word) > len(upper) || upper[i:i+len(word)] != word {
		return false
	}
	if i > 0 && isIdentChar(upper[i-1]) {
		return false
	}
	after := i + len(word)
	if after < len(upper) && isIdentChar(upper[after]) {
		return false
	}
	return true
}

func normalizeTableName(raw string) string {
	trimmed := strings.TrimSpace(raw)
	trimmed = strings.Trim(trimmed, "`\"")
	if dot := strings.LastIndexByte(trimmed, '.'); dot >= 0 {
		trimmed = trimmed[dot+1:]
	}
	trimmed = strings.Trim(trimmed, "`\"")
	return strings.ToLower(trimmed)
}

func normalizeIdent(raw string) string {
	trimmed := strings.TrimSpace(raw)
	trimmed = strings.Trim(trimmed, "`\"")
	return strings.ToLower(trimmed)
}

func analyzeGoPackages(target string, catalog *migrationCatalog) ([]finding, []string, int, error) {
	var findings []finding
	var warnings []string
	checkedCalls := 0

	pkgFiles := make(map[string][]string)
	err := filepath.Walk(target, func(path string, info os.FileInfo, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if info.IsDir() {
			name := info.Name()
			if path != target && strings.HasPrefix(name, ".") {
				return filepath.SkipDir
			}
			switch name {
			case "node_modules", ".git", ".cache", "tmp", "dist", "target", "vendor", "testdata", "generated", "scripts", "build", "coverage":
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
		rel, relErr := filepath.Rel(target, path)
		if relErr == nil {
			slash := filepath.ToSlash(rel)
			if strings.HasPrefix(slash, "tooling/scripts/") || strings.HasPrefix(slash, "foundation/tooling/scripts/") {
				return nil
			}
		}
		dir := filepath.Dir(path)
		pkgFiles[dir] = append(pkgFiles[dir], path)
		return nil
	})
	if err != nil {
		return nil, nil, 0, err
	}

	dirs := make([]string, 0, len(pkgFiles))
	for dir := range pkgFiles {
		dirs = append(dirs, dir)
	}
	sort.Strings(dirs)

	for _, dir := range dirs {
		files := pkgFiles[dir]
		sort.Strings(files)
		pkgFindings, pkgWarnings, pkgCalls := inspectPackageFiles(files, catalog)
		findings = append(findings, pkgFindings...)
		warnings = append(warnings, pkgWarnings...)
		checkedCalls += pkgCalls
	}

	return findings, warnings, checkedCalls, nil
}

type parsedGoFile struct {
	Path  string
	File  *ast.File
	Lines []string
}

func inspectPackageFiles(paths []string, catalog *migrationCatalog) ([]finding, []string, int) {
	fset := token.NewFileSet()
	var parsed []*parsedGoFile
	for _, p := range paths {
		f, err := parser.ParseFile(fset, p, nil, 0)
		if err != nil {
			continue
		}
		parsed = append(parsed, &parsedGoFile{
			Path:  p,
			File:  f,
			Lines: readLines(p),
		})
	}
	if len(parsed) == 0 {
		return nil, nil, 0
	}

	stringConsts, intConsts := resolvePackageConsts(parsed)
	scanners := collectPackageScannerHelpers(fset, parsed)
	recordFields := collectPackageRecordFields(parsed, stringConsts, intConsts)
	hermesFindings := checkPackageHermesSpecs(fset, parsed, stringConsts, intConsts, recordFields, catalog)
	projectionMapFindings := checkPackageProjectionMaps(fset, parsed, stringConsts, intConsts, catalog)

	var findings []finding
	var warnings []string
	findings = append(findings, hermesFindings...)
	findings = append(findings, projectionMapFindings...)
	checkedCalls := 0

	for _, pf := range parsed {
		handledCalls := make(map[*ast.CallExpr]bool)

		// First pass: inspect function bodies for local rows/row Query/QueryRow variables paired with Scan.
		ast.Inspect(pf.File, func(n ast.Node) bool {
			var body *ast.BlockStmt
			switch fn := n.(type) {
			case *ast.FuncDecl:
				body = fn.Body
			case *ast.FuncLit:
				body = fn.Body
			}
			if body != nil {
				checkedCalls += inspectFunctionLocalQueryScans(fset, pf, body, stringConsts, intConsts, catalog, handledCalls, &findings)
			}
			return true
		})

		ast.Inspect(pf.File, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok {
				return true
			}

			// Handle QueryRow(...).Scan(...) chained directly.
			if sel, ok := call.Fun.(*ast.SelectorExpr); ok && sel.Sel.Name == "Scan" {
				if innerCall, ok := sel.X.(*ast.CallExpr); ok && isDirectQueryRowCall(innerCall) {
					handledCalls[innerCall] = true
					pos := fset.Position(call.Pos())
					qPos := fset.Position(innerCall.Pos())
					if hasWaiver(pf.Lines, pos.Line) || hasWaiver(pf.Lines, qPos.Line) {
						return true
					}
					sqlText, ok := evalStringExpr(innerCall.Args[1], stringConsts, intConsts)
					if ok && looksLikeSQL(sqlText) {
						checkedCalls++
						scanTargets := extractScanTargets(call.Args)
						callArgs := innerCall.Args[2:]
						spread := innerCall.Ellipsis.IsValid()
						checkSQLAndScan(sqlText, qPos, pf.Lines, &scannerSummary{Pos: pos, Targets: scanTargets}, callArgs, spread, catalog, &findings)
					}
					return true
				}
			}

			if handledCalls[call] {
				return true
			}

			info, ok := classifyDatabaseCall(fset, call)
			if !ok {
				return true
			}
			if hasWaiver(pf.Lines, info.Pos.Line) {
				return true
			}
			sqlText, ok := evalStringExpr(info.QueryExpr, stringConsts, intConsts)
			if !ok || !looksLikeSQL(sqlText) {
				return true
			}
			checkedCalls++
			var summary *scannerSummary
			if info.ScanExpr != nil {
				summary = resolveScannerSummary(fset, info.ScanExpr, scanners)
				if summary != nil && hasWaiver(pf.Lines, summary.Pos.Line) {
					return true
				}
			}
			checkSQLAndScan(sqlText, info.Pos, pf.Lines, summary, info.Args, info.SpreadArgs, catalog, &findings)
			return true
		})
	}

	return findings, warnings, checkedCalls
}

func inspectFunctionLocalQueryScans(
	fset *token.FileSet,
	pf *parsedGoFile,
	body *ast.BlockStmt,
	stringConsts map[string]string,
	intConsts map[string]int64,
	catalog *migrationCatalog,
	handledCalls map[*ast.CallExpr]bool,
	findings *[]finding,
) int {
	type localQueryVar struct {
		call    *ast.CallExpr
		qPos    token.Position
		sqlText string
		args    []ast.Expr
		spread  bool
	}
	queryVars := make(map[string]localQueryVar)
	scanByVar := make(map[string][]*ast.CallExpr)

	// Inspect only statements inside this function body without descending into nested FuncLits.
	var walkNode func(ast.Node)
	walkNode = func(node ast.Node) {
		if node == nil {
			return
		}
		if _, isFuncLit := node.(*ast.FuncLit); isFuncLit {
			return
		}
		switch st := node.(type) {
		case *ast.AssignStmt:
			if len(st.Lhs) >= 1 && len(st.Rhs) == 1 {
				if lhsIdent, ok := st.Lhs[0].(*ast.Ident); ok && lhsIdent.Name != "_" {
					if call, ok := st.Rhs[0].(*ast.CallExpr); ok && isDirectQueryOrQueryRowCall(call) {
						qPos := fset.Position(call.Pos())
						if sqlText, ok := evalStringExpr(call.Args[1], stringConsts, intConsts); ok && looksLikeSQL(sqlText) {
							queryVars[lhsIdent.Name] = localQueryVar{
								call:    call,
								qPos:    qPos,
								sqlText: sqlText,
								args:    call.Args[2:],
								spread:  call.Ellipsis.IsValid(),
							}
						}
					}
				}
			}
		case *ast.CallExpr:
			if sel, ok := st.Fun.(*ast.SelectorExpr); ok && sel.Sel.Name == "Scan" && len(st.Args) > 0 {
				if recvIdent, ok := sel.X.(*ast.Ident); ok {
					scanByVar[recvIdent.Name] = append(scanByVar[recvIdent.Name], st)
				}
			}
		}
		ast.Inspect(node, func(child ast.Node) bool {
			if child == node {
				return true
			}
			if _, isFuncLit := child.(*ast.FuncLit); isFuncLit {
				return false
			}
			walkNode(child)
			return false
		})
	}
	walkNode(body)

	checked := 0
	for varName, qv := range queryVars {
		scans := scanByVar[varName]
		if len(scans) != 1 {
			continue
		}
		handledCalls[qv.call] = true
		scanCall := scans[0]
		scanPos := fset.Position(scanCall.Pos())
		if hasWaiver(pf.Lines, qv.qPos.Line) || hasWaiver(pf.Lines, scanPos.Line) {
			continue
		}
		checked++
		targets := extractScanTargets(scanCall.Args)
		checkSQLAndScan(
			qv.sqlText,
			qv.qPos,
			pf.Lines,
			&scannerSummary{Pos: scanPos, Targets: targets},
			qv.args,
			qv.spread,
			catalog,
			findings,
		)
	}
	return checked
}

func resolvePackageConsts(parsed []*parsedGoFile) (map[string]string, map[string]int64) {
	rawExprs := make(map[string]ast.Expr)
	intConsts := make(map[string]int64)
	for _, pf := range parsed {
		for _, decl := range pf.File.Decls {
			gen, ok := decl.(*ast.GenDecl)
			if !ok || (gen.Tok != token.CONST && gen.Tok != token.VAR) {
				continue
			}
			for _, spec := range gen.Specs {
				vs, ok := spec.(*ast.ValueSpec)
				if !ok {
					continue
				}
				for i, name := range vs.Names {
					if i < len(vs.Values) {
						rawExprs[name.Name] = vs.Values[i]
						if lit, ok := vs.Values[i].(*ast.BasicLit); ok && lit.Kind == token.INT {
							if v, err := strconv.ParseInt(strings.ReplaceAll(lit.Value, "_", ""), 0, 64); err == nil {
								intConsts[name.Name] = v
							}
						}
					}
				}
			}
		}
	}

	resolved := make(map[string]string)
	for pass := 0; pass < 8; pass++ {
		progress := false
		for name, expr := range rawExprs {
			if _, done := resolved[name]; done {
				continue
			}
			if val, ok := evalStringExpr(expr, resolved, intConsts); ok {
				resolved[name] = val
				progress = true
			}
		}
		if !progress {
			break
		}
	}
	return resolved, intConsts
}

func evalStringExpr(expr ast.Expr, consts map[string]string, intConsts map[string]int64) (string, bool) {
	switch e := expr.(type) {
	case *ast.BasicLit:
		if e.Kind != token.STRING {
			return "", false
		}
		val, err := strconv.Unquote(e.Value)
		if err != nil {
			return "", false
		}
		return val, true
	case *ast.Ident:
		val, ok := consts[e.Name]
		return val, ok
	case *ast.ParenExpr:
		return evalStringExpr(e.X, consts, intConsts)
	case *ast.BinaryExpr:
		if e.Op != token.ADD {
			return "", false
		}
		left, lok := evalStringExpr(e.X, consts, intConsts)
		right, rok := evalStringExpr(e.Y, consts, intConsts)
		if !lok || !rok {
			return "", false
		}
		return left + right, true
	case *ast.CallExpr:
		// Support strconv.Itoa(constInt).
		if sel, ok := e.Fun.(*ast.SelectorExpr); ok && sel.Sel.Name == "Itoa" && len(e.Args) == 1 {
			if pkgIdent, ok := sel.X.(*ast.Ident); ok && pkgIdent.Name == "strconv" {
				if v, ok := evalIntExpr(e.Args[0], intConsts); ok {
					return strconv.FormatInt(v, 10), true
				}
			}
		}
	}
	return "", false
}

func evalIntExpr(expr ast.Expr, intConsts map[string]int64) (int64, bool) {
	switch e := expr.(type) {
	case *ast.BasicLit:
		if e.Kind == token.INT {
			v, err := strconv.ParseInt(strings.ReplaceAll(e.Value, "_", ""), 0, 64)
			return v, err == nil
		}
	case *ast.Ident:
		v, ok := intConsts[e.Name]
		return v, ok
	}
	return 0, false
}

func collectPackageScannerHelpers(fset *token.FileSet, parsed []*parsedGoFile) map[string]*scannerSummary {
	helpers := make(map[string]*scannerSummary)
	for _, pf := range parsed {
		for _, decl := range pf.File.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if !ok || fn.Body == nil || fn.Type.Params == nil || len(fn.Type.Params.List) == 0 {
				continue
			}
			var scanCalls []*ast.CallExpr
			ast.Inspect(fn.Body, func(n ast.Node) bool {
				call, ok := n.(*ast.CallExpr)
				if !ok {
					return true
				}
				if sel, ok := call.Fun.(*ast.SelectorExpr); ok && sel.Sel.Name == "Scan" && len(call.Args) > 0 {
					scanCalls = append(scanCalls, call)
				}
				return true
			})
			if len(scanCalls) == 1 {
				sc := scanCalls[0]
				helpers[fn.Name.Name] = &scannerSummary{
					Pos:     fset.Position(sc.Pos()),
					Targets: extractScanTargets(sc.Args),
				}
			}
		}
	}
	return helpers
}

func resolveScannerSummary(fset *token.FileSet, expr ast.Expr, helpers map[string]*scannerSummary) *scannerSummary {
	switch e := expr.(type) {
	case *ast.Ident:
		return helpers[e.Name]
	case *ast.SelectorExpr:
		return helpers[e.Sel.Name]
	case *ast.FuncLit:
		var scanCalls []*ast.CallExpr
		var delegated *scannerSummary
		ast.Inspect(e.Body, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok {
				return true
			}
			if sel, ok := call.Fun.(*ast.SelectorExpr); ok && sel.Sel.Name == "Scan" && len(call.Args) > 0 {
				scanCalls = append(scanCalls, call)
				return true
			}
			switch fn := call.Fun.(type) {
			case *ast.Ident:
				if h, ok := helpers[fn.Name]; ok {
					delegated = h
				}
			case *ast.SelectorExpr:
				if h, ok := helpers[fn.Sel.Name]; ok {
					delegated = h
				}
			}
			return true
		})
		if len(scanCalls) == 1 {
			sc := scanCalls[0]
			return &scannerSummary{
				Pos:     fset.Position(sc.Pos()),
				Targets: extractScanTargets(sc.Args),
			}
		}
		if delegated != nil {
			return delegated
		}
	}
	return nil
}

func extractScanTargets(args []ast.Expr) []scanTarget {
	targets := make([]scanTarget, 0, len(args))
	for _, arg := range args {
		raw := exprTailIdent(arg)
		targets = append(targets, scanTarget{
			Raw:  raw,
			Norm: normalizeFieldToken(raw),
		})
	}
	return targets
}

func exprTailIdent(expr ast.Expr) string {
	switch e := expr.(type) {
	case *ast.UnaryExpr:
		return exprTailIdent(e.X)
	case *ast.ParenExpr:
		return exprTailIdent(e.X)
	case *ast.StarExpr:
		return exprTailIdent(e.X)
	case *ast.Ident:
		return e.Name
	case *ast.SelectorExpr:
		return e.Sel.Name
	case *ast.CallExpr:
		if len(e.Args) == 1 {
			return exprTailIdent(e.Args[0])
		}
		return exprTailIdent(e.Fun)
	case *ast.IndexExpr:
		return exprTailIdent(e.X)
	}
	return ""
}

func normalizeFieldToken(name string) string {
	if name == "" {
		return ""
	}
	// Convert CamelCase / PascalCase to snake_case and strip common local suffixes.
	var out strings.Builder
	for i := 0; i < len(name); i++ {
		ch := name[i]
		if ch >= 'A' && ch <= 'Z' {
			if i > 0 {
				prev := name[i-1]
				prevLower := (prev >= 'a' && prev <= 'z') || (prev >= '0' && prev <= '9')
				nextLower := i+1 < len(name) && (name[i+1] >= 'a' && name[i+1] <= 'z')
				if prevLower || nextLower {
					out.WriteByte('_')
				}
			}
			out.WriteByte(ch + ('a' - 'A'))
		} else {
			out.WriteByte(ch)
		}
	}
	res := strings.Trim(out.String(), "_")
	for _, trimPrefix := range []string{"out_", "dest_", "row_", "rec_", "db_", "sql_", "null_", "raw_"} {
		if strings.HasPrefix(res, trimPrefix) && len(res) > len(trimPrefix) {
			res = strings.TrimPrefix(res, trimPrefix)
			break
		}
	}
	for _, trimSuffix := range []string{"_val", "_str", "_raw", "_bytes", "_json", "_nullable", "_null", "_ptr"} {
		if strings.HasSuffix(res, trimSuffix) && len(res) > len(trimSuffix) {
			res = strings.TrimSuffix(res, trimSuffix)
			break
		}
	}
	return res
}

func isDirectQueryRowCall(call *ast.CallExpr) bool {
	sel, ok := call.Fun.(*ast.SelectorExpr)
	if !ok || sel.Sel.Name != "QueryRow" {
		return false
	}
	return len(call.Args) >= 2
}

func isDirectQueryOrQueryRowCall(call *ast.CallExpr) bool {
	sel, ok := call.Fun.(*ast.SelectorExpr)
	if !ok || (sel.Sel.Name != "Query" && sel.Sel.Name != "QueryRow") {
		return false
	}
	return len(call.Args) >= 2
}

func classifyDatabaseCall(fset *token.FileSet, call *ast.CallExpr) (sqlCallInfo, bool) {
	funcName := callFuncName(call.Fun)
	switch funcName {
	case "QueryOne", "QuerySQLOne", "QueryEach", "QuerySQLEach", "QueryAll", "QuerySQLAll":
		if len(call.Args) < 4 {
			return sqlCallInfo{}, false
		}
		return sqlCallInfo{
			FuncName:   funcName,
			Pos:        fset.Position(call.Pos()),
			QueryExpr:  call.Args[2],
			ScanExpr:   call.Args[3],
			Args:       call.Args[4:],
			SpreadArgs: call.Ellipsis.IsValid(),
		}, true
	case "QueryAllLimit", "QuerySQLAllLimit":
		if len(call.Args) < 5 {
			return sqlCallInfo{}, false
		}
		return sqlCallInfo{
			FuncName:   funcName,
			Pos:        fset.Position(call.Pos()),
			QueryExpr:  call.Args[3],
			ScanExpr:   call.Args[4],
			Args:       call.Args[5:],
			SpreadArgs: call.Ellipsis.IsValid(),
		}, true
	case "ExecCommand", "ExecCommandResult", "ExecRowsAffected", "ExecSQLRowsAffected":
		if len(call.Args) < 3 {
			return sqlCallInfo{}, false
		}
		return sqlCallInfo{
			FuncName:   funcName,
			Pos:        fset.Position(call.Pos()),
			QueryExpr:  call.Args[2],
			Args:       call.Args[3:],
			SpreadArgs: call.Ellipsis.IsValid(),
		}, true
	case "Exec":
		if sel, ok := call.Fun.(*ast.SelectorExpr); ok && sel.Sel.Name == "Exec" && len(call.Args) >= 2 {
			return sqlCallInfo{
				FuncName:   funcName,
				Pos:        fset.Position(call.Pos()),
				QueryExpr:  call.Args[1],
				Args:       call.Args[2:],
				SpreadArgs: call.Ellipsis.IsValid(),
			}, true
		}
	}
	return sqlCallInfo{}, false
}

func callFuncName(fn ast.Expr) string {
	switch e := fn.(type) {
	case *ast.Ident:
		return e.Name
	case *ast.SelectorExpr:
		return e.Sel.Name
	case *ast.IndexExpr:
		return callFuncName(e.X)
	case *ast.IndexListExpr:
		return callFuncName(e.X)
	}
	return ""
}

func looksLikeSQL(s string) bool {
	upper := strings.ToUpper(s)
	for _, kw := range []string{"SELECT ", "SELECT\n", "SELECT\t", "INSERT ", "UPDATE ", "DELETE ", "WITH "} {
		if strings.Contains(upper, kw) {
			return true
		}
	}
	return false
}

func checkSQLAndScan(
	rawSQL string,
	pos token.Position,
	lines []string,
	summary *scannerSummary,
	callArgs []ast.Expr,
	spreadArgs bool,
	catalog *migrationCatalog,
	findings *[]finding,
) {
	cleaned := strings.TrimSpace(stripSQLComments(rawSQL))
	if cleaned == "" {
		return
	}

	// 1. Verify placeholder count $1..$N and contiguity against call arguments when not spread.
	maxPlaceholder, missingPlaceholders := inspectSQLPlaceholders(cleaned)
	if len(missingPlaceholders) > 0 {
		*findings = append(*findings, finding{
			Pos: pos,
			Message: fmt.Sprintf(
				"SQL query references up to $%d but omits placeholder(s) %s",
				maxPlaceholder, strings.Join(missingPlaceholders, ", "),
			),
			Line: lineText(lines, pos.Line),
		})
	}
	if !spreadArgs {
		if maxPlaceholder > 0 && maxPlaceholder != len(callArgs) {
			*findings = append(*findings, finding{
				Pos: pos,
				Message: fmt.Sprintf(
					"SQL query references $%d (%d parameters) but executor call passes %d argument(s)",
					maxPlaceholder, maxPlaceholder, len(callArgs),
				),
				Line: lineText(lines, pos.Line),
			})
		}
	}

	// 2. Verify SELECT / RETURNING projection count, field ordering against Scan, and column existence.
	projCols, tableName, hasProj := extractProjectedColumns(cleaned)
	if hasProj && !containsWildcard(projCols) {
		if summary != nil && len(summary.Targets) > 0 {
			if len(projCols) != len(summary.Targets) {
				*findings = append(*findings, finding{
					Pos: summary.Pos,
					Message: fmt.Sprintf(
						"SQL projection has %d column(s) [%s] but Scan binds %d destination(s) [%s]",
						len(projCols),
						strings.Join(summarizeSQLCols(projCols), ", "),
						len(summary.Targets),
						strings.Join(summarizeScanTargets(summary.Targets), ", "),
					),
					Line: lineText(lines, summary.Pos.Line),
				})
			} else {
				checkScanFieldOrder(projCols, summary, lines, findings)
			}
		}

		aliases := extractTopLevelTableAliases(cleaned, tableName)
		verifyProjectedColumnsExist(projCols, tableName, aliases, catalog, pos, lines, findings)
	}

	// 3. Verify INSERT INTO column list, required NOT NULL columns, ON CONFLICT, and argument order.
	checkInsertStatement(cleaned, pos, lines, callArgs, spreadArgs, catalog, findings)

	// 4. Verify UPDATE statement columns, WHERE scope, and argument order.
	checkUpdateStatement(cleaned, pos, lines, callArgs, spreadArgs, catalog, findings)

	// 5. Verify DELETE statement WHERE scope.
	checkDeleteStatement(cleaned, pos, lines, findings)
}

func inspectSQLPlaceholders(sql string) (int, []string) {
	// Mask single-quoted literals before scanning for $N.
	masked := maskSingleQuotes(sql)
	matches := placeholderRe.FindAllStringSubmatch(masked, -1)
	maxN := 0
	seen := make(map[int]bool)
	for _, m := range matches {
		n, err := strconv.Atoi(m[1])
		if err == nil && n > 0 {
			seen[n] = true
			if n > maxN {
				maxN = n
			}
		}
	}
	var missing []string
	for k := 1; k < maxN; k++ {
		if !seen[k] {
			missing = append(missing, fmt.Sprintf("$%d", k))
		}
	}
	return maxN, missing
}

func maskSingleQuotes(sql string) string {
	var out strings.Builder
	out.Grow(len(sql))
	inQuote := false
	for i := 0; i < len(sql); i++ {
		if inQuote {
			if sql[i] == '\'' {
				if i+1 < len(sql) && sql[i+1] == '\'' {
					out.WriteString("  ")
					i++
				} else {
					inQuote = false
					out.WriteByte('\'')
				}
			} else {
				out.WriteByte(' ')
			}
			continue
		}
		if sql[i] == '\'' {
			inQuote = true
		}
		out.WriteByte(sql[i])
	}
	return out.String()
}

type projectedCol struct {
	Raw       string
	Canonical string
	SimpleCol string
	Qualifier string
}

func extractProjectedColumns(sql string) ([]projectedCol, string, bool) {
	upper := strings.ToUpper(sql)

	// Check top-level RETURNING first (for INSERT/UPDATE/DELETE ... RETURNING).
	if retPos := findTopLevelKeyword(sql, upper, "RETURNING", 0); retPos >= 0 {
		retBody := strings.TrimSpace(sql[retPos+len("RETURNING"):])
		retBody = strings.TrimSuffix(retBody, ";")
		parts := splitTopLevelCSV(retBody)
		tableName := extractMutationTargetTable(sql)
		return parseProjectedColList(parts), tableName, true
	}

	// Find outermost SELECT ... FROM.
	selPos := findOuterSelect(sql, upper)
	if selPos < 0 {
		return nil, "", false
	}
	afterSelect := selPos + len("SELECT")
	fromPos := findTopLevelKeyword(sql, upper, "FROM", afterSelect)
	var projSegment string
	var tableName string
	if fromPos >= 0 {
		projSegment = strings.TrimSpace(sql[afterSelect:fromPos])
		tableName = extractSimpleFromTable(sql[fromPos+len("FROM"):])
		if tableName != "" && isCTEName(sql, upper, tableName) {
			tableName = ""
		}
	} else {
		projSegment = strings.TrimSpace(sql[afterSelect:])
	}

	// Strip leading DISTINCT or DISTINCT ON (...).
	upperProj := strings.ToUpper(projSegment)
	if strings.HasPrefix(upperProj, "DISTINCT") {
		projSegment = strings.TrimSpace(projSegment[len("DISTINCT"):])
		upperProj = strings.ToUpper(projSegment)
		if strings.HasPrefix(upperProj, "ON") {
			rest := strings.TrimSpace(projSegment[len("ON"):])
			if strings.HasPrefix(rest, "(") {
				closeIdx := findMatchingParen(rest, 0)
				if closeIdx >= 0 {
					projSegment = strings.TrimSpace(rest[closeIdx+1:])
				}
			}
		}
	}

	parts := splitTopLevelCSV(projSegment)
	return parseProjectedColList(parts), tableName, true
}

func findOuterSelect(sql, upper string) int {
	idx := 0
	if matchWordAt(upper, "WITH", 0) {
		idx = skipWithClause(sql, upper)
		if idx < 0 {
			return -1
		}
	}
	// Do not treat INSERT INTO ... SELECT as an outer SELECT read query.
	for _, mutKw := range []string{"INSERT", "UPDATE", "DELETE"} {
		if kwPos := findTopLevelKeyword(sql, upper, mutKw, idx); kwPos >= 0 {
			return -1
		}
	}
	return findTopLevelKeyword(sql, upper, "SELECT", idx)
}

func isCTEName(sql, upper, tableName string) bool {
	if !matchWordAt(upper, "WITH", 0) {
		return false
	}
	cteRe := regexp.MustCompile(`(?is)\b([A-Za-z_][A-Za-z0-9_]*)\s+AS\s*\(`)
	for _, m := range cteRe.FindAllStringSubmatch(sql, -1) {
		if strings.EqualFold(m[1], tableName) {
			return true
		}
	}
	return false
}

func skipWithClause(sql, upper string) int {
	i := len("WITH")
	for i < len(sql) && isSpace(sql[i]) {
		i++
	}
	if matchWordAt(upper, "RECURSIVE", i) {
		i += len("RECURSIVE")
	}
	for i < len(sql) {
		openIdx := strings.IndexByte(sql[i:], '(')
		if openIdx < 0 {
			return -1
		}
		absOpen := i + openIdx
		absClose := findMatchingParen(sql, absOpen)
		if absClose < 0 {
			return -1
		}
		i = absClose + 1
		for i < len(sql) && isSpace(sql[i]) {
			i++
		}
		if i < len(sql) && sql[i] == ',' {
			i++
			continue
		}
		return i
	}
	return -1
}

func isSpace(b byte) bool {
	return b == ' ' || b == '\t' || b == '\n' || b == '\r'
}

func findTopLevelKeyword(sql, upper, kw string, start int) int {
	depth := 0
	inQuote := false
	for i := start; i <= len(sql)-len(kw); i++ {
		ch := sql[i]
		if inQuote {
			if ch == '\'' {
				if i+1 < len(sql) && sql[i+1] == '\'' {
					i++
				} else {
					inQuote = false
				}
			}
			continue
		}
		switch ch {
		case '\'':
			inQuote = true
		case '(':
			depth++
		case ')':
			if depth > 0 {
				depth--
			}
		default:
			if depth == 0 && matchWordAt(upper, kw, i) {
				return i
			}
		}
	}
	return -1
}

func parseProjectedColList(parts []string) []projectedCol {
	cols := make([]projectedCol, 0, len(parts))
	for _, raw := range parts {
		trimmed := strings.TrimSpace(raw)
		if trimmed == "" {
			continue
		}
		canonical, simpleCol, qualifier := extractProjectedColIdentity(trimmed)
		cols = append(cols, projectedCol{
			Raw:       trimmed,
			Canonical: canonical,
			SimpleCol: simpleCol,
			Qualifier: qualifier,
		})
	}
	return cols
}

func extractProjectedColIdentity(expr string) (canonical string, simpleCol string, qualifier string) {
	upper := strings.ToUpper(expr)
	baseExpr := expr
	aliasCanonical := ""
	if asPos := findLastTopLevelAs(expr, upper); asPos >= 0 {
		alias := normalizeIdent(expr[asPos+len("AS"):])
		aliasCanonical = normalizeFieldToken(alias)
		baseExpr = strings.TrimSpace(expr[:asPos])
	}

	// Strip type casts like ::text or ::bigint at the end of baseExpr.
	noCast := regexp.MustCompile(`(::[A-Za-z0-9_.\[\]]+\s*)+$`).ReplaceAllString(baseExpr, "")
	noCast = strings.TrimSpace(noCast)

	// Simple column or qualified column (u.organization_id).
	simpleRe := regexp.MustCompile(`^(?:([A-Za-z_][A-Za-z0-9_]*)\.)?([A-Za-z_][A-Za-z0-9_]*)$`)
	if m := simpleRe.FindStringSubmatch(noCast); m != nil {
		col := strings.ToLower(m[2])
		if isSQLReservedColumnWord(col) {
			return aliasCanonical, "", ""
		}
		qual := strings.ToLower(m[1])
		canon := aliasCanonical
		if canon == "" {
			canon = normalizeFieldToken(col)
		}
		return canon, col, qual
	}

	// COALESCE(col, ...) or NULLIF(BTRIM(col), '') -> extract first column argument.
	coalesceRe := regexp.MustCompile(`(?is)^(?:COALESCE|NULLIF|BTRIM)\s*\(\s*(?:(?:COALESCE|NULLIF|BTRIM)\s*\(\s*)*(?:([A-Za-z_][A-Za-z0-9_]*)\.)?([A-Za-z_][A-Za-z0-9_]*)\s*(?:::|\)|,)`)
	if m := coalesceRe.FindStringSubmatch(noCast); m != nil {
		col := strings.ToLower(m[2])
		if isSQLReservedColumnWord(col) {
			return aliasCanonical, "", ""
		}
		qual := strings.ToLower(m[1])
		canon := aliasCanonical
		if canon == "" {
			canon = normalizeFieldToken(col)
		}
		return canon, col, qual
	}

	return aliasCanonical, "", ""
}

func isSQLReservedColumnWord(word string) bool {
	switch word {
	case "true", "false", "null", "current_timestamp", "current_date", "current_time",
		"current_user", "session_user", "localtime", "localtimestamp", "xmin", "xmax", "ctid", "tableoid":
		return true
	}
	return false
}

func findLastTopLevelAs(expr, upper string) int {
	depth := 0
	inQuote := false
	last := -1
	for i := 0; i <= len(expr)-2; i++ {
		ch := expr[i]
		if inQuote {
			if ch == '\'' {
				if i+1 < len(expr) && expr[i+1] == '\'' {
					i++
				} else {
					inQuote = false
				}
			}
			continue
		}
		switch ch {
		case '\'':
			inQuote = true
		case '(':
			depth++
		case ')':
			if depth > 0 {
				depth--
			}
		default:
			if depth == 0 && matchWordAt(upper, "AS", i) {
				last = i
			}
		}
	}
	return last
}

func containsWildcard(cols []projectedCol) bool {
	for _, c := range cols {
		if c.Raw == "*" || strings.HasSuffix(c.Raw, ".*") {
			return true
		}
	}
	return false
}

func checkScanFieldOrder(cols []projectedCol, summary *scannerSummary, lines []string, findings *[]finding) {
	n := len(cols)
	for i := 0; i < n; i++ {
		ci := cols[i].Canonical
		si := summary.Targets[i].Norm
		if ci == "" || si == "" || tokensMatch(ci, si) {
			continue
		}
		for j := i + 1; j < n; j++ {
			cj := cols[j].Canonical
			sj := summary.Targets[j].Norm
			if cj == "" || sj == "" || ci == cj || tokensMatch(cj, sj) {
				continue
			}
			if tokensMatch(ci, sj) && tokensMatch(cj, si) {
				*findings = append(*findings, finding{
					Pos: summary.Pos,
					Message: fmt.Sprintf(
						"positional field swap in Scan: SQL projects column %d (%s) before column %d (%s), but Scan binds &%s before &%s",
						i+1, cols[i].Raw, j+1, cols[j].Raw, summary.Targets[i].Raw, summary.Targets[j].Raw,
					),
					Line: lineText(lines, summary.Pos.Line),
				})
				return
			}
		}
	}
}

func tokensMatch(colNorm, scanNorm string) bool {
	if colNorm == "" || scanNorm == "" {
		return false
	}
	if canonicalSynonym(colNorm) == canonicalSynonym(scanNorm) {
		return true
	}
	return false
}

func canonicalSynonym(tok string) string {
	switch tok {
	case "org_id":
		return "organization_id"
	default:
		return tok
	}
}

func extractTopLevelTableAliases(sql string, defaultTable string) map[string]string {
	aliases := make(map[string]string)
	upper := strings.ToUpper(sql)
	if defaultTable != "" && !isCTEName(sql, upper, defaultTable) {
		aliases[defaultTable] = defaultTable
	}

	startIdx := 0
	if matchWordAt(upper, "WITH", 0) {
		if next := skipWithClause(sql, upper); next >= 0 {
			startIdx = next
		}
	}
	topBody := sql[startIdx:]
	topUpper := upper[startIdx:]

	recordTableAlias := func(rawTable, rawAlias string) {
		tbl := normalizeTableName(rawTable)
		if tbl == "" || isCTEName(sql, upper, tbl) || isSQLAliasStopWord(tbl) {
			return
		}
		aliases[tbl] = tbl
		alias := normalizeIdent(rawAlias)
		if alias != "" && !isSQLAliasStopWord(alias) {
			aliases[alias] = tbl
		}
	}

	// 1. UPDATE <table> [AS] <alias> or DELETE FROM <table> [AS] <alias>.
	mutRe := regexp.MustCompile(`(?is)^\s*(?:UPDATE|DELETE\s+FROM)\s+([A-Za-z0-9_."` + "`" + `]+)(?:\s+(?:AS\s+)?([A-Za-z_][A-Za-z0-9_]*))?`)
	if m := mutRe.FindStringSubmatch(topBody); m != nil {
		recordTableAlias(m[1], m[2])
	}

	// 2. Top-level FROM clause items (supporting comma-separated FROM t1 a1, t2 a2) and JOIN clauses.
	if fromPos := findTopLevelKeyword(topBody, topUpper, "FROM", 0); fromPos >= 0 {
		fromTail := topBody[fromPos+len("FROM"):]
		fromUpper := topUpper[fromPos+len("FROM"):]
		endFrom := len(fromTail)
		for _, stopKw := range []string{"WHERE", "GROUP", "ORDER", "LIMIT", "OFFSET", "FOR", "UNION", "HAVING", "RETURNING", "ON"} {
			if stopKw == "ON" {
				continue
			}
			if pos := findTopLevelKeyword(fromTail, fromUpper, stopKw, 0); pos >= 0 && pos < endFrom {
				endFrom = pos
			}
		}
		fromClause := fromTail[:endFrom]
		fromClauseUpper := fromUpper[:endFrom]

		// Split out the initial FROM list before the first top-level JOIN.
		firstJoin := findAnyTopLevelJoin(fromClause, fromClauseUpper)
		headFrom := fromClause
		if firstJoin >= 0 {
			headFrom = fromClause[:firstJoin]
		}
		for _, item := range splitTopLevelCSV(headFrom) {
			trimmed := strings.TrimSpace(item)
			if strings.HasPrefix(trimmed, "(") {
				continue
			}
			itemRe := regexp.MustCompile(`(?is)^([A-Za-z0-9_."` + "`" + `]+)(?:\s+(?:AS\s+)?([A-Za-z_][A-Za-z0-9_]*))?`)
			if m := itemRe.FindStringSubmatch(trimmed); m != nil {
				recordTableAlias(m[1], m[2])
			}
		}

		// Extract all top-level JOIN <table> [AS] <alias> clauses inside fromClause.
		joinRe := regexp.MustCompile(`(?is)\bJOIN\s+([A-Za-z0-9_."` + "`" + `]+)(?:\s+(?:AS\s+)?([A-Za-z_][A-Za-z0-9_]*))?`)
		for _, jm := range joinRe.FindAllStringSubmatch(fromClause, -1) {
			recordTableAlias(jm[1], jm[2])
		}
	}

	return aliases
}

func findAnyTopLevelJoin(sql, upper string) int {
	earliest := -1
	for _, kw := range []string{"JOIN", "LEFT", "INNER", "RIGHT", "FULL", "CROSS", "LATERAL"} {
		if pos := findTopLevelKeyword(sql, upper, kw, 0); pos >= 0 {
			if earliest < 0 || pos < earliest {
				earliest = pos
			}
		}
	}
	return earliest
}

func isSQLAliasStopWord(word string) bool {
	switch strings.ToLower(word) {
	case "set", "where", "from", "join", "left", "right", "inner", "outer", "full", "cross",
		"on", "using", "order", "group", "by", "limit", "offset", "having", "returning",
		"for", "union", "intersect", "except", "values", "select", "as", "and", "or", "lateral":
		return true
	}
	return false
}

func verifyProjectedColumnsExist(
	cols []projectedCol,
	defaultTable string,
	aliases map[string]string,
	catalog *migrationCatalog,
	pos token.Position,
	lines []string,
	findings *[]finding,
) {
	for _, c := range cols {
		if c.SimpleCol == "" {
			continue
		}
		targetTable := ""
		if c.Qualifier != "" {
			targetTable = aliases[c.Qualifier]
		} else {
			targetTable = defaultTable
		}
		if targetTable == "" {
			continue
		}
		tbl, ok := catalog.Tables[targetTable]
		if !ok || tbl == nil {
			continue
		}
		if _, exists := tbl.ColumnByKey[c.SimpleCol]; !exists {
			*findings = append(*findings, finding{
				Pos: pos,
				Message: fmt.Sprintf(
					"SQL query projects column %q which does not exist on migration table %q (defined in %s)",
					c.SimpleCol, tbl.Name, tbl.File,
				),
				Line: lineText(lines, pos.Line),
			})
		}
	}
}

func extractMutationTargetTable(sql string) string {
	upper := strings.ToUpper(sql)
	top := sql
	if matchWordAt(upper, "WITH", 0) {
		if next := skipWithClause(sql, upper); next >= 0 {
			top = sql[next:]
		}
	}
	insRe := regexp.MustCompile(`(?is)\bINSERT\s+INTO\s+([A-Za-z0-9_."` + "`" + `]+)`)
	if m := insRe.FindStringSubmatch(top); m != nil {
		return normalizeTableName(m[1])
	}
	updRe := regexp.MustCompile(`(?is)\bUPDATE\s+([A-Za-z0-9_."` + "`" + `]+)`)
	if m := updRe.FindStringSubmatch(top); m != nil {
		return normalizeTableName(m[1])
	}
	delRe := regexp.MustCompile(`(?is)\bDELETE\s+FROM\s+([A-Za-z0-9_."` + "`" + `]+)`)
	if m := delRe.FindStringSubmatch(top); m != nil {
		return normalizeTableName(m[1])
	}
	return ""
}

func findEnclosingScopeEnd(sql string, pos int) int {
	var openStack []int
	inQuote := false
	for i := 0; i < pos && i < len(sql); i++ {
		ch := sql[i]
		if inQuote {
			if ch == '\'' {
				if i+1 < len(sql) && sql[i+1] == '\'' {
					i++
				} else {
					inQuote = false
				}
			}
			continue
		}
		switch ch {
		case '\'':
			inQuote = true
		case '(':
			openStack = append(openStack, i)
		case ')':
			if len(openStack) > 0 {
				openStack = openStack[:len(openStack)-1]
			}
		}
	}
	if len(openStack) == 0 {
		return len(sql)
	}
	enclosingOpen := openStack[len(openStack)-1]
	if closeIdx := findMatchingParen(sql, enclosingOpen); closeIdx >= 0 {
		return closeIdx
	}
	return len(sql)
}

func extractSimpleFromTable(fromTail string) string {
	// Only return a table name when the FROM clause is a single unjoined table.
	upper := strings.ToUpper(fromTail)
	for _, stopKw := range []string{"WHERE", "GROUP", "ORDER", "LIMIT", "OFFSET", "FOR", "UNION", "HAVING"} {
		if pos := findTopLevelKeyword(fromTail, upper, stopKw, 0); pos >= 0 {
			fromTail = fromTail[:pos]
			upper = upper[:pos]
		}
	}
	if strings.Contains(upper, "JOIN") || strings.Contains(fromTail, ",") || strings.Contains(fromTail, "(") {
		return ""
	}
	fields := strings.Fields(strings.TrimSpace(fromTail))
	if len(fields) == 0 {
		return ""
	}
	return normalizeTableName(fields[0])
}

func checkInsertStatement(
	sql string,
	pos token.Position,
	lines []string,
	callArgs []ast.Expr,
	spreadArgs bool,
	catalog *migrationCatalog,
	findings *[]finding,
) {
	re := regexp.MustCompile(`(?is)\bINSERT\s+INTO\s+([A-Za-z0-9_."` + "`" + `]+)(?:\s+AS\s+[A-Za-z_][A-Za-z0-9_]*)?\s*\(`)
	matches := re.FindAllStringSubmatchIndex(sql, -1)
	for _, m := range matches {
		scopeEnd := findEnclosingScopeEnd(sql, m[0])
		if scopeEnd <= m[0] {
			continue
		}
		checkSingleInsertScope(sql[m[0]:scopeEnd], pos, lines, callArgs, spreadArgs, catalog, findings)
	}
}

func checkSingleInsertScope(
	sql string,
	pos token.Position,
	lines []string,
	callArgs []ast.Expr,
	spreadArgs bool,
	catalog *migrationCatalog,
	findings *[]finding,
) {
	re := regexp.MustCompile(`(?is)^\s*INSERT\s+INTO\s+([A-Za-z0-9_."` + "`" + `]+)(?:\s+AS\s+[A-Za-z_][A-Za-z0-9_]*)?\s*\(`)
	m := re.FindStringSubmatchIndex(sql)
	if m == nil {
		return
	}
	tableName := normalizeTableName(sql[m[2]:m[3]])
	openColsIdx := m[1] - 1
	closeColsIdx := findMatchingParen(sql, openColsIdx)
	if closeColsIdx < 0 {
		return
	}
	rawCols := splitTopLevelCSV(sql[openColsIdx+1 : closeColsIdx])
	insertCols := make([]string, 0, len(rawCols))
	insertColSet := make(map[string]struct{}, len(rawCols))
	for _, rc := range rawCols {
		col := normalizeIdent(rc)
		if col != "" {
			insertCols = append(insertCols, col)
			insertColSet[col] = struct{}{}
		}
	}

	tbl := catalog.Tables[tableName]
	if tbl != nil {
		for _, col := range insertCols {
			if _, ok := tbl.ColumnByKey[col]; !ok {
				*findings = append(*findings, finding{
					Pos: pos,
					Message: fmt.Sprintf(
						"INSERT INTO %s references column %q which does not exist in migration schema (%s)",
						tableName, col, tbl.File,
					),
					Line: lineText(lines, pos.Line),
				})
			}
		}
		for _, schemaCol := range tbl.Columns {
			if _, alive := tbl.ColumnByKey[schemaCol.Name]; !alive {
				continue
			}
			if schemaCol.NotNull && !schemaCol.HasDefault {
				if _, present := insertColSet[schemaCol.Name]; !present {
					*findings = append(*findings, finding{
						Pos: pos,
						Message: fmt.Sprintf(
							"INSERT INTO %s omits required NOT NULL column %q (no DEFAULT in %s)",
							tableName, schemaCol.Name, tbl.File,
						),
						Line: lineText(lines, pos.Line),
					})
				}
			}
		}
	}

	rest := sql[closeColsIdx+1:]
	restUpper := strings.ToUpper(rest)

	// Verify ON CONFLICT target columns and DO UPDATE SET columns when tbl != nil.
	if tbl != nil {
		checkOnConflictClause(rest, restUpper, tableName, tbl, pos, lines, findings)
	}

	// Case A: VALUES (...), (...) follows.
	valRe := regexp.MustCompile(`(?is)^\s*VALUES\s*\(`)
	if vm := valRe.FindStringSubmatchIndex(rest); vm != nil {
		openValsIdx := closeColsIdx + 1 + vm[1] - 1
		firstTuple := true
		for openValsIdx < len(sql) && sql[openValsIdx] == '(' {
			closeValsIdx := findMatchingParen(sql, openValsIdx)
			if closeValsIdx < 0 {
				break
			}
			valExprs := splitTopLevelCSV(sql[openValsIdx+1 : closeValsIdx])
			if len(valExprs) != len(insertCols) {
				*findings = append(*findings, finding{
					Pos: pos,
					Message: fmt.Sprintf(
						"INSERT INTO %s lists %d column(s) but VALUES tuple supplies %d expression(s)",
						tableName, len(insertCols), len(valExprs),
					),
					Line: lineText(lines, pos.Line),
				})
				return
			}
			if firstTuple && !spreadArgs && len(callArgs) > 0 {
				checkColumnArgOrder("INSERT INTO "+tableName, insertCols, valExprs, callArgs, pos, lines, findings)
			}
			firstTuple = false
			nextIdx := closeValsIdx + 1
			for nextIdx < len(sql) && isSpace(sql[nextIdx]) {
				nextIdx++
			}
			if nextIdx < len(sql) && sql[nextIdx] == ',' {
				nextIdx++
				for nextIdx < len(sql) && isSpace(sql[nextIdx]) {
					nextIdx++
				}
				openValsIdx = nextIdx
			} else {
				break
			}
		}
		return
	}

	// Case B: INSERT INTO <table> (...) SELECT <exprs> ... follows.
	selRe := regexp.MustCompile(`(?is)^\s*SELECT\b`)
	if sm := selRe.FindStringSubmatchIndex(rest); sm != nil {
		afterSel := rest[sm[1]:]
		afterSelUpper := restUpper[sm[1]:]
		endSel := len(afterSel)
		for _, stopKw := range []string{"FROM", "WHERE", "GROUP", "ORDER", "LIMIT", "ON", "RETURNING", "UNION"} {
			if kwPos := findTopLevelKeyword(afterSel, afterSelUpper, stopKw, 0); kwPos >= 0 && kwPos < endSel {
				endSel = kwPos
			}
		}
		selSegment := strings.TrimSpace(afterSel[:endSel])
		if selSegment != "" && !strings.HasPrefix(strings.ToUpper(selSegment), "DISTINCT") {
			selExprs := splitTopLevelCSV(selSegment)
			hasStar := false
			for _, se := range selExprs {
				ts := strings.TrimSpace(se)
				if ts == "*" || strings.HasSuffix(ts, ".*") {
					hasStar = true
					break
				}
			}
			if !hasStar {
				if len(selExprs) != len(insertCols) {
					*findings = append(*findings, finding{
						Pos: pos,
						Message: fmt.Sprintf(
							"INSERT INTO %s lists %d column(s) but SELECT supplies %d expression(s)",
							tableName, len(insertCols), len(selExprs),
						),
						Line: lineText(lines, pos.Line),
					})
					return
				}
				if !spreadArgs && len(callArgs) > 0 {
					checkColumnArgOrder("INSERT INTO "+tableName, insertCols, selExprs, callArgs, pos, lines, findings)
				}
			}
		}
	}
}

func checkOnConflictClause(
	rest string,
	restUpper string,
	tableName string,
	tbl *tableSchema,
	pos token.Position,
	lines []string,
	findings *[]finding,
) {
	onPos := findTopLevelKeyword(rest, restUpper, "ON", 0)
	if onPos < 0 {
		return
	}
	afterOn := strings.TrimSpace(rest[onPos+len("ON"):])
	afterOnUpper := strings.ToUpper(afterOn)
	if !strings.HasPrefix(afterOnUpper, "CONFLICT") {
		return
	}
	afterConflict := strings.TrimSpace(afterOn[len("CONFLICT"):])
	if strings.HasPrefix(afterConflict, "(") {
		closeConflict := findMatchingParen(afterConflict, 0)
		if closeConflict > 0 {
			conflictCols := splitTopLevelCSV(afterConflict[1:closeConflict])
			for _, rawCol := range conflictCols {
				trimmed := strings.TrimSpace(rawCol)
				// Only verify plain column identifiers (skip expression indexes like LOWER(email)).
				if regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`).MatchString(trimmed) {
					col := strings.ToLower(trimmed)
					if _, ok := tbl.ColumnByKey[col]; !ok {
						*findings = append(*findings, finding{
							Pos: pos,
							Message: fmt.Sprintf(
								"INSERT INTO %s ON CONFLICT references column %q which does not exist in migration schema (%s)",
								tableName, col, tbl.File,
							),
							Line: lineText(lines, pos.Line),
						})
					}
				}
			}
			afterConflict = strings.TrimSpace(afterConflict[closeConflict+1:])
		}
	}

	afterConflictUpper := strings.ToUpper(afterConflict)
	doSetRe := regexp.MustCompile(`(?is)\bDO\s+UPDATE\s+SET\s+`)
	dm := doSetRe.FindStringSubmatchIndex(afterConflict)
	if dm == nil {
		return
	}
	setBody := afterConflict[dm[1]:]
	setBodyUpper := afterConflictUpper[dm[1]:]
	endSet := len(setBody)
	for _, stopKw := range []string{"WHERE", "RETURNING"} {
		if kwPos := findTopLevelKeyword(setBody, setBodyUpper, stopKw, 0); kwPos >= 0 && kwPos < endSet {
			endSet = kwPos
		}
	}
	assignments := splitTopLevelCSV(setBody[:endSet])
	exclMap := make(map[string]string)
	exclRe := regexp.MustCompile(`(?is)^EXCLUDED\.([A-Za-z_][A-Za-z0-9_]*)$`)
	for _, assign := range assignments {
		eq := strings.IndexByte(assign, '=')
		if eq < 0 {
			continue
		}
		col := normalizeIdent(assign[:eq])
		if dot := strings.LastIndexByte(col, '.'); dot >= 0 {
			col = col[dot+1:]
		}
		rhs := strings.TrimSpace(assign[eq+1:])
		if col == "" {
			continue
		}
		if _, ok := tbl.ColumnByKey[col]; !ok {
			*findings = append(*findings, finding{
				Pos: pos,
				Message: fmt.Sprintf(
					"INSERT INTO %s ON CONFLICT DO UPDATE SET references column %q which does not exist in migration schema (%s)",
					tableName, col, tbl.File,
				),
				Line: lineText(lines, pos.Line),
			})
		}
		if em := exclRe.FindStringSubmatch(rhs); em != nil {
			exclCol := strings.ToLower(em[1])
			if _, ok := tbl.ColumnByKey[exclCol]; !ok {
				*findings = append(*findings, finding{
					Pos: pos,
					Message: fmt.Sprintf(
						"INSERT INTO %s ON CONFLICT DO UPDATE SET references EXCLUDED.%s which does not exist in migration schema (%s)",
						tableName, exclCol, tbl.File,
					),
					Line: lineText(lines, pos.Line),
				})
			} else if exclCol != col {
				exclMap[col] = exclCol
			}
		}
	}
	for colA, exclB := range exclMap {
		if exclA, swapped := exclMap[exclB]; swapped && exclA == colA {
			*findings = append(*findings, finding{
				Pos: pos,
				Message: fmt.Sprintf(
					"ON CONFLICT DO UPDATE SET in %s swaps EXCLUDED columns: %q = EXCLUDED.%s and %q = EXCLUDED.%s",
					tableName, colA, exclB, exclB, exclA,
				),
				Line: lineText(lines, pos.Line),
			})
			break
		}
	}
}

func checkUpdateStatement(
	sql string,
	pos token.Position,
	lines []string,
	callArgs []ast.Expr,
	spreadArgs bool,
	catalog *migrationCatalog,
	findings *[]finding,
) {
	upper := strings.ToUpper(sql)
	startIdx := 0
	if matchWordAt(upper, "WITH", 0) {
		if next := skipWithClause(sql, upper); next >= 0 {
			startIdx = next
		}
	}
	topSQL := sql[startIdx:]
	re := regexp.MustCompile(`(?is)^\s*UPDATE\s+([A-Za-z0-9_."` + "`" + `]+)(?:\s+AS\s+[A-Za-z0-9_]+|\s+[A-Za-z0-9_]+)?\s+SET\s+`)
	m := re.FindStringSubmatchIndex(topSQL)
	if m == nil {
		return
	}
	tableName := normalizeTableName(topSQL[m[2]:m[3]])
	if tableName == "set" {
		return
	}
	afterSet := topSQL[m[1]:]
	upperAfter := strings.ToUpper(afterSet)
	wherePos := findTopLevelKeyword(afterSet, upperAfter, "WHERE", 0)
	if wherePos < 0 {
		*findings = append(*findings, finding{
			Pos: pos,
			Message: fmt.Sprintf(
				"UPDATE %s statement has no WHERE clause; full-table updates are prohibited on runtime paths",
				tableName,
			),
			Line: lineText(lines, pos.Line),
		})
	}
	endSet := len(afterSet)
	for _, kw := range []string{"WHERE", "FROM", "RETURNING"} {
		if kwPos := findTopLevelKeyword(afterSet, upperAfter, kw, 0); kwPos >= 0 && kwPos < endSet {
			endSet = kwPos
		}
	}
	assignments := splitTopLevelCSV(afterSet[:endSet])
	var cols []string
	var exprs []string
	tbl := catalog.Tables[tableName]

	for _, assign := range assignments {
		eq := strings.IndexByte(assign, '=')
		if eq < 0 {
			continue
		}
		col := normalizeIdent(assign[:eq])
		if dot := strings.LastIndexByte(col, '.'); dot >= 0 {
			col = col[dot+1:]
		}
		rhs := strings.TrimSpace(assign[eq+1:])
		if col == "" {
			continue
		}
		cols = append(cols, col)
		exprs = append(exprs, rhs)
		if tbl != nil {
			if _, ok := tbl.ColumnByKey[col]; !ok {
				*findings = append(*findings, finding{
					Pos: pos,
					Message: fmt.Sprintf(
						"UPDATE %s SET references column %q which does not exist in migration schema (%s)",
						tableName, col, tbl.File,
					),
					Line: lineText(lines, pos.Line),
				})
			}
		}
	}

	if !spreadArgs && len(callArgs) > 0 && len(cols) > 1 {
		checkColumnArgOrder("UPDATE "+tableName, cols, exprs, callArgs, pos, lines, findings)
	}
}

func checkDeleteStatement(
	sql string,
	pos token.Position,
	lines []string,
	findings *[]finding,
) {
	upper := strings.ToUpper(sql)
	startIdx := 0
	if matchWordAt(upper, "WITH", 0) {
		if next := skipWithClause(sql, upper); next >= 0 {
			startIdx = next
		}
	}
	topSQL := sql[startIdx:]
	re := regexp.MustCompile(`(?is)^\s*DELETE\s+FROM\s+([A-Za-z0-9_."` + "`" + `]+)`)
	m := re.FindStringSubmatchIndex(topSQL)
	if m == nil {
		return
	}
	tableName := normalizeTableName(topSQL[m[2]:m[3]])
	afterTable := topSQL[m[1]:]
	upperAfter := strings.ToUpper(afterTable)
	if findTopLevelKeyword(afterTable, upperAfter, "WHERE", 0) < 0 {
		*findings = append(*findings, finding{
			Pos: pos,
			Message: fmt.Sprintf(
				"DELETE FROM %s statement has no WHERE clause; full-table deletes are prohibited on runtime paths",
				tableName,
			),
			Line: lineText(lines, pos.Line),
		})
	}
}

func checkColumnArgOrder(
	stmtLabel string,
	cols []string,
	sqlExprs []string,
	callArgs []ast.Expr,
	pos token.Position,
	lines []string,
	findings *[]finding,
) {
	type boundParam struct {
		ColNorm string
		ColRaw  string
		ArgNorm string
		ArgRaw  string
	}
	var bound []boundParam
	for i := 0; i < len(cols) && i < len(sqlExprs); i++ {
		pNum := extractSinglePlaceholder(sqlExprs[i])
		if pNum <= 0 || pNum > len(callArgs) {
			continue
		}
		argRaw := exprTailIdent(callArgs[pNum-1])
		if argRaw == "" {
			continue
		}
		bound = append(bound, boundParam{
			ColNorm: normalizeFieldToken(cols[i]),
			ColRaw:  cols[i],
			ArgNorm: normalizeFieldToken(argRaw),
			ArgRaw:  argRaw,
		})
	}
	for i := 0; i < len(bound); i++ {
		bi := bound[i]
		if bi.ColNorm == "" || bi.ArgNorm == "" || tokensMatch(bi.ColNorm, bi.ArgNorm) {
			continue
		}
		for j := i + 1; j < nBound(bound); j++ {
			bj := bound[j]
			if bj.ColNorm == "" || bj.ArgNorm == "" || bi.ColNorm == bj.ColNorm || tokensMatch(bj.ColNorm, bj.ArgNorm) {
				continue
			}
			if tokensMatch(bi.ColNorm, bj.ArgNorm) && tokensMatch(bj.ColNorm, bi.ArgNorm) {
				*findings = append(*findings, finding{
					Pos: pos,
					Message: fmt.Sprintf(
						"positional argument swap in %s: column %q receives %q while column %q receives %q",
						stmtLabel, bi.ColRaw, bi.ArgRaw, bj.ColRaw, bj.ArgRaw,
					),
					Line: lineText(lines, pos.Line),
				})
				return
			}
		}
	}
}

func nBound[T any](s []T) int {
	return len(s)
}

func extractSinglePlaceholder(expr string) int {
	matches := placeholderRe.FindAllStringSubmatch(expr, -1)
	if len(matches) != 1 {
		return 0
	}
	n, err := strconv.Atoi(matches[0][1])
	if err != nil {
		return 0
	}
	return n
}

func collectPackageRecordFields(parsed []*parsedGoFile, consts map[string]string, intConsts map[string]int64) map[string]struct{} {
	fields := make(map[string]struct{})
	for _, pf := range parsed {
		ast.Inspect(pf.File, func(n ast.Node) bool {
			comp, ok := n.(*ast.CompositeLit)
			if !ok {
				return true
			}
			typeName := callFuncName(comp.Type)
			if typeName != "RecordField" && typeName != "FieldValue" {
				return true
			}
			for _, elt := range comp.Elts {
				kv, ok := elt.(*ast.KeyValueExpr)
				if !ok {
					continue
				}
				if keyIdent, ok := kv.Key.(*ast.Ident); ok && keyIdent.Name == "Name" {
					if val, ok := evalStringExpr(kv.Value, consts, intConsts); ok && val != "" {
						fields[strings.ToLower(strings.TrimSpace(val))] = struct{}{}
					}
				}
			}
			return true
		})
	}
	return fields
}

func checkPackageHermesSpecs(
	fset *token.FileSet,
	parsed []*parsedGoFile,
	consts map[string]string,
	intConsts map[string]int64,
	recordFields map[string]struct{},
	catalog *migrationCatalog,
) []finding {
	var findings []finding
	for _, pf := range parsed {
		ast.Inspect(pf.File, func(n ast.Node) bool {
			comp, ok := n.(*ast.CompositeLit)
			if !ok || callFuncName(comp.Type) != "ProjectionSpec" {
				return true
			}
			pos := fset.Position(comp.Pos())
			if hasWaiver(pf.Lines, pos.Line) {
				return true
			}
			var collection string
			var indexed []string
			for _, elt := range comp.Elts {
				kv, ok := elt.(*ast.KeyValueExpr)
				if !ok {
					continue
				}
				keyIdent, ok := kv.Key.(*ast.Ident)
				if !ok {
					continue
				}
				switch keyIdent.Name {
				case "Collection":
					collection, _ = evalStringExpr(kv.Value, consts, intConsts)
				case "IndexedFields", "RangeIndexedFields":
					if sliceLit, ok := kv.Value.(*ast.CompositeLit); ok {
						for _, item := range sliceLit.Elts {
							if s, ok := evalStringExpr(item, consts, intConsts); ok && s != "" {
								indexed = append(indexed, strings.ToLower(strings.TrimSpace(s)))
							}
						}
					}
				}
			}
			if collection == "" || len(indexed) == 0 {
				return true
			}
			tbl := catalog.Tables[strings.ToLower(collection)]
			if tbl == nil && len(recordFields) == 0 {
				return true
			}
			for _, idxField := range indexed {
				inTable := tbl != nil && tbl.ColumnByKey[idxField] != nil
				_, inRecordFields := recordFields[idxField]
				if tbl != nil && !inTable && !inRecordFields {
					findings = append(findings, finding{
						Pos: pos,
						Message: fmt.Sprintf(
							"Hermes ProjectionSpec for collection %q indexes field %q, which is absent from migration table %q and package RecordField emissions",
							collection, idxField, tbl.Name,
						),
						Line: lineText(pf.Lines, pos.Line),
					})
				}
			}
			return true
		})
	}
	return findings
}

func checkPackageProjectionMaps(
	fset *token.FileSet,
	parsed []*parsedGoFile,
	consts map[string]string,
	intConsts map[string]int64,
	catalog *migrationCatalog,
) []finding {
	if len(catalog.Tables) == 0 {
		return nil
	}
	var findings []finding
	for _, pf := range parsed {
		for _, decl := range pf.File.Decls {
			gen, ok := decl.(*ast.GenDecl)
			if !ok || (gen.Tok != token.VAR && gen.Tok != token.CONST) {
				continue
			}
			for _, spec := range gen.Specs {
				vs, ok := spec.(*ast.ValueSpec)
				if !ok {
					continue
				}
				for i, name := range vs.Names {
					if i >= len(vs.Values) {
						continue
					}
					comp, ok := vs.Values[i].(*ast.CompositeLit)
					if !ok {
						continue
					}
					switch name.Name {
					case "projectionSources":
						for _, elt := range comp.Elts {
							kv, ok := elt.(*ast.KeyValueExpr)
							if !ok {
								continue
							}
							scope, sok := evalStringExpr(kv.Key, consts, intConsts)
							table, tok := evalStringExpr(kv.Value, consts, intConsts)
							if !sok || !tok || table == "" {
								continue
							}
							pos := fset.Position(kv.Pos())
							if hasWaiver(pf.Lines, pos.Line) {
								continue
							}
							tblName := normalizeTableName(table)
							if _, exists := catalog.Tables[tblName]; !exists {
								findings = append(findings, finding{
									Pos: pos,
									Message: fmt.Sprintf(
										"Hermes projectionSources maps scope %q to table %q, which does not exist in migration schemas",
										scope, tblName,
									),
									Line: lineText(pf.Lines, pos.Line),
								})
							}
						}
					case "projectedFields":
						for _, elt := range comp.Elts {
							kv, ok := elt.(*ast.KeyValueExpr)
							if !ok {
								continue
							}
							table, tok := evalStringExpr(kv.Key, consts, intConsts)
							if !tok || table == "" {
								continue
							}
							tblName := normalizeTableName(table)
							tbl, exists := catalog.Tables[tblName]
							pos := fset.Position(kv.Pos())
							if hasWaiver(pf.Lines, pos.Line) {
								continue
							}
							if !exists || tbl == nil {
								findings = append(findings, finding{
									Pos: pos,
									Message: fmt.Sprintf(
										"Hermes projectedFields declares table %q, which does not exist in migration schemas",
										tblName,
									),
									Line: lineText(pf.Lines, pos.Line),
								})
								continue
							}
							sliceLit, ok := kv.Value.(*ast.CompositeLit)
							if !ok {
								continue
							}
							for _, item := range sliceLit.Elts {
								colName, cok := evalStringExpr(item, consts, intConsts)
								if !cok || colName == "" {
									continue
								}
								itemPos := fset.Position(item.Pos())
								if hasWaiver(pf.Lines, itemPos.Line) {
									continue
								}
								col := strings.ToLower(strings.TrimSpace(colName))
								if _, colExists := tbl.ColumnByKey[col]; !colExists {
									findings = append(findings, finding{
										Pos: itemPos,
										Message: fmt.Sprintf(
											"Hermes projectedFields[%q] lists column %q, which does not exist on migration table %q (%s)",
											tblName, col, tbl.Name, tbl.File,
										),
										Line: lineText(pf.Lines, itemPos.Line),
									})
								}
							}
						}
					}
				}
			}
		}
	}
	return findings
}

func summarizeSQLCols(cols []projectedCol) []string {
	out := make([]string, 0, len(cols))
	for _, c := range cols {
		if c.Canonical != "" {
			out = append(out, c.Canonical)
		} else {
			out = append(out, c.Raw)
		}
	}
	return out
}

func summarizeScanTargets(targets []scanTarget) []string {
	out := make([]string, 0, len(targets))
	for _, t := range targets {
		if t.Raw != "" {
			out = append(out, "&"+t.Raw)
		} else {
			out = append(out, "?")
		}
	}
	return out
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
	return lines
}

func lineText(lines []string, n int) string {
	if n >= 1 && n <= len(lines) {
		return lines[n-1]
	}
	return ""
}

// hasWaiver accepts the waiver on the same line or on the comment line above it.
func hasWaiver(lines []string, n int) bool {
	if strings.Contains(lineText(lines, n), sqlShapeWaiver) {
		return true
	}
	prev := strings.TrimSpace(lineText(lines, n-1))
	return strings.HasPrefix(prev, "//") && strings.Contains(prev, sqlShapeWaiver)
}
