package main

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

const (
	defaultLicenseURL = "https://api.ovasabi.studio/v1/license/verify"
	defaultTimeout    = 10 * time.Second
)

type commandRunner interface {
	Run(ctx context.Context, name string, args []string, dir string, env []string, stdout io.Writer, stderr io.Writer) error
}

type osRunner struct{}

func (osRunner) Run(ctx context.Context, name string, args []string, dir string, env []string, stdout io.Writer, stderr io.Writer) error {
	// #nosec G204 -- the CLI's purpose is launching the Foundation scaffold
	// scripts; name/args come from internal call sites, not user input.
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), env...)
	cmd.Stdout = stdout
	cmd.Stderr = stderr
	return cmd.Run()
}

type app struct {
	stdout   io.Writer
	stderr   io.Writer
	runner   commandRunner
	client   *http.Client
	lookPath func(string) (string, error) // nil means exec.LookPath
}

func main() {
	a := app{
		stdout: os.Stdout,
		stderr: os.Stderr,
		runner: osRunner{},
		client: http.DefaultClient,
	}
	if err := a.run(context.Background(), os.Args[1:]); err != nil {
		fmt.Fprintln(a.stderr, "ovasabi:", err)
		os.Exit(1)
	}
}

func (a app) run(ctx context.Context, args []string) error {
	if len(args) == 0 {
		a.usage()
		return errors.New("command is required")
	}
	switch args[0] {
	case "init":
		return a.runInit(ctx, args[1:])
	case "update":
		return a.runUpdate(ctx, args[1:])
	case "refresh":
		return a.runRefresh(ctx, args[1:])
	case "license":
		return a.runLicense(ctx, args[1:])
	case "doctor":
		return a.runDoctor(ctx, args[1:])
	case "help", "--help", "-h":
		a.usage()
		return nil
	default:
		a.usage()
		return fmt.Errorf("unknown command %q", args[0])
	}
}

func (a app) usage() {
	fmt.Fprintln(a.stdout, `Ovasabi CLI

Usage:
  ovasabi init --profile=performance --name=trader_os [options]
  ovasabi update --project-dir=../trader_os_v1 [options]
  ovasabi refresh --project-dir=../trader_os_v1 [--dry-run] [--acknowledge-seed-drift]
  ovasabi license verify [options]
  ovasabi doctor [--json]

refresh reconciles a project against its declared .foundation state with no
overrides; update is the flag-driven variant for changing profile or features.

The CLI wraps the existing Foundation scaffold scripts while adding the
distribution and licensing boundary used by package-registry installs.`)
}

type licenseOptions struct {
	offline     bool
	file        string
	key         string
	publicKey   string
	verifyURL   string
	timeout     time.Duration
	skip        bool
	profile     string
	projectName string
}

func (a app) runInit(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("init", flag.ContinueOnError)
	fs.SetOutput(a.stderr)
	name := fs.String("name", "", "project name")
	profile := fs.String("profile", "core", "foundation profile: lite, core, performance, regulated")
	projectDir := fs.String("project-dir", "", "project output directory")
	goModule := fs.String("go-module", "", "Go module path")
	foundationDir := fs.String("foundation-dir", "", "Foundation core checkout")
	skipDeps := fs.Bool("skip-deps", false, "skip dependency checks")
	dryRun := fs.Bool("dry-run", false, "preview without writing files")
	lic := bindLicenseFlags(fs)
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *name == "" {
		return errors.New("--name is required")
	}
	if err := validateProjectName(*name); err != nil {
		return err
	}
	if *foundationDir == "" {
		dir, err := discoverFoundationDir()
		if err != nil {
			return err
		}
		*foundationDir = dir
	}
	absFoundationDir, err := resolveCallerPath(*foundationDir)
	if err != nil {
		return err
	}
	*foundationDir = absFoundationDir
	if *projectDir != "" {
		absProjectDir, err := resolveCallerPath(*projectDir)
		if err != nil {
			return err
		}
		*projectDir = absProjectDir
	}
	scriptProfile, err := scriptProfile(*profile)
	if err != nil {
		return err
	}
	lic.profile = *profile
	lic.projectName = *name
	if err := a.verifyLicense(ctx, lic); err != nil {
		return err
	}
	script := filepath.Join(*foundationDir, "init.sh")
	scriptArgs := []string{*name, scriptProfile}
	if *projectDir != "" {
		scriptArgs = append(scriptArgs, "--project-dir", *projectDir)
	}
	if *goModule != "" {
		scriptArgs = append(scriptArgs, "--go-module", *goModule)
	}
	if *skipDeps {
		scriptArgs = append(scriptArgs, "--skip-deps")
	}
	if *dryRun {
		scriptArgs = append(scriptArgs, "--dry-run")
	}
	return a.runner.Run(ctx, script, scriptArgs, *foundationDir, nil, a.stdout, a.stderr)
}

