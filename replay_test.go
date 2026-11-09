package jev

import (
	"bytes"
	"encoding/json"
	"errors"
	"maps"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"

	contracts "github.com/Herrscherd/herrscher-contracts"
)

const fixturePath = "testdata/replay/session.json"

func fixtureSnapshot(t *testing.T) Snapshot {
	t.Helper()
	data, err := os.ReadFile(fixturePath)
	if err != nil {
		t.Fatalf("read the fixture: %v", err)
	}
	snapshot, err := DecodeSnapshot(bytes.NewReader(data))
	if err != nil {
		t.Fatalf("DecodeSnapshot: %v", err)
	}
	return snapshot
}

func onlyEntry(t *testing.T, dir string) os.DirEntry {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read the snapshot dir: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("snapshot dir holds %d files, want 1", len(entries))
	}
	return entries[0]
}

func onlySnapshot(t *testing.T, dir string) Snapshot {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(dir, onlyEntry(t, dir).Name()))
	if err != nil {
		t.Fatalf("read the snapshot: %v", err)
	}
	snapshot, err := DecodeSnapshot(bytes.NewReader(data))
	if err != nil {
		t.Fatalf("DecodeSnapshot of the captured file: %v", err)
	}
	return snapshot
}

func onlySnapshotInfo(t *testing.T, dir string) os.FileInfo {
	t.Helper()
	info, err := onlyEntry(t, dir).Info()
	if err != nil {
		t.Fatalf("stat the snapshot: %v", err)
	}
	return info
}

func TestReplayPlansWhatTheCompactionDid(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "snapshots")
	server := &fakeJev{answers: sessionAnswers()}
	stage := newTestCompactor(t, server, map[string]string{settingReplayDir: dir})
	editor := &fakeHistory{history: sessionHistory(), outcome: contracts.PruneOutcome{Edited: 2, CharsBefore: 1000, CharsAfter: 600}}
	if _, err := stage.Compact(t.Context(), contracts.CompactRequest{History: editor}, nil); err != nil {
		t.Fatalf("Compact: %v", err)
	}
	if len(editor.pruned) != 1 {
		t.Fatalf("compaction pruned %d times, want 1", len(editor.pruned))
	}
	snapshot := onlySnapshot(t, dir)
	replayed, err := Replay(snapshot, contracts.PluginConfig{Settings: snapshot.Settings})
	if err != nil {
		t.Fatalf("Replay: %v", err)
	}
	if !slices.Equal(replayed.Edits, editor.pruned[0]) {
		t.Fatalf("replayed %+v, compaction pruned %+v", replayed.Edits, editor.pruned[0])
	}
	if replayed.Unanswered != 0 || replayed.Reverted {
		t.Fatalf("replay unanswered=%d reverted=%t, want 0 and false", replayed.Unanswered, replayed.Reverted)
	}
}

func TestReplayOfTheFixturePrunesWhatTheJudgmentsSay(t *testing.T) {
	replayed, err := Replay(fixtureSnapshot(t), contracts.PluginConfig{Settings: fixtureSnapshot(t).Settings})
	if err != nil {
		t.Fatalf("Replay: %v", err)
	}
	want := []contracts.HistoryEdit{
		{CallID: "toolu_02", Action: contracts.PruneResult, KeepChars: defaultTruncateHead},
		{CallID: "toolu_03", Action: contracts.PruneResult, KeepChars: defaultTruncateHead},
		{CallID: "toolu_04", Action: contracts.PruneCall},
	}
	if !slices.Equal(replayed.Edits, want) {
		t.Fatalf("edits %+v, want %+v", replayed.Edits, want)
	}
	if replayed.Calls != 6 || replayed.Kept != 3 || replayed.Truncated != 2 || replayed.Pruned != 1 {
		t.Fatalf("calls=%d kept=%d truncated=%d pruned=%d, want 6, 3, 2, 1", replayed.Calls, replayed.Kept, replayed.Truncated, replayed.Pruned)
	}
}

