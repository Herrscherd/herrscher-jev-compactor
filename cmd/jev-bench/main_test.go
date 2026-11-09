package main

import (
	"bytes"
	"flag"
	"os"
	"path/filepath"
	"testing"
)

const (
	updateFlag     = "update"
	fixture        = "../../testdata/replay/session.json"
	strictSettings = "testdata/strict.settings"
	looseSettings  = "testdata/loose.settings"
	revertSettings = "testdata/revert.settings"
)

func TestMain(m *testing.M) {
	flag.Bool(updateFlag, false, "rewrite the golden files")
	os.Exit(m.Run())
}

func TestBenchTableGolden(t *testing.T) {
	var stdout, stderr bytes.Buffer
	args := []string{"-settings", strictSettings, "-settings", looseSettings, "-settings", revertSettings, fixture}
	if code := execute(args, &stdout, &stderr); code != exitSuccess {
		t.Fatalf("exit %d, stderr: %s", code.code(), stderr.String())
	}
	assertGolden(t, filepath.Join("testdata", "compare.golden"), stdout.Bytes())
}

func TestBenchJSONGolden(t *testing.T) {
	var stdout, stderr bytes.Buffer
	args := []string{"-json", "-settings", strictSettings, "-settings", looseSettings, "-settings", revertSettings, fixture}
	if code := execute(args, &stdout, &stderr); code != exitSuccess {
		t.Fatalf("exit %d, stderr: %s", code.code(), stderr.String())
	}
	assertGolden(t, filepath.Join("testdata", "compare.json.golden"), stdout.Bytes())
}

func TestBenchExitCodes(t *testing.T) {
	dir := t.TempDir()
	empty := filepath.Join(dir, "empty.json")
	badSettings := filepath.Join(dir, "bad.settings")
	unknownSettings := filepath.Join(dir, "unknown.settings")
	writeFile(t, empty, "{}")
	writeFile(t, badSettings, "keep_threshold 0.5\n")
	writeFile(t, unknownSettings, "no_such_setting=1\n")

	tests := []struct {
		name string
		args []string
		want exit
	}{
		{name: "no snapshot", args: []string{"-json"}, want: exitUsage},
		{name: "unknown flag", args: []string{"-nope", fixture}, want: exitUsage},
		{name: "missing file", args: []string{filepath.Join(dir, "absent.json")}, want: exitNoInput},
		{name: "not a snapshot", args: []string{empty}, want: exitDataError},
		{name: "bad settings line", args: []string{"-settings", badSettings, fixture}, want: exitConfig},
		{name: "unknown setting", args: []string{"-settings", unknownSettings, fixture}, want: exitConfig},
		{name: "help", args: []string{"-h"}, want: exitSuccess},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			if got := execute(test.args, &stdout, &stderr); got != test.want {
				t.Fatalf("exit %d, want %d, stderr: %s", got.code(), test.want.code(), stderr.String())
			}
		})
	}
}

func assertGolden(t *testing.T, path string, got []byte) {
	t.Helper()
	if flag.Lookup(updateFlag).Value.String() == "true" {
		if err := os.WriteFile(path, got, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, want) {
		t.Fatalf("%s differs from golden:\n%s", path, got)
	}
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}
