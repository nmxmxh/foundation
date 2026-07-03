package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"testing"
)

type scriptedRunner struct {
	outputs map[string]string
	fails   map[string]error
	calls   []string
}

func (r *scriptedRunner) Run(_ context.Context, name string, _ []string, _ string, _ []string, stdout io.Writer, _ io.Writer) error {
	r.calls = append(r.calls, name)
	if err, ok := r.fails[name]; ok {
		return err
	}
	fmt.Fprintln(stdout, r.outputs[name])
	return nil
}

func lookPathFor(available ...string) func(string) (string, error) {
	set := map[string]bool{}
	for _, name := range available {
		set[name] = true
	}
	return func(name string) (string, error) {
		if set[name] {
			return "/usr/bin/" + name, nil
		}
		return "", errors.New(name + " not found")
	}
}

func allToolNames() []string {
	names := make([]string, 0, len(doctorTools()))
	for _, tool := range doctorTools() {
		names = append(names, tool.name)
	}
	return names
}

func TestDoctorAllToolsPresent(t *testing.T) {
	out := &bytes.Buffer{}
	runner := &scriptedRunner{outputs: map[string]string{
		"git": "git version 2.39.0", "go": "go version go1.26.1 darwin/arm64",
		"node": "v22.1.0", "npm": "10.5.0",
		"docker": "Docker version 27.0.0", "cargo": "cargo 1.95.0",
	}}
	a := app{stdout: out, stderr: &bytes.Buffer{}, runner: runner, lookPath: lookPathFor(allToolNames()...)}
	if err := a.run(context.Background(), []string{"doctor"}); err != nil {
		t.Fatalf("doctor with full toolchain: %v", err)
	}
	report := out.String()
	if !strings.Contains(report, "environment ready") {
		t.Fatalf("report missing ready line:\n%s", report)
	}
	if !strings.Contains(report, "go version go1.26.1 darwin/arm64") {
		t.Fatalf("report missing probed version:\n%s", report)
	}
	if len(runner.calls) != len(doctorTools()) {
		t.Fatalf("probed %d tools, want %d", len(runner.calls), len(doctorTools()))
	}
}

func TestDoctorMissingRequiredFails(t *testing.T) {
	out := &bytes.Buffer{}
	a := app{stdout: out, stderr: &bytes.Buffer{}, runner: &scriptedRunner{outputs: map[string]string{}},
		lookPath: lookPathFor("git", "go", "npm", "docker", "cargo")} // node missing
	err := a.run(context.Background(), []string{"doctor"})
	if err == nil {
		t.Fatal("expected error when a required tool is missing")
	}
	if !strings.Contains(err.Error(), "required tool") {
		t.Fatalf("error = %v, want required-tool failure", err)
	}
	if !strings.Contains(out.String(), "not on PATH") {
		t.Fatalf("report missing not-on-PATH marker:\n%s", out.String())
	}
}

func TestDoctorMissingOptionalPasses(t *testing.T) {
	out := &bytes.Buffer{}
	a := app{stdout: out, stderr: &bytes.Buffer{}, runner: &scriptedRunner{outputs: map[string]string{}},
		lookPath: lookPathFor("git", "go", "node", "npm")} // docker + cargo missing
	if err := a.run(context.Background(), []string{"doctor"}); err != nil {
		t.Fatalf("optional tools must not fail doctor: %v", err)
	}
	if !strings.Contains(out.String(), "environment ready") {
		t.Fatalf("report missing ready line:\n%s", out.String())
	}
}

func TestDoctorJSONShape(t *testing.T) {
	out := &bytes.Buffer{}
	a := app{stdout: out, stderr: &bytes.Buffer{}, runner: &scriptedRunner{outputs: map[string]string{}},
		lookPath: lookPathFor("git", "go", "node", "npm")}
	if err := a.run(context.Background(), []string{"doctor", "--json"}); err != nil {
		t.Fatalf("doctor --json: %v", err)
	}
	var payload struct {
		OK    bool           `json:"ok"`
		Tools []doctorResult `json:"tools"`
	}
	if err := json.Unmarshal(out.Bytes(), &payload); err != nil {
		t.Fatalf("doctor --json output is not valid JSON: %v\n%s", err, out.String())
	}
	if !payload.OK {
		t.Fatal("ok = false with all required tools present")
	}
	if len(payload.Tools) != len(doctorTools()) {
		t.Fatalf("tools = %d, want %d", len(payload.Tools), len(doctorTools()))
	}
	for _, tool := range payload.Tools {
		if tool.Name == "docker" && tool.Found {
			t.Fatal("docker reported found despite missing from PATH")
		}
	}
}

func TestDoctorJSONMissingRequiredFails(t *testing.T) {
	out := &bytes.Buffer{}
	a := app{stdout: out, stderr: &bytes.Buffer{}, runner: &scriptedRunner{outputs: map[string]string{}},
		lookPath: lookPathFor("git")}
	err := a.run(context.Background(), []string{"doctor", "--json"})
	if err == nil {
		t.Fatal("expected error when required tools are missing")
	}
	var payload struct {
		OK bool `json:"ok"`
	}
	if jsonErr := json.Unmarshal(out.Bytes(), &payload); jsonErr != nil {
		t.Fatalf("JSON must still be emitted on failure: %v", jsonErr)
	}
	if payload.OK {
		t.Fatal("ok = true with required tools missing")
	}
}

func TestDoctorVersionProbeFailureStillCountsAsFound(t *testing.T) {
	out := &bytes.Buffer{}
	runner := &scriptedRunner{
		outputs: map[string]string{},
		fails:   map[string]error{"git": errors.New("exit status 1")},
	}
	a := app{stdout: out, stderr: &bytes.Buffer{}, runner: runner, lookPath: lookPathFor("git", "go", "node", "npm")}
	if err := a.run(context.Background(), []string{"doctor"}); err != nil {
		t.Fatalf("version probe failure must not fail doctor when binary exists: %v", err)
	}
	if !strings.Contains(out.String(), "version probe failed") {
		t.Fatalf("report missing probe-failure marker:\n%s", out.String())
	}
}

func TestFirstLine(t *testing.T) {
	cases := map[string]string{
		"git version 2.39.0\n":    "git version 2.39.0",
		"v22.1.0\nextra\nlines\n": "v22.1.0",
		"  padded  \n":            "padded",
		"":                        "",
	}
	for input, want := range cases {
		if got := firstLine(input); got != want {
			t.Fatalf("firstLine(%q) = %q, want %q", input, got, want)
		}
	}
}
