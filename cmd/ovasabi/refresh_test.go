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