func TestReplayKeepsAndCountsTheCallsJevNeverJudged(t *testing.T) {
	snapshot := fixtureSnapshot(t)
	snapshot.Judgments = slices.DeleteFunc(snapshot.Judgments, func(judgment Judgment) bool {
		return judgment.CallID == "toolu_03"
	})
	replayed, err := Replay(snapshot, contracts.PluginConfig{Settings: snapshot.Settings})
	if err != nil {
		t.Fatalf("Replay: %v", err)
	}
	if replayed.Unanswered != 1 {
		t.Fatalf("unanswered = %d, want 1", replayed.Unanswered)
	}
	for _, edit := range replayed.Edits {
		if edit.CallID == "toolu_03" {
			t.Fatalf("unjudged call pruned: %+v", edit)
		}
	}
}

func TestReplayReportsAShortReductionAsReverted(t *testing.T) {
	snapshot := fixtureSnapshot(t)
	settings := maps.Clone(snapshot.Settings)
	settings[settingOnShort] = "revert"
	settings[settingMinReduction] = "0.9"
	replayed, err := Replay(snapshot, contracts.PluginConfig{Settings: settings})
	if err != nil {
		t.Fatalf("Replay: %v", err)
	}
	if !replayed.Reverted || len(replayed.Edits) != 0 || replayed.Kept != replayed.Calls {
		t.Fatalf("reverted=%t edits=%d kept=%d calls=%d, want a reverted plan with no edit", replayed.Reverted, len(replayed.Edits), replayed.Kept, replayed.Calls)
	}
}

func TestReplayRefusesTheAPIKeyAndUnknownSettings(t *testing.T) {
	snapshot := fixtureSnapshot(t)
	for _, settings := range []map[string]string{
		{settingAPIKey: "not-for-replay"},
		{"bogus_setting": "1"},
	} {
		if _, err := Replay(snapshot, contracts.PluginConfig{Settings: settings}); !errors.Is(err, ErrInvalidSetting) {
			t.Fatalf("Replay(%v) error = %v, want ErrInvalidSetting", settings, err)
		}
	}
}

func TestDecodeSnapshotRefusesOtherVersionsAndUnknownFields(t *testing.T) {
	for _, document := range []string{
		`{"version":2,"history":{"messages":[]}}`,
		`{"version":1,"extra":true}`,
		`not json`,
	} {
		if _, err := DecodeSnapshot(strings.NewReader(document)); !errors.Is(err, ErrInvalidSnapshot) {
			t.Fatalf("DecodeSnapshot(%s) error = %v, want ErrInvalidSnapshot", document, err)
		}
	}
}

func TestReplayRefusesAnUnknownRole(t *testing.T) {
	snapshot := fixtureSnapshot(t)
	snapshot.History.Messages[0].Role = "system"
	if _, err := Replay(snapshot, contracts.PluginConfig{Settings: snapshot.Settings}); !errors.Is(err, ErrInvalidSnapshot) {
		t.Fatalf("Replay error = %v, want ErrInvalidSnapshot", err)
	}
}

func TestReplayDirMustNotBeAFile(t *testing.T) {
	file := filepath.Join(t.TempDir(), "taken")
	if err := os.WriteFile(file, []byte("x"), 0o600); err != nil {
		t.Fatalf("write the file: %v", err)
	}
	cfg := contracts.PluginConfig{Settings: map[string]string{settingAPIKey: "test-key", settingReplayDir: file}}
	if _, err := newCompactor(cfg, newMessages(), &http.Client{}); !errors.Is(err, ErrInvalidSetting) {
		t.Fatalf("newCompactor error = %v, want ErrInvalidSetting", err)
	}
}

