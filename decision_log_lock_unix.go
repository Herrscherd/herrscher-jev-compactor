//go:build unix

package jev

import (
	"log/slog"
	"os"

	"golang.org/x/sys/unix"
)

func lockFile(file *os.File) (func(), error) {
	if err := unix.Flock(int(file.Fd()), unix.LOCK_EX); err != nil {
		return nil, err
	}
	return func() {
		if err := unix.Flock(int(file.Fd()), unix.LOCK_UN); err != nil {
			slog.Debug("jev: unlock decision log", "err", err)
		}
	}, nil
}
