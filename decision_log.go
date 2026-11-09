package jev

import (
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"sync"
	"time"
)

const (
	decisionLogMode         = 0o600
	decisionLogEvent        = "jev: call decided"
	decisionLogMaxBytes     = 10 << 20
	decisionLogBackupSuffix = ".1"
	decisionLogLockSuffix   = ".lock"
	decisionLogOverflow     = 2
)

var errDecisionLogStopped = errors.New("decision log stopped")

type decisionLog struct {
	mutex          sync.Mutex
	path           string
	limit          int64
	compaction     string
	rotationFailed bool
	stopped        bool
}

func openDecisionLog(path string) *decisionLog {
	return openDecisionLogWithLimit(path, decisionLogMaxBytes)
}

func openDecisionLogWithLimit(path string, limit int64) *decisionLog {
	if path == "" {
		return nil
	}
	journal := &decisionLog{path: filepath.Clean(path), limit: limit, compaction: rand.Text()}
	err := journal.locked(func(file *os.File) error { return file.Chmod(decisionLogMode) })
	if err != nil {
		slog.Warn("jev: decision log not opened, the compaction goes on", "path", path, "err", err)
		return nil
	}
	return journal
}

func (j *decisionLog) locked(use func(*os.File) error) error {
	j.mutex.Lock()
	defer j.mutex.Unlock()
	if j.stopped {
		return errDecisionLogStopped
	}
	lock, err := openLockFile(j.path + decisionLogLockSuffix)
	if err != nil {
		return err
	}
	defer closeQuietly(lock)
	unlock, err := lockFile(lock)
	if err != nil {
		return fmt.Errorf("decision log lock: %w", err)
	}
	defer unlock()
	if rotateErr := j.rotateIfFull(); rotateErr != nil {
		return rotateErr
	}
	file, err := os.OpenFile(j.path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, decisionLogMode)
	if err != nil {
		return fmt.Errorf("decision log: %w", err)
	}
	defer closeQuietly(file)
	return use(file)
}

func openLockFile(path string) (*os.File, error) {
	lock, err := os.OpenFile(filepath.Clean(path), os.O_CREATE|os.O_WRONLY, decisionLogMode)
	if err != nil {
		return nil, fmt.Errorf("decision log lock: %w", err)
	}
	if err = lock.Chmod(decisionLogMode); err != nil {
		closeQuietly(lock)
		return nil, fmt.Errorf("decision log lock: %w", err)
	}
	return lock, nil
}

func (j *decisionLog) rotateIfFull() error {
	info, err := os.Stat(j.path)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("decision log: %w", err)
	}
	if info.Size() < j.limit {
		return nil
	}
	renameErr := os.Rename(j.path, j.path+decisionLogBackupSuffix)
	if renameErr == nil {
		return nil
	}
	if info.Size() >= decisionLogOverflow*j.limit {
		j.stopped = true
		slog.Warn("jev: decision log stopped, it is over twice its limit and cannot be rotated", "path", j.path, "err", renameErr)
		return errDecisionLogStopped
	}
	if !j.rotationFailed {
		j.rotationFailed = true
		slog.Warn("jev: decision log not rotated, the compaction goes on", "path", j.path, "err", renameErr)
	}
	return nil
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
	err = j.locked(func(file *os.File) error {
		_, writeErr := file.Write(append(line, '\n'))
		return writeErr
	})
	if err != nil && !errors.Is(err, errDecisionLogStopped) {
		slog.Warn("jev: decision log write failed, the compaction goes on", "err", err)
	}
}

func closeQuietly(file *os.File) {
	if err := file.Close(); err != nil {
		slog.Debug("jev: close decision log", "err", err)
	}
}
