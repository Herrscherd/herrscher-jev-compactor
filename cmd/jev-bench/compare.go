package main

import (
	"errors"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"strings"

	contracts "github.com/Herrscherd/herrscher-contracts"

	jev "github.com/Herrscherd/herrscher-jev-compactor"
)

const recordedName = "recorded"

var errSettingsLine = errors.New("settings line is not key=value")

type comparison struct {
	Snapshots int       `json:"snapshots"`
	Settings  []summary `json:"settings"`
}

type summary struct {
	Setting         string  `json:"setting"`
	Kept            int     `json:"kept"`
	Truncated       int     `json:"truncated"`
	Pruned          int     `json:"pruned"`
	Unanswered      int     `json:"unanswered"`
	Reverted        int     `json:"reverted"`
	CharsReduction  float64 `json:"chars_reduction"`
	TokensReduction float64 `json:"tokens_reduction"`
}

type column struct {
	name      string
	overrides map[string]string
}

type tally struct {
	name          string
	kept          int
	truncated     int
	pruned        int
	unanswered    int
	reverted      int
	charsBefore   int
	charsRemoved  int
	tokensBefore  int
	tokensRemoved int
}

func compare(snapshotPaths, settingsPaths []string) (comparison, error) {
	snapshots, err := readSnapshots(snapshotPaths)
	if err != nil {
		return comparison{}, err
	}
	columns, err := readColumns(settingsPaths)
	if err != nil {
		return comparison{}, err
	}
	result := comparison{Snapshots: len(snapshots)}
	for _, current := range columns {
		total, replayErr := current.replay(snapshots)
		if replayErr != nil {
			return comparison{}, replayErr
		}
		result.Settings = append(result.Settings, total.summary())
	}
	return result, nil
}

func (c column) replay(snapshots []jev.Snapshot) (tally, error) {
	total := tally{name: c.name}
	for _, snapshot := range snapshots {
		replayed, err := jev.Replay(snapshot, contracts.PluginConfig{Settings: c.settingsFor(snapshot)})
		if err != nil {
			return tally{}, fmt.Errorf("%s: %w", c.name, err)
		}
		total = total.add(replayed)
	}
	return total, nil
}

func (c column) settingsFor(snapshot jev.Snapshot) map[string]string {
	merged := make(map[string]string, len(snapshot.Settings)+len(c.overrides))
	maps.Copy(merged, snapshot.Settings)
	maps.Copy(merged, c.overrides)
	return merged
}

func (t tally) add(replayed jev.Replayed) tally {
	t.kept += replayed.Kept
	t.truncated += replayed.Truncated
	t.pruned += replayed.Pruned
	t.unanswered += replayed.Unanswered
	t.charsBefore += replayed.CharsBefore
	t.charsRemoved += replayed.CharsRemoved
	t.tokensBefore += replayed.TokensBefore
	t.tokensRemoved += replayed.TokensRemoved
	if replayed.Reverted {
		t.reverted++
	}
	return t
}

func (t tally) summary() summary {
	return summary{
		Setting:         t.name,
		Kept:            t.kept,
		Truncated:       t.truncated,
		Pruned:          t.pruned,
		Unanswered:      t.unanswered,
		Reverted:        t.reverted,
		CharsReduction:  shareOf(t.charsRemoved, t.charsBefore),
		TokensReduction: shareOf(t.tokensRemoved, t.tokensBefore),
	}
}

func shareOf(part, whole int) float64 {
	if whole <= 0 {
		return 0
	}
	return float64(part) / float64(whole)
}

func readSnapshots(paths []string) ([]jev.Snapshot, error) {
	snapshots := make([]jev.Snapshot, 0, len(paths))
	for _, path := range paths {
		snapshot, err := readSnapshot(path)
		if err != nil {
			return nil, err
		}
		snapshots = append(snapshots, snapshot)
	}
	return snapshots, nil
}

func readSnapshot(path string) (jev.Snapshot, error) {
	file, err := os.Open(filepath.Clean(path))
	if err != nil {
		return jev.Snapshot{}, err
	}
	snapshot, decodeErr := jev.DecodeSnapshot(file)
	if joined := errors.Join(decodeErr, file.Close()); joined != nil {
		return jev.Snapshot{}, fmt.Errorf("%s: %w", path, joined)
	}
	return snapshot, nil
}

func readColumns(paths []string) ([]column, error) {
	columns := []column{{name: recordedName}}
	for _, path := range paths {
		overrides, err := readSettings(path)
		if err != nil {
			return nil, err
		}
		name := strings.TrimSuffix(filepath.Base(path), filepath.Ext(path))
		columns = append(columns, column{name: name, overrides: overrides})
	}
	return columns, nil
}

func readSettings(path string) (map[string]string, error) {
	data, err := os.ReadFile(filepath.Clean(path))
	if err != nil {
		return nil, err
	}
	settings := make(map[string]string)
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		key, value, found := strings.Cut(line, "=")
		key = strings.TrimSpace(key)
		if !found || key == "" {
			return nil, fmt.Errorf("%w: %s: %q", errSettingsLine, path, line)
		}
		settings[key] = strings.TrimSpace(value)
	}
	return settings, nil
}