func (a app) runUpdate(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("update", flag.ContinueOnError)
	fs.SetOutput(a.stderr)
	projectDir := fs.String("project-dir", "", "project directory")
	foundationDir := fs.String("foundation-dir", "", "Foundation core checkout")
	force := fs.Bool("force", false, "overwrite force-managed files")
	dryRun := fs.Bool("dry-run", false, "preview without writing files")
	docsOnly := fs.Bool("docs-only", false, "update docs only")
	toolingOnly := fs.Bool("tooling-only", false, "update tooling only")
	foundationOnly := fs.Bool("foundation-only", false, "update vendored foundation modules only")
	acknowledgeSeedDrift := fs.Bool("acknowledge-seed-drift", false, "re-baseline the seed ledger to current templates")
	profile := fs.String("profile", "", "override profile")
	lic := bindLicenseFlags(fs)
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *projectDir == "" && fs.NArg() > 0 {
		*projectDir = fs.Arg(0)
	}
	if *projectDir == "" {
		return errors.New("--project-dir or project path is required")
	}
	if err := resolveDirs(foundationDir, projectDir); err != nil {
		return err
	}
	lic.profile = *profile
	if err := a.verifyLicense(ctx, lic); err != nil {
		return err
	}
	script := filepath.Join(*foundationDir, "scripts", "update-project.sh")
	scriptArgs := []string{*projectDir}
	if *force {
		scriptArgs = append(scriptArgs, "--force")
	}
	if *dryRun {
		scriptArgs = append(scriptArgs, "--dry-run")
	}
	if *docsOnly {
		scriptArgs = append(scriptArgs, "--docs-only")
	}
	if *toolingOnly {
		scriptArgs = append(scriptArgs, "--tooling-only")
	}
	if *foundationOnly {
		scriptArgs = append(scriptArgs, "--foundation-only")
	}
	if *acknowledgeSeedDrift {
		scriptArgs = append(scriptArgs, "--acknowledge-seed-drift")
	}
	if *profile != "" {
		mapped, err := scriptProfile(*profile)
		if err != nil {
			return err
		}
		scriptArgs = append(scriptArgs, "--profile", mapped)
	}
	return a.runner.Run(ctx, script, scriptArgs, *foundationDir, nil, a.stdout, a.stderr)
}

// runRefresh reconciles a project purely from its declared .foundation state:
// no profile, force, or feature overrides are accepted.
func (a app) runRefresh(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("refresh", flag.ContinueOnError)
	fs.SetOutput(a.stderr)
	projectDir := fs.String("project-dir", "", "project directory")
	foundationDir := fs.String("foundation-dir", "", "Foundation core checkout")
	dryRun := fs.Bool("dry-run", false, "preview without writing files")
	acknowledgeSeedDrift := fs.Bool("acknowledge-seed-drift", false, "re-baseline the seed ledger to current templates")
	lic := bindLicenseFlags(fs)
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *projectDir == "" && fs.NArg() > 0 {
		*projectDir = fs.Arg(0)
	}
	if *projectDir == "" {
		return errors.New("--project-dir or project path is required")
	}
	if err := resolveDirs(foundationDir, projectDir); err != nil {
		return err
	}
	if err := a.verifyLicense(ctx, lic); err != nil {
		return err
	}
	script := filepath.Join(*foundationDir, "scripts", "update-project.sh")
	scriptArgs := []string{*projectDir}
	if *dryRun {
		scriptArgs = append(scriptArgs, "--dry-run")
	}
	if *acknowledgeSeedDrift {
		scriptArgs = append(scriptArgs, "--acknowledge-seed-drift")
	}
	return a.runner.Run(ctx, script, scriptArgs, *foundationDir, nil, a.stdout, a.stderr)
}

