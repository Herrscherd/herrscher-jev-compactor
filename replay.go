package jev

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"time"

	contracts "github.com/Herrscherd/herrscher-contracts"
)

const (
	snapshotVersion      = 1
	snapshotDirMode      = 0o700
	snapshotDirSharedBit = 0o077
	snapshotFileMode     = 0o600
	maxSnapshotBytes     = 64 << 20
	snapshotNameLayout   = "20060102T150405.000000000Z"
)

// ErrInvalidSnapshot is returned when a compaction snapshot cannot be read back:
// it is malformed, has another version, holds a role no history has, carries a
// probability outside [0, 1] or names a call twice. Match it with errors.Is.
var ErrInvalidSnapshot = errors.New("invalid jev snapshot")

var (
	errReplayAPIKey      = errors.New("a replay takes no api key")
	errSnapshotTooLarge  = errors.New("snapshot is larger than 64 MiB")
	errSnapshotDirShared = errors.New("snapshot directory is readable by its group or others")
	errSnapshotNotDir    = errors.New("snapshot directory is not a directory")
)

// Snapshot is what one compaction saw and decided on: the history as Jev read
// it, the goal, the effective settings, the probabilities Jev gave per call and
// the calls the incremental cache kept without asking. Replay turns it back into
// a plan without any network call. The history is stored in clear, tool outputs
// included, so a snapshot holds whatever the session read or wrote.
type Snapshot struct {
	Version    int               `json:"version"`
	CapturedAt time.Time         `json:"captured_at"`
	Goal       string            `json:"goal"`
	Settings   map[string]string `json:"settings"`
	History    snapshotHistory   `json:"history"`
	Judgments  []Judgment        `json:"judgments"`
	Cached     []string          `json:"cached,omitempty"`
}

// Judgment is the probability Jev gave for one tool call: that its call should
// stay in the history, and that its result should stay verbatim.
type Judgment struct {
	CallID  string  `json:"call_id"`
	PCall   float64 `json:"p_call"`
	PResult float64 `json:"p_result"`
}

// Replayed is the plan a snapshot gives under one set of settings. Counts are
// per tool call, sizes are the net removal the planned edits make. Cached counts
// the calls the incremental cache kept without asking Jev.
type Replayed struct {
	Calls         int
	Kept          int
	Truncated     int
	Pruned        int
	Unanswered    int
	Cached        int
	Reverted      bool
	CharsBefore   int
	CharsRemoved  int
	TokensBefore  int
	TokensRemoved int
	Edits         []contracts.HistoryEdit
}

type snapshotHistory struct {
	Messages []snapshotMessage `json:"messages"`
}

type snapshotMessage struct {
	Role  string         `json:"role"`
	Text  string         `json:"text,omitempty"`
	Calls []snapshotCall `json:"calls,omitempty"`
}

type snapshotCall struct {
	ID            string `json:"id"`
	Tool          string `json:"tool"`
	Input         string `json:"input"`
	Result        string `json:"result"`
	ResultIsError bool   `json:"result_is_error,omitempty"`
}

// DecodeSnapshot reads a snapshot written by a compaction. It refuses unknown
// fields, data after the snapshot object, and anything past 64 MiB.
func DecodeSnapshot(reader io.Reader) (Snapshot, error) {
	var snapshot Snapshot
	decoder := json.NewDecoder(io.LimitReader(reader, maxSnapshotBytes))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&snapshot); err != nil {
		return Snapshot{}, fmt.Errorf("%w: %w", ErrInvalidSnapshot, err)
	}
	var trailing json.RawMessage
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return Snapshot{}, fmt.Errorf("%w: data after the snapshot object", ErrInvalidSnapshot)
	}
	if err := snapshot.check(); err != nil {
		return Snapshot{}, err
	}
	return snapshot, nil
}

