package main

import (
	"bytes"
	"context"
	"net/http"
	"os"
	"path/filepath"
	"testing"
)

func foundationFixture(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "templates"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "templates", "scaffold.manifest.tsv"), []byte(""), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "init.sh"), []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	return root
}

func TestRefreshWrapsUpdateScriptWithoutOverrides(t *testing.T) {
	root := foundationFixture(t)
	runner := &recordingRunner{}
	a := app{stdout: &bytes.Buffer{}, stderr: &bytes.Buffer{}, runner: runner, client: http.DefaultClient}
	err := a.run(context.Background(), []string{
		"refresh",
		"--project-dir=/tmp/trader_os_v1",
		"--foundation-dir=" + root,
		"--skip-license",
		"--dry-run",
		"--acknowledge-seed-drift",
	})
	if err != nil {
		t.Fatal(err)
	}
	if runner.run.name != filepath.Join(root, "scripts", "update-project.sh") {
		t.Fatalf("refresh script = %q", runner.run.name)
	}
	want := []string{"/tmp/trader_os_v1", "--dry-run", "--acknowledge-seed-drift"}
	if !equalStrings(runner.run.args, want) {
		t.Fatalf("args = %#v, want %#v", runner.run.args, want)
	}
}

func TestRefreshRejectsOverrideFlags(t *testing.T) {
	root := foundationFixture(t)
	for _, override := range []string{"--force", "--profile=performance", "--with-wasm"} {
		a := app{stdout: &bytes.Buffer{}, stderr: &bytes.Buffer{}, runner: &recordingRunner{}, client: http.DefaultClient}
		err := a.run(context.Background(), []string{
			"refresh",
			"--project-dir=/tmp/trader_os_v1",
			"--foundation-dir=" + root,
			"--skip-license",
			override,
		})
		if err == nil {
			t.Fatalf("refresh accepted override flag %q; overrides belong to update", override)
		}
	}
}

func TestRefreshRequiresProjectDir(t *testing.T) {
	root := foundationFixture(t)
	a := app{stdout: &bytes.Buffer{}, stderr: &bytes.Buffer{}, runner: &recordingRunner{}, client: http.DefaultClient}
	err := a.run(context.Background(), []string{"refresh", "--foundation-dir=" + root, "--skip-license"})
	if err == nil {
		t.Fatal("refresh without a project dir must fail")
	}
}

func TestUpdatePassesAcknowledgeSeedDrift(t *testing.T) {
	root := foundationFixture(t)
	runner := &recordingRunner{}
	a := app{stdout: &bytes.Buffer{}, stderr: &bytes.Buffer{}, runner: runner, client: http.DefaultClient}
	err := a.run(context.Background(), []string{
		"update",
		"--project-dir=/tmp/trader_os_v1",
		"--foundation-dir=" + root,
		"--skip-license",
		"--acknowledge-seed-drift",
	})
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"/tmp/trader_os_v1", "--acknowledge-seed-drift"}
	if !equalStrings(runner.run.args, want) {
		t.Fatalf("args = %#v, want %#v", runner.run.args, want)
	}
}

func TestFleetUpdateForwardsSafetyAndReportingFlags(t *testing.T) {
	root := foundationFixture(t)
	runner := &recordingRunner{}
	a := app{stdout: &bytes.Buffer{}, stderr: &bytes.Buffer{}, runner: runner, client: http.DefaultClient}
	err := a.run(context.Background(), []string{
		"fleet-update", "--foundation-dir=" + root, "--dry-run", "--force",
		"--validate", "--verify-idempotence", "--report-dir=/tmp/fleet-report",
	})
	if err != nil {
		t.Fatal(err)
	}
	if runner.run.name != filepath.Join(root, "scripts", "update-all.sh") {
		t.Fatalf("fleet script = %q", runner.run.name)
	}
	want := []string{"--force", "--dry-run", "--report-dir", "/tmp/fleet-report", "--validate", "--verify-idempotence"}
	if !equalStrings(runner.run.args, want) {
		t.Fatalf("args = %#v, want %#v", runner.run.args, want)
	}
}

func TestAgentGraphAndFeaturePlanUseSharedChangeTool(t *testing.T) {
	root, err := discoverFoundationDir()
	if err != nil {
		t.Fatal(err)
	}
	for _, tt := range []struct {
		args []string
		want []string
	}{
		{args: []string{"agent", "graph", "--capability=live_projection"}, want: []string{filepath.Join(root, "tooling/scripts/agent_change.mjs"), "graph", "--capability", "live_projection"}},
		{args: []string{"add", "feature", "review-task", "--commands=create,complete", "--projection=list", "--offline", "--realtime"}, want: []string{filepath.Join(root, "tooling/scripts/agent_change.mjs"), "plan", "--feature", "review-task"}},
	} {
		runner := &recordingRunner{}
		a := app{stdout: &bytes.Buffer{}, stderr: &bytes.Buffer{}, runner: runner, client: http.DefaultClient}
		if err := a.run(context.Background(), tt.args); err != nil {
			t.Fatal(err)
		}
		if runner.run.name != "node" || len(runner.run.args) < len(tt.want) || !equalStrings(runner.run.args[:len(tt.want)], tt.want) {
			t.Fatalf("run = %q %#v, want node prefix %#v", runner.run.name, runner.run.args, tt.want)
		}
	}
}
