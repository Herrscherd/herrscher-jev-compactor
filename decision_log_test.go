package jev

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func readDecisionLog(t *testing.T, path string) []map[string]any {
	t.Helper()
	content, err := os.ReadFile(filepath.Clean(path))
	if err != nil {
		t.Fatalf("read decision log: %v", err)
	}
	var entries []map[string]any
	for _, line := range strings.Split(strings.TrimSpace(string(content)), "\n") {
		var entry map[string]any
		if err := json.Unmarshal([]byte(line), &entry); err != nil {
			t.Fatalf("decision log line %q is not JSON: %v", line, err)
		}
		entries = append(entries, entry)
	}
	return entries
}

func TestDecisionLogWritesOneJSONLinePerDecision(t *testing.T) {
	path := filepath.Join(t.TempDir(), "decisions.jsonl")
	stage := newTestCompactor(t, &fakeJev{answers: sessionAnswers()}, map[string]string{settingDecisionLog: path, settingRules: "Grep:prune"})
	if _, _, err := compactSession(t, stage); err != nil {
		t.Fatalf("Compact: %v", err)
	}
	entries := readDecisionLog(t, path)
	if len(entries) == 0 {
		t.Fatal("decision log is empty, want one line per decision")
	}
	for _, entry := range entries {
		for _, key := range []string{"time", "msg", "ref", "tool", "verdict", "reason", "result_size"} {
			if _, found := entry[key]; !found {
				t.Fatalf("decision %v has no %q field", entry, key)
			}
		}
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat decision log: %v", err)
	}
	if info.Mode().Perm() != decisionLogMode {
		t.Fatalf("decision log mode = %v, want %v", info.Mode().Perm(), os.FileMode(decisionLogMode))
	}
}

func TestDecisionLogAppendsAcrossCompactions(t *testing.T) {
	path := filepath.Join(t.TempDir(), "decisions.jsonl")
	stage := newTestCompactor(t, &fakeJev{answers: sessionAnswers()}, map[string]string{settingDecisionLog: path})
	if _, _, err := compactSession(t, stage); err != nil {
		t.Fatalf("first Compact: %v", err)
	}
	first := len(readDecisionLog(t, path))
	if _, _, err := compactSession(t, stage); err != nil {
		t.Fatalf("second Compact: %v", err)
	}
	if got := len(readDecisionLog(t, path)); got != 2*first {
		t.Fatalf("lines after two compactions = %d, want %d appended to the first run's %d", got, 2*first, first)
	}
}

func TestDecisionLogOpenFailureDoesNotFailTheCompaction(t *testing.T) {
	stage := newTestCompactor(t, &fakeJev{answers: sessionAnswers()}, map[string]string{settingDecisionLog: t.TempDir()})
	result, _, err := compactSession(t, stage)
	if err != nil {
		t.Fatalf("Compact with an unopenable decision log: %v", err)
	}
	if !result.Done {
		t.Fatalf("result = %+v, want the compaction to go on without the log", result)
	}
}

func TestDecisionLogWriteFailureDoesNotFailTheCompaction(t *testing.T) {
	if _, err := os.Stat("/dev/full"); err != nil {
		t.Skip("no /dev/full to make every write fail")
	}
	stage := newTestCompactor(t, &fakeJev{answers: sessionAnswers()}, map[string]string{settingDecisionLog: "/dev/full"})
	result, _, err := compactSession(t, stage)
	if err != nil {
		t.Fatalf("Compact with failing writes to the decision log: %v", err)
	}
	if !result.Done {
		t.Fatalf("result = %+v, want the compaction to go on despite the failed writes", result)
	}
}