func resolveDirs(foundationDir, projectDir *string) error {
	if *foundationDir == "" {
		dir, err := discoverFoundationDir()
		if err != nil {
			return err
		}
		*foundationDir = dir
	}
	absFoundationDir, err := resolveCallerPath(*foundationDir)
	if err != nil {
		return err
	}
	*foundationDir = absFoundationDir
	if *projectDir != "" {
		absProjectDir, err := resolveCallerPath(*projectDir)
		if err != nil {
			return err
		}
		*projectDir = absProjectDir
	}
	return nil
}

func (a app) runLicense(ctx context.Context, args []string) error {
	if len(args) == 0 || args[0] != "verify" {
		return errors.New("usage: ovasabi license verify [options]")
	}
	fs := flag.NewFlagSet("license verify", flag.ContinueOnError)
	fs.SetOutput(a.stderr)
	lic := bindLicenseFlags(fs)
	if err := fs.Parse(args[1:]); err != nil {
		return err
	}
	lic.skip = false
	if err := a.verifyLicense(ctx, lic); err != nil {
		return err
	}
	fmt.Fprintln(a.stdout, "license verification passed")
	return nil
}

func bindLicenseFlags(fs *flag.FlagSet) licenseOptions {
	lic := licenseOptions{timeout: defaultTimeout, verifyURL: defaultLicenseURL}
	fs.BoolVar(&lic.skip, "skip-license", false, "skip license verification")
	fs.BoolVar(&lic.offline, "offline-license", false, "verify signed license file without network")
	fs.StringVar(&lic.file, "license-file", "", "path to ovasabi.lic")
	fs.StringVar(&lic.key, "license-key", "", "commercial license key")
	fs.StringVar(&lic.publicKey, "license-public-key", "", "PEM public key for offline license verification")
	fs.StringVar(&lic.verifyURL, "license-url", defaultLicenseURL, "online license verification URL")
	fs.DurationVar(&lic.timeout, "license-timeout", defaultTimeout, "license verification timeout")
	return lic
}

func (a app) verifyLicense(ctx context.Context, opts licenseOptions) error {
	if opts.skip {
		return nil
	}
	if opts.offline || opts.file != "" {
		return verifyOfflineLicense(opts)
	}
	key := opts.key
	if key == "" {
		key = os.Getenv("OVASABI_LICENSE_KEY")
	}
	if key == "" {
		key = readConfigLicenseKey()
	}
	if key == "" {
		return nil
	}
	return a.verifyOnlineLicense(ctx, opts, key)
}

