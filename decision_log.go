package jev

import (
	"encoding/json"
	"log/slog"
	"os"
	"path/filepath"
	"time"
)

const (
	decisionLogMode  = 0o600
	decisionLogEvent = "jev: call decided"
)

type decisionLog struct {
	file *os.File
}

func openDecisionLog(path string) *decisionLog {
	if path == "" {
		return nil
	}
	file, err := os.OpenFile(filepath.Clean(path), os.O_APPEND|os.O_CREATE|os.O_WRONLY, decisionLogMode)
	if err != nil {
		slog.Warn("jev: decision log not opened, the compaction goes on", "path", path, "err", err)
		return nil
	}
	return &decisionLog{file: file}
}

func (j *decisionLog) write(fields []any) {
	if j == nil {
		return
	}
	entry := map[string]any{"time": time.Now().UTC(), "msg": decisionLogEvent}
	for index := 0; index+1 < len(fields); index += 2 {
		key, isText := fields[index].(string)
		if !isText {
			continue
		}
		entry[key] = fields[index+1]
	}
	line, err := json.Marshal(entry)
	if err != nil {
		slog.Warn("jev: decision log entry not encoded, the compaction goes on", "err", err)
		return
	}
	if _, err := j.file.Write(append(line, '\n')); err != nil {
		slog.Warn("jev: decision log write failed, the compaction goes on", "err", err)
	}
}

func (j *decisionLog) close() {
	if j == nil {
		return
	}
	if err := j.file.Close(); err != nil {
		slog.Debug("jev: close decision log", "err", err)
	}
}
