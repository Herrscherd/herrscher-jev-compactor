package main

import (
	"errors"
	"io/fs"

	jev "github.com/Herrscherd/herrscher-jev-compactor"
)

type exit uint8

const (
	exitSuccess   exit = 0
	exitUsage     exit = 2
	exitDataError exit = 65
	exitNoInput   exit = 66
	exitIOError   exit = 74
	exitConfig    exit = 78
)

func (e exit) code() int {
	return int(e)
}

func exitOf(err error) exit {
	switch {
	case err == nil:
		return exitSuccess
	case errors.Is(err, errUsage):
		return exitUsage
	case errors.Is(err, jev.ErrInvalidSnapshot):
		return exitDataError
	case errors.Is(err, jev.ErrInvalidSetting), errors.Is(err, errSettingsLine):
		return exitConfig
	case errors.Is(err, fs.ErrNotExist):
		return exitNoInput
	default:
		return exitIOError
	}
}