func (a app) verifyOnlineLicense(ctx context.Context, opts licenseOptions, key string) error {
	timeout := opts.timeout
	if timeout <= 0 {
		timeout = defaultTimeout
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, opts.verifyURL, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+key)
	req.Header.Set("X-Ovasabi-Correlation-Id", correlationID())
	if opts.profile != "" {
		req.Header.Set("X-Ovasabi-Profile", opts.profile)
	}
	resp, err := a.client.Do(req)
	if err != nil {
		return fmt.Errorf("online license verification failed: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("online license verification failed: status %d", resp.StatusCode)
	}
	var body struct {
		Active bool `json:"active"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&body); err != nil && err != io.EOF {
		return fmt.Errorf("decode license response: %w", err)
	}
	if !body.Active {
		return errors.New("license is not active")
	}
	return nil
}

type licenseClaims struct {
	Issuer    string   `json:"iss"`
	Audience  string   `json:"aud"`
	Subject   string   `json:"sub"`
	OrgID     string   `json:"org_id"`
	Seats     int      `json:"seats"`
	Profiles  []string `json:"profiles"`
	Channels  []string `json:"channels"`
	Expires   int64    `json:"exp"`
	NotBefore int64    `json:"nbf"`
}

func verifyOfflineLicense(opts licenseOptions) error {
	if opts.file == "" {
		return errors.New("--license-file is required for offline license verification")
	}
	tokenBytes, err := os.ReadFile(opts.file)
	if err != nil {
		return err
	}
	token := strings.TrimSpace(string(tokenBytes))
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		return errors.New("offline license must be a compact JWT")
	}
	headerJSON, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil {
		return fmt.Errorf("decode license header: %w", err)
	}
	var header struct {
		Alg string `json:"alg"`
		Typ string `json:"typ"`
	}
	if err := json.Unmarshal(headerJSON, &header); err != nil {
		return fmt.Errorf("parse license header: %w", err)
	}
	if header.Alg != "EdDSA" {
		return fmt.Errorf("unsupported offline license algorithm %q", header.Alg)
	}
	pub, err := parsePublicKey(opts.publicKey)
	if err != nil {
		return err
	}
	sig, err := base64.RawURLEncoding.DecodeString(parts[2])
	if err != nil {
		return fmt.Errorf("decode license signature: %w", err)
	}
	signed := []byte(parts[0] + "." + parts[1])
	if !ed25519.Verify(pub, signed, sig) {
		return errors.New("offline license signature verification failed")
	}
	claimsJSON, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return fmt.Errorf("decode license claims: %w", err)
	}
	var claims licenseClaims
	if err := json.Unmarshal(claimsJSON, &claims); err != nil {
		return fmt.Errorf("parse license claims: %w", err)
	}
	now := time.Now().Unix()
	if claims.Expires > 0 && now >= claims.Expires {
		return errors.New("offline license expired")
	}
	if claims.NotBefore > 0 && now < claims.NotBefore {
		return errors.New("offline license is not valid yet")
	}
	if opts.profile != "" && len(claims.Profiles) > 0 && !contains(claims.Profiles, opts.profile) {
		return fmt.Errorf("offline license does not enable profile %q", opts.profile)
	}
	if claims.Seats < 0 {
		return errors.New("offline license has invalid seat count")
	}
	return nil
}

func parsePublicKey(value string) (ed25519.PublicKey, error) {
	if value == "" {
		value = os.Getenv("OVASABI_LICENSE_PUBLIC_KEY")
	}
	if value == "" {
		return nil, errors.New("offline license public key is required")
	}
	block, _ := pem.Decode([]byte(value))
	if block == nil {
		return nil, errors.New("offline license public key must be PEM encoded")
	}
	parsed, err := x509.ParsePKIXPublicKey(block.Bytes)
	if err != nil {
		return nil, fmt.Errorf("parse offline license public key: %w", err)
	}
	pub, ok := parsed.(ed25519.PublicKey)
	if !ok {
		return nil, errors.New("offline license public key must be Ed25519")
	}
	return pub, nil
}

func readConfigLicenseKey() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	data, err := os.ReadFile(filepath.Join(home, ".ovasabi", "config.json")) // #nosec G304 -- fixed path under the user's home directory.
	if err != nil {
		return ""
	}
	var config struct {
		LicenseKey string `json:"licenseKey"`
	}
	if err := json.Unmarshal(data, &config); err != nil {
		return ""
	}
	return config.LicenseKey
}

func discoverFoundationDir() (string, error) {
	wd, err := os.Getwd()
	if err != nil {
		return "", err
	}
	for {
		if fileExists(filepath.Join(wd, "templates", "scaffold.manifest.tsv")) && fileExists(filepath.Join(wd, "init.sh")) {
			return wd, nil
		}
		parent := filepath.Dir(wd)
		if parent == wd {
			return "", errors.New("could not locate Foundation root; pass --foundation-dir")
		}
		wd = parent
	}
}

func resolveCallerPath(pathValue string) (string, error) {
	if filepath.IsAbs(pathValue) {
		return filepath.Clean(pathValue), nil
	}
	base := os.Getenv("OVASABI_CALLER_CWD")
	if base == "" {
		var err error
		base, err = os.Getwd()
		if err != nil {
			return "", err
		}
	}
	return filepath.Abs(filepath.Join(base, pathValue))
}

func validateProjectName(name string) error {
	if name == "" {
		return errors.New("project name is required")
	}
	for i, r := range name {
		valid := r == '_' || r == '-' || (r >= '0' && r <= '9') || (r >= 'A' && r <= 'Z') || (r >= 'a' && r <= 'z')
		if !valid {
			return fmt.Errorf("invalid project name %q", name)
		}
		if i == 0 && (r == '_' || r == '-') {
			return fmt.Errorf("invalid project name %q", name)
		}
	}
	return nil
}

func scriptProfile(profile string) (string, error) {
	switch profile {
	case "", "core", "performance", "regulated":
		return "full", nil
	case "lite":
		return "minimal", nil
	case "full", "backend", "frontend", "minimal":
		return profile, nil
	default:
		return "", fmt.Errorf("unknown profile %q", profile)
	}
}

func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

func contains(values []string, needle string) bool {
	for _, value := range values {
		if value == needle {
			return true
		}
	}
	return false
}

func correlationID() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return fmt.Sprintf("ovasabi-%d", time.Now().UnixNano())
	}
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])
}
