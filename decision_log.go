package jev

import (
	"crypto/rand"
	"encoding/json"
	"log/slog"
	"os"
	"path/filepath"
	"time"
)

const (
	decisionLogMode         = 0o600
	decisionLogEvent        = "jev: call decided"
	decisionLogMaxBytes     = 10 << 20
	decisionLogBackupSuffix = ".1"
)

type decisionLog struct {
	file       *os.File
	compaction string
}

func openDecisionLog(path string) *decisionLog {
	return openDecisionLogWithLimit(path, decisionLogMaxBytes)
}

func openDecisionLogWithLimit(path string, limit int64) *decisionLog {
	if path == "" {
		return nil
	}
	clean := filepath.Clean(path)
	rotateDecisionLog(clean, limit)
	file, err := os.OpenFile(clean, os.O_APPEND|os.O_CREATE|os.O_WRONLY, decisionLogMode)
	if err != nil {
		slog.Warn("jev: decision log not opened, the compaction goes on", "path", path, "err", err)
		return nil
	}
	if err := file.Chmod(decisionLogMode); err != nil {
		slog.Warn("jev: decision log not restricted to its owner, the compaction goes on", "path", path, "err", err)
		if closeErr := file.Close(); closeErr != nil {
			slog.Debug("jev: close decision log", "err", closeErr)
		}
		return nil
	}
	return &decisionLog{file: file, compaction: rand.Text()}
}

func rotateDecisionLog(path string, limit int64) {
	info, err := os.Stat(path)
	if err != nil || info.Size() < limit {
		return
	}
	if err := os.Rename(path, path+decisionLogBackupSuffix); err != nil {
		slog.Warn("jev: decision log not rotated, the compaction goes on", "path", path, "err", err)
	}
}

func (j *decisionLog) write(fields []any) {
	if j == nil {
		return
	}
	entry := map[string]any{"time": time.Now().UTC(), "msg": decisionLogEvent, "compaction": j.compaction}
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