func (s Snapshot) check() error {
	if s.Version != snapshotVersion {
		return fmt.Errorf("%w: version %d, want %d", ErrInvalidSnapshot, s.Version, snapshotVersion)
	}
	judged := make(map[string]bool, len(s.Judgments))
	for _, judgment := range s.Judgments {
		if judged[judgment.CallID] {
			return fmt.Errorf("%w: call %q judged twice", ErrInvalidSnapshot, judgment.CallID)
		}
		judged[judgment.CallID] = true
		if !isProbability(judgment.PCall) || !isProbability(judgment.PResult) {
			return fmt.Errorf("%w: call %q has a probability outside [0, 1]", ErrInvalidSnapshot, judgment.CallID)
		}
	}
	served := make(map[string]bool, len(s.Cached))
	for _, callID := range s.Cached {
		if served[callID] {
			return fmt.Errorf("%w: call %q served by the cache twice", ErrInvalidSnapshot, callID)
		}
		served[callID] = true
	}
	return nil
}

func isProbability(p float64) bool {
	return p >= 0 && p <= 1
}

// Replay gives the plan a snapshot yields under the settings of cfg. It makes
// no network call: the probabilities come from the snapshot. A call the
// snapshot holds no probability for is kept and counted as unanswered. When
// incremental is on, the calls the snapshot names as served by the cache are
// kept and counted as cached. The settings are read like a live compaction
// reads them, with defaults applied, and api_key is refused.
func Replay(snapshot Snapshot, cfg contracts.PluginConfig) (Replayed, error) {
	if err := snapshot.check(); err != nil {
		return Replayed{}, err
	}
	if err := checkReplaySettings(cfg); err != nil {
		return Replayed{}, err
	}
	opts, err := readOptions(cfg)
	if err != nil {
		return Replayed{}, err
	}
	history, err := snapshot.History.history()
	if err != nil {
		return Replayed{}, err
	}
	stage := &compactor{opts: opts}
	if opts.incremental {
		stage.rememberServed(snapshot, history)
	}
	report, err := stage.decide(context.Background(), history, contracts.SessionInfo{}, snapshot.Goal, replayAnswers(snapshot.Judgments), nil)
	if err != nil {
		return Replayed{}, err
	}
	return stage.replayed(report, history), nil
}

func checkReplaySettings(cfg contracts.PluginConfig) error {
	if _, given := cfg.Settings[settingAPIKey]; given {
		return fmt.Errorf("%w: %s: %w", ErrInvalidSetting, settingAPIKey, errReplayAPIKey)
	}
	declared := make(map[string]bool)
	for _, setting := range settings(newMessages()) {
		declared[setting.Key] = true
	}
	for _, key := range slices.Sorted(maps.Keys(cfg.Settings)) {
		if !declared[key] {
			return fmt.Errorf("%w: %q is not a setting of the stage", ErrInvalidSetting, key)
		}
	}
	return nil
}

func replayAnswers(judgments []Judgment) answerFunc {
	byCall := make(map[string]Judgment, len(judgments))
	for _, judgment := range judgments {
		byCall[judgment.CallID] = judgment
	}
	return func(_ context.Context, _, asked []toolCall) (answerSet, error) {
		answers := make(map[string]callAnswer, len(asked))
		for _, call := range asked {
			if judgment, known := byCall[call.source.ID]; known {
				answers[call.ref] = callAnswer{keepCall: judgment.PCall, keepResult: judgment.PResult}
			}
		}
		return answerSet{answers: answers}, nil
	}
}

func (c *compactor) rememberServed(snapshot Snapshot, history contracts.History) {
	c.cache = newKeepCache()
	served := make(map[string]bool, len(snapshot.Cached))
	for _, id := range snapshot.Cached {
		served[id] = true
	}
	var calls []toolCall
	for _, call := range collectCallsWith(history, c.opts.preservation(), c.opts.includeEmpty) {
		if served[call.source.ID] {
			calls = append(calls, call)
		}
	}
	c.scopeFor(contracts.SessionInfo{}, snapshot.Goal).remember(calls)
}

func (c *compactor) replayed(report planReport, history contracts.History) Replayed {
	result := Replayed{
		Calls:        report.calls,
		Unanswered:   report.unanswered,
		Cached:       report.cached,
		CharsBefore:  unitCharacters.historySize(history),
		TokensBefore: unitEstimated.historySize(history),
	}
	if verdict := c.verdictOf(report, history).verdict; verdict == planOutOfReach || verdict == planShort {
		result.Reverted = true
		result.Kept = report.calls
		return result
	}
	result.Edits = report.edits
	result.Kept = report.calls - len(report.edits)
	for _, edit := range report.edits {
		if edit.Action == contracts.PruneCall {
			result.Pruned++
			continue
		}
		result.Truncated++
	}
	result.CharsRemoved = report.removed.in(unitCharacters)
	result.TokensRemoved = report.removed.in(unitEstimated)
	return result
}

