package main

import (
	"bytes"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os/exec"
	"strings"
	"time"
)

const doctorProbeTimeout = 5 * time.Second

type doctorTool struct {
	name        string
	versionArgs []string
	required    bool
	purpose     string
}

// doctorTools mirrors check_dependencies in scripts/init-project.sh (full
// profile): git/go/node/npm are required, docker and cargo are lane-optional.
func doctorTools() []doctorTool {
	return []doctorTool{
		{name: "git", versionArgs: []string{"--version"}, required: true, purpose: "version control and scaffold sync"},
		{name: "go", versionArgs: []string{"version"}, required: true, purpose: "backend toolchain (Go 1.26+)"},
		{name: "node", versionArgs: []string{"--version"}, required: true, purpose: "frontend build and scaffold generators"},
		{name: "npm", versionArgs: []string{"--version"}, required: true, purpose: "frontend dependency management"},
		{name: "docker", versionArgs: []string{"--version"}, required: false, purpose: "local dev stack (make docker-up)"},
		{name: "cargo", versionArgs: []string{"--version"}, required: false, purpose: "Rust/WASM performance lanes"},
	}
}

type doctorResult struct {
	Name     string `json:"name"`
	Required bool   `json:"required"`
	Found    bool   `json:"found"`
	Version  string `json:"version,omitempty"`
	Purpose  string `json:"purpose"`
}

func (a app) runDoctor(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("doctor", flag.ContinueOnError)
	fs.SetOutput(a.stderr)
	jsonOut := fs.Bool("json", false, "emit machine-readable JSON")
	if err := fs.Parse(args); err != nil {
		return err
	}
	results := a.probeTools(ctx, doctorTools())
	if *jsonOut {
		return a.writeDoctorJSON(results)
	}
	return a.writeDoctorReport(results)
}

func (a app) probeTools(ctx context.Context, tools []doctorTool) []doctorResult {
	results := make([]doctorResult, 0, len(tools))
	for _, tool := range tools {
		results = append(results, a.probeTool(ctx, tool))
	}
	return results
}

func (a app) probeTool(ctx context.Context, tool doctorTool) doctorResult {
	result := doctorResult{Name: tool.name, Required: tool.required, Purpose: tool.purpose}
	if _, err := a.look(tool.name); err != nil {
		return result
	}
	result.Found = true
	probeCtx, cancel := context.WithTimeout(ctx, doctorProbeTimeout)
	defer cancel()
	var out bytes.Buffer
	if err := a.runner.Run(probeCtx, tool.name, tool.versionArgs, "", nil, &out, io.Discard); err != nil {
		result.Version = "version probe failed"
		return result
	}
	result.Version = firstLine(out.String())
	return result
}

func (a app) look(name string) (string, error) {
	if a.lookPath != nil {
		return a.lookPath(name)
	}
	return exec.LookPath(name)
}

func (a app) writeDoctorReport(results []doctorResult) error {
	missing := 0
	for _, r := range results {
		status := "ok"
		detail := r.Version
		if !r.Found {
			status = "missing"
			detail = "not on PATH"
			if r.Required {
				missing++
			}
		}
		requirement := "required"
		if !r.Required {
			requirement = "optional"
		}
		fmt.Fprintf(a.stdout, "[%-7s] %-8s %-8s %s — %s\n", status, r.Name, requirement, detail, r.Purpose)
	}
	if missing > 0 {
		return fmt.Errorf("%d required tool(s) missing", missing)
	}
	fmt.Fprintln(a.stdout, "environment ready")
	return nil
}

func (a app) writeDoctorJSON(results []doctorResult) error {
	missing := 0
	for _, r := range results {
		if r.Required && !r.Found {
			missing++
		}
	}
	payload := struct {
		OK    bool           `json:"ok"`
		Tools []doctorResult `json:"tools"`
	}{OK: missing == 0, Tools: results}
	enc := json.NewEncoder(a.stdout)
	enc.SetIndent("", "  ")
	if err := enc.Encode(payload); err != nil {
		return err
	}
	if missing > 0 {
		return fmt.Errorf("%d required tool(s) missing", missing)
	}
	return nil
}

func firstLine(s string) string {
	s = strings.TrimSpace(s)
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		s = s[:i]
	}
	return strings.TrimSpace(s)
}
