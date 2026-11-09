package jev

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"time"

	contracts "github.com/Herrscherd/herrscher-contracts"
)

const (
	snapshotVersion    = 1
	snapshotDirMode    = 0o700
	snapshotFileMode   = 0o600
	maxSnapshotBytes   = 64 << 20
	snapshotNameLayout = "20060102T150405.000000000Z"
)

// ErrInvalidSnapshot is returned when a compaction snapshot cannot be read back:
// it is malformed, has another version, or holds a role no history has. Match
// it with errors.Is.
var ErrInvalidSnapshot = errors.New("invalid jev snapshot")

// Snapshot is what one compaction saw and decided on: the history as Jev read
// it, the goal, the effective settings and the probabilities Jev gave per call.
// Replay turns it back into a plan without any network call.
type Snapshot struct {
	Version    int               `json:"version"`
	CapturedAt time.Time         `json:"captured_at"`
	Goal       string            `json:"goal"`
	Settings   map[string]string `json:"settings"`
	History    snapshotHistory   `json:"history"`
	Judgments  []Judgment        `json:"judgments"`
}

// Judgment is the probability Jev gave for one tool call: that its call should
// stay in the history, and that its result should stay verbatim.
type Judgment struct {
	CallID  string  `json:"call_id"`
	PCall   float64 `json:"p_call"`
	PResult float64 `json:"p_result"`
}

// Replayed is the plan a snapshot gives under one set of settings. Counts are
// per tool call, sizes are the net removal the planned edits make.
type Replayed struct {
	Calls         int
	Kept          int
	Truncated     int
	Pruned        int
	Unanswered    int
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
// fields and stops after 64 MiB.
func DecodeSnapshot(reader io.Reader) (Snapshot, error) {
	var snapshot Snapshot
	decoder := json.NewDecoder(io.LimitReader(reader, maxSnapshotBytes))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&snapshot); err != nil {
		return Snapshot{}, fmt.Errorf("%w: %w", ErrInvalidSnapshot, err)
	}
	if snapshot.Version != snapshotVersion {
		return Snapshot{}, fmt.Errorf("%w: version %d, want %d", ErrInvalidSnapshot, snapshot.Version, snapshotVersion)
	}
	return snapshot, nil
}

// Replay gives the plan a snapshot yields under the settings of cfg. It makes
// no network call: the probabilities come from the snapshot. A call the
// snapshot holds no probability for is kept and counted as unanswered. The
// settings are read like a live compaction reads them, with defaults applied,
// and api_key is refused.
func Replay(snapshot Snapshot, cfg contracts.PluginConfig) (Replayed, error) {
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
	report, err := stage.decide(context.Background(), history, contracts.SessionInfo{}, snapshot.Goal, replayAnswers(snapshot.Judgments), nil)
	if err != nil {
		return Replayed{}, err
	}
	return stage.replayed(report, history), nil
}

func checkReplaySettings(cfg contracts.PluginConfig) error {
	declared := make(map[string]bool)
	for _, setting := range settings(newMessages()) {
		declared[setting.Key] = true
	}
	for key, value := range cfg.Settings {
		if key == settingAPIKey || !declared[key] {
			return &settingError{key: key, value: value}
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

func (c *compactor) replayed(report planReport, history contracts.History) Replayed {
	result := Replayed{
		Calls:        report.calls,
		Unanswered:   report.unanswered,
		CharsBefore:  unitCharacters.historySize(history),
		TokensBefore: unitEstimated.historySize(history),
	}
	_, short := c.shortOfTarget(report, history)
	if report.outOfReach || short {
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
	if err := os.MkdirAll(dir, snapshotDirMode); err != nil {
		return "", err
	}
	encoded, err := json.MarshalIndent(snapshot, "", "  ")
	if err != nil {
		return "", err
	}
	name := "compaction-" + snapshot.CapturedAt.Format(snapshotNameLayout) + "-" + rand.Text() + ".json"
	path := filepath.Clean(filepath.Join(dir, name))
	file, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, snapshotFileMode)
	if err != nil {
		return "", err
	}
	if _, err = file.Write(append(encoded, '\n')); err != nil {
		return "", errors.Join(err, file.Close())
	}
	return path, file.Close()
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