func (c *compactor) capture(history contracts.History, report planReport) {
	if c.opts.replayDir == "" {
		return
	}
	snapshot := Snapshot{
		Version:    snapshotVersion,
		CapturedAt: time.Now().UTC(),
		Goal:       report.goal,
		Settings:   c.settings,
		History:    snapshotHistoryOf(history),
		Judgments:  report.judgments,
		Cached:     report.cachedIDs,
	}
	path, err := writeSnapshot(c.opts.replayDir, snapshot)
	if err != nil {
		slog.Warn("jev: compaction snapshot not written, the compaction goes on", "err", err)
		return
	}
	slog.Debug("jev: compaction snapshot written", "path", path)
}

func writeSnapshot(dir string, snapshot Snapshot) (string, error) {
	dir = filepath.Clean(dir)
	encoded, err := json.MarshalIndent(snapshot, "", "  ")
	if err != nil {
		return "", err
	}
	encoded = append(encoded, '\n')
	if len(encoded) > maxSnapshotBytes {
		return "", errSnapshotTooLarge
	}
	if dirErr := prepareSnapshotDir(dir); dirErr != nil {
		return "", dirErr
	}
	name := "compaction-" + snapshot.CapturedAt.Format(snapshotNameLayout) + "-" + rand.Text() + ".json"
	path := filepath.Clean(filepath.Join(dir, name))
	file, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, snapshotFileMode)
	if err != nil {
		return "", err
	}
	if _, err = file.Write(encoded); err != nil {
		return "", errors.Join(err, file.Close(), os.Remove(path))
	}
	if err := file.Close(); err != nil {
		return "", errors.Join(err, os.Remove(path))
	}
	return path, nil
}

func prepareSnapshotDir(dir string) error {
	info, err := os.Stat(dir)
	if errors.Is(err, fs.ErrNotExist) {
		return os.MkdirAll(dir, snapshotDirMode)
	}
	if err != nil {
		return err
	}
	if !info.IsDir() {
		return fmt.Errorf("%s: %w", dir, errSnapshotNotDir)
	}
	if info.Mode().Perm()&snapshotDirSharedBit != 0 {
		return fmt.Errorf("%s: %w", dir, errSnapshotDirShared)
	}
	return nil
}

func effectiveSettings(cfg contracts.PluginConfig, texts *messages) map[string]string {
	effective := make(map[string]string)
	for _, setting := range settings(texts) {
		if setting.Key == settingAPIKey || setting.Key == settingReplayDir {
			continue
		}
		if value := orDefault(cfg.Get(setting.Key), setting.Default); value != "" {
			effective[setting.Key] = value
		}
	}
	return effective
}

func snapshotHistoryOf(history contracts.History) snapshotHistory {
	converted := snapshotHistory{Messages: make([]snapshotMessage, 0, len(history.Messages))}
	for _, message := range history.Messages {
		item := snapshotMessage{Role: message.Role.String(), Text: message.Text}
		for _, call := range message.Calls {
			item.Calls = append(item.Calls, snapshotCall{ID: call.ID, Tool: call.Tool, Input: call.Input, Result: call.Result, ResultIsError: call.ResultIsError})
		}
		converted.Messages = append(converted.Messages, item)
	}
	return converted
}

func (h snapshotHistory) history() (contracts.History, error) {
	var history contracts.History
	for _, message := range h.Messages {
		role, err := contracts.ParseHistoryRole(message.Role)
		if err != nil {
			return contracts.History{}, fmt.Errorf("%w: %w", ErrInvalidSnapshot, err)
		}
		item := contracts.HistoryMessage{Role: role, Text: message.Text}
		for _, call := range message.Calls {
			item.Calls = append(item.Calls, contracts.HistoryCall{ID: call.ID, Tool: call.Tool, Input: call.Input, Result: call.Result, ResultIsError: call.ResultIsError})
		}
		history.Messages = append(history.Messages, item)
	}
	return history, nil
}