func TestCaptureFailureNeverStopsTheCompaction(t *testing.T) {
	blocker := filepath.Join(t.TempDir(), "taken")
	if err := os.WriteFile(blocker, []byte("x"), 0o600); err != nil {
		t.Fatalf("write the file: %v", err)
	}
	server := &fakeJev{answers: sessionAnswers()}
	stage := newTestCompactor(t, server, map[string]string{settingReplayDir: filepath.Join(blocker, "snapshots")})
	editor := &fakeHistory{history: sessionHistory(), outcome: contracts.PruneOutcome{Edited: 2, CharsBefore: 1000, CharsAfter: 600}}
	result, err := stage.Compact(t.Context(), contracts.CompactRequest{History: editor}, nil)
	if err != nil {
		t.Fatalf("Compact: %v", err)
	}
	if len(editor.pruned) != 1 || result.Stats == nil || result.Stats.Outcome != contracts.CompactDone {
		t.Fatalf("pruned %d times, outcome %v, want the compaction to go on", len(editor.pruned), result.Stats)
	}
}

func TestCapturedFilesArePrivate(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX permission bits do not apply on Windows")
	}
	dir := filepath.Join(t.TempDir(), "snapshots")
	server := &fakeJev{answers: sessionAnswers()}
	stage := newTestCompactor(t, server, map[string]string{settingReplayDir: dir})
	editor := &fakeHistory{history: sessionHistory(), outcome: contracts.PruneOutcome{Edited: 2, CharsBefore: 1000, CharsAfter: 600}}
	if _, err := stage.Compact(t.Context(), contracts.CompactRequest{History: editor}, nil); err != nil {
		t.Fatalf("Compact: %v", err)
	}
	info := onlySnapshotInfo(t, dir)
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("snapshot mode %o, want 600", info.Mode().Perm())
	}
	dirInfo, err := os.Stat(dir)
	if err != nil {
		t.Fatalf("stat the snapshot dir: %v", err)
	}
	if dirInfo.Mode().Perm()&0o077 != 0 {
		t.Fatalf("snapshot dir mode %o, want no access for group or others", dirInfo.Mode().Perm())
	}
}

func TestWriteSnapshotNeverOverwrites(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "snapshots")
	snapshot := fixtureSnapshot(t)
	first, err := writeSnapshot(dir, snapshot)
	if err != nil {
		t.Fatalf("first writeSnapshot: %v", err)
	}
	second, err := writeSnapshot(dir, snapshot)
	if err != nil {
		t.Fatalf("second writeSnapshot: %v", err)
	}
	if first == second {
		t.Fatalf("two captures wrote the same file %s", first)
	}
}

func encodedSnapshot(t *testing.T, snapshot Snapshot) []byte {
	t.Helper()
	data, err := json.Marshal(snapshot)
	if err != nil {
		t.Fatalf("marshal the snapshot: %v", err)
	}
	return data
}

func TestDecodeSnapshotRefusesDataAfterTheObject(t *testing.T) {
	data := append(encodedSnapshot(t, fixtureSnapshot(t)), []byte(" {}")...)
	if _, err := DecodeSnapshot(bytes.NewReader(data)); !errors.Is(err, ErrInvalidSnapshot) {
		t.Fatalf("DecodeSnapshot error = %v, want ErrInvalidSnapshot", err)
	}
}

func TestDecodeSnapshotBoundsTheProbabilities(t *testing.T) {
	for _, value := range []float64{-0.1, 1.5} {
		snapshot := fixtureSnapshot(t)
		snapshot.Judgments[0].PCall = value
		if _, err := DecodeSnapshot(bytes.NewReader(encodedSnapshot(t, snapshot))); !errors.Is(err, ErrInvalidSnapshot) {
			t.Fatalf("p_call %v: DecodeSnapshot error = %v, want ErrInvalidSnapshot", value, err)
		}
	}
}

func TestDecodeSnapshotRefusesADuplicateCallID(t *testing.T) {
	snapshot := fixtureSnapshot(t)
	snapshot.Judgments = append(snapshot.Judgments, snapshot.Judgments[0])
	if _, err := DecodeSnapshot(bytes.NewReader(encodedSnapshot(t, snapshot))); !errors.Is(err, ErrInvalidSnapshot) {
		t.Fatalf("DecodeSnapshot error = %v, want ErrInvalidSnapshot", err)
	}
}

