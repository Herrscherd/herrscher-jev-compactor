package jev

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
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

func TestDecisionLogRotatesWhenItGrowsPastTheLimit(t *testing.T) {
	path := filepath.Join(t.TempDir(), "decisions.jsonl")
	old := strings.Repeat("x", 100) + "\n"
	if err := os.WriteFile(path, []byte(old), decisionLogMode); err != nil {
		t.Fatalf("seed decision log: %v", err)
	}
	openDecisionLogWithLimit(path, 50)
	backup, err := os.ReadFile(path + decisionLogBackupSuffix)
	if err != nil || string(backup) != old {
		t.Fatalf("backup = %q, %v, want the previous log moved aside", backup, err)
	}
	info, err := os.Stat(path)
	if err != nil || info.Size() != 0 {
		t.Fatalf("new log = %v, %v, want an empty file at the path", info, err)
	}
}

func TestDecisionLogKeepsASmallLogInPlace(t *testing.T) {
	path := filepath.Join(t.TempDir(), "decisions.jsonl")
	if err := os.WriteFile(path, []byte("kept\n"), decisionLogMode); err != nil {
		t.Fatalf("seed decision log: %v", err)
	}
	openDecisionLogWithLimit(path, 1<<20)
	if _, err := os.Stat(path + decisionLogBackupSuffix); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("backup stat = %v, want no rotation below the limit", err)
	}
}

func TestDecisionLogKeepsEveryLineWholeWhenWritersRotateConcurrently(t *testing.T) {
	path := filepath.Join(t.TempDir(), "decisions.jsonl")
	journal := openDecisionLogWithLimit(path, 300)
	var writers sync.WaitGroup
	for worker := range 4 {
		writers.Go(func() {
			for n := range 50 {
				journal.write([]any{"worker", worker, "n", n})
			}
		})
	}
	writers.Wait()
	for _, name := range []string{path, path + decisionLogBackupSuffix} {
		content, err := os.ReadFile(filepath.Clean(name))
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		for line := range strings.SplitSeq(strings.TrimSpace(string(content)), "\n") {
			var entry map[string]any
			if err := json.Unmarshal([]byte(line), &entry); err != nil {
				t.Fatalf("%s holds a torn line %q: %v", name, line, err)
			}
		}
	}
}

func TestDecisionLogKeepsWritingWhenRotationFailsBelowTwiceTheLimit(t *testing.T) {
	path := filepath.Join(t.TempDir(), "decisions.jsonl")
	seed := strings.Repeat("x", 159) + "\n"
	if err := os.WriteFile(path, []byte(seed), decisionLogMode); err != nil {
		t.Fatalf("seed decision log: %v", err)
	}
	if err := os.Mkdir(path+decisionLogBackupSuffix, decisionLogMode); err != nil {
		t.Fatalf("block rotation: %v", err)
	}
	journal := openDecisionLogWithLimit(path, 150)
	journal.write([]any{"n", 1})
	content, err := os.ReadFile(filepath.Clean(path))
	if err != nil {
		t.Fatalf("read decision log: %v", err)
	}
	if lines := strings.Count(string(content), "\n"); lines != 2 {
		t.Fatalf("lines = %d, want the seed and the new entry", lines)
	}
}

func TestDecisionLogStopsWritingPastTwiceTheLimit(t *testing.T) {
	path := filepath.Join(t.TempDir(), "decisions.jsonl")
	seed := strings.Repeat("x", 59) + "\n"
	if err := os.WriteFile(path, []byte(seed), decisionLogMode); err != nil {
		t.Fatalf("seed decision log: %v", err)
	}
	if err := os.Mkdir(path+decisionLogBackupSuffix, decisionLogMode); err != nil {
		t.Fatalf("block rotation: %v", err)
	}
	journal := openDecisionLogWithLimit(path, 50)
	for n := range 5 {
		journal.write([]any{"n", n})
	}
	content, err := os.ReadFile(filepath.Clean(path))
	if err != nil {
		t.Fatalf("read decision log: %v", err)
	}
	if lines := strings.Count(string(content), "\n"); lines != 2 {
		t.Fatalf("lines = %d, want the seed and the first entry only, once the log is past twice its limit", lines)
	}
}

func TestDecisionLogRestrictsAnExistingFileToItsOwner(t *testing.T) {
	path := filepath.Join(t.TempDir(), "decisions.jsonl")
	if err := os.WriteFile(path, nil, 0o644); err != nil {
		t.Fatalf("seed decision log: %v", err)
	}
	openDecisionLog(path)
	info, err := os.Stat(path)
	if err != nil || info.Mode().Perm() != decisionLogMode {
		t.Fatalf("mode = %v, %v, want %v", info, err, os.FileMode(decisionLogMode))
	}
}

func TestDecisionLogTagsEachEntryWithItsCompaction(t *testing.T) {
	path := filepath.Join(t.TempDir(), "decisions.jsonl")
	stage := newTestCompactor(t, &fakeJev{answers: sessionAnswers()}, map[string]string{settingDecisionLog: path})
	if _, _, err := compactSession(t, stage); err != nil {
		t.Fatalf("first Compact: %v", err)
	}
	first := readDecisionLog(t, path)
	if _, _, err := compactSession(t, stage); err != nil {
		t.Fatalf("second Compact: %v", err)
	}
	all := readDecisionLog(t, path)
	firstID, ok := first[0]["compaction"].(string)
	if !ok || firstID == "" {
		t.Fatalf("entry %v has no compaction id", first[0])
	}
	for _, entry := range first {
		if entry["compaction"] != firstID {
			t.Fatalf("entry %v has compaction %v, want %q", entry, entry["compaction"], firstID)
		}
	}
	if second := all[len(first)]["compaction"]; second == firstID {
		t.Fatalf("second compaction reuses id %q, want one id per compaction", firstID)
	}
}

func TestDecisionLogSaysWhyAShortResultIsKept(t *testing.T) {
	path := filepath.Join(t.TempDir(), "decisions.jsonl")
	stage := newTestCompactor(t, &fakeJev{answers: sessionAnswers()}, map[string]string{settingDecisionLog: path, settingMinResultChars: "10"})
	if _, _, err := compactSession(t, stage); err != nil {
		t.Fatalf("Compact: %v", err)
	}
	for _, entry := range readDecisionLog(t, path) {
		if entry["tool"] == "Bash" && entry["result_size"] == float64(len("a b c")) {
			if entry["verdict"] != "keep" || entry["reason"] != reasonShort {
				t.Fatalf("short result entry = %v, want keep for reason %q", entry, reasonShort)
			}
			return
		}
	}
	t.Fatal("no decision logged for the short result, want one kept for reason " + reasonShort)
}
