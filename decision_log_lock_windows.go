//go:build windows

package jev

import (
	"log/slog"
	"os"

	"golang.org/x/sys/windows"
)

func lockFile(file *os.File) (func(), error) {
	handle := windows.Handle(file.Fd())
	overlapped := &windows.Overlapped{}
	if err := windows.LockFileEx(handle, windows.LOCKFILE_EXCLUSIVE_LOCK, 0, 1, 0, overlapped); err != nil {
		return nil, err
	}
	return func() {
		if err := windows.UnlockFileEx(handle, 0, 1, 0, overlapped); err != nil {
			slog.Debug("jev: unlock decision log", "err", err)
		}
	}, nil
}