func TestReplayRechecksTheVersion(t *testing.T) {
	snapshot := fixtureSnapshot(t)
	snapshot.Version = snapshotVersion + 1
	if _, err := Replay(snapshot, contracts.PluginConfig{Settings: snapshot.Settings}); !errors.Is(err, ErrInvalidSnapshot) {
		t.Fatalf("Replay error = %v, want ErrInvalidSnapshot", err)
	}
}

func TestReplayNeverEchoesTheAPIKey(t *testing.T) {
	const canary = "canary-secret-value-7f3a"
	_, err := Replay(fixtureSnapshot(t), contracts.PluginConfig{Settings: map[string]string{settingAPIKey: canary}})
	if !errors.Is(err, ErrInvalidSetting) {
		t.Fatalf("Replay error = %v, want ErrInvalidSetting", err)
	}
	if strings.Contains(err.Error(), canary) {
		t.Fatalf("the api key leaked into %q", err)
	}
}

func TestReplayOfNothingToPruneIsNotReverted(t *testing.T) {
	snapshot := fixtureSnapshot(t)
	for index := range snapshot.Judgments {
		snapshot.Judgments[index].PCall = 1
		snapshot.Judgments[index].PResult = 1
	}
	settings := map[string]string{settingKeepThreshold: "0", settingOnShort: "revert", settingMinReduction: "0.01"}
	replayed, err := Replay(snapshot, contracts.PluginConfig{Settings: settings})
	if err != nil {
		t.Fatalf("Replay: %v", err)
	}
	if replayed.Reverted || len(replayed.Edits) != 0 {
		t.Fatalf("reverted=%t edits=%d, want nothing reverted and nothing pruned", replayed.Reverted, len(replayed.Edits))
	}
}

func TestReplayCountsTheCachedCallsApartFromTheUnanswered(t *testing.T) {
	snapshot := fixtureSnapshot(t)
	snapshot.Judgments = slices.DeleteFunc(snapshot.Judgments, func(judgment Judgment) bool {
		return judgment.CallID == "toolu_03"
	})
	snapshot.Cached = []string{"toolu_03"}

	settings := maps.Clone(snapshot.Settings)
	settings[settingIncremental] = "true"
	incremental, err := Replay(snapshot, contracts.PluginConfig{Settings: settings})
	if err != nil {
		t.Fatalf("Replay incremental: %v", err)
	}
	if incremental.Cached != 1 || incremental.Unanswered != 0 {
		t.Fatalf("incremental: cached=%d unanswered=%d, want 1 and 0", incremental.Cached, incremental.Unanswered)
	}

	plain, err := Replay(snapshot, contracts.PluginConfig{Settings: snapshot.Settings})
	if err != nil {
		t.Fatalf("Replay plain: %v", err)
	}
	if plain.Cached != 0 || plain.Unanswered != 1 {
		t.Fatalf("plain: cached=%d unanswered=%d, want 0 and 1", plain.Cached, plain.Unanswered)
	}
}

func TestWriteSnapshotRefusesASharedDirectory(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "shared")
	if err := os.Mkdir(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := writeSnapshot(dir, fixtureSnapshot(t)); !errors.Is(err, errSnapshotDirShared) {
		t.Fatalf("writeSnapshot error = %v, want errSnapshotDirShared", err)
	}
	if entries, err := os.ReadDir(dir); err != nil || len(entries) != 0 {
		t.Fatalf("shared dir holds %d entries (%v), want none", len(entries), err)
	}
}

func TestWriteSnapshotRefusesAnOversizedSnapshot(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "snapshots")
	snapshot := fixtureSnapshot(t)
	snapshot.Goal = strings.Repeat("g", maxSnapshotBytes)
	if _, err := writeSnapshot(dir, snapshot); !errors.Is(err, errSnapshotTooLarge) {
		t.Fatalf("writeSnapshot error = %v, want errSnapshotTooLarge", err)
	}
	if entries, err := os.ReadDir(dir); err == nil && len(entries) != 0 {
		t.Fatalf("oversized snapshot left %d files behind", len(entries))
	}
}
