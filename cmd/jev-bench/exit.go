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
	if err == nil {
		return exitSuccess
	}
	if _, ok := errors.AsType[usageError](err); ok {
		return exitUsage
	}
	switch {
	case errors.Is(err, jev.ErrInvalidSnapshot):
		return exitDataError
	case errors.Is(err, jev.ErrInvalidSetting), errors.Is(err, errSettingsLine), errors.Is(err, errSettingsTooLarge):
		return exitConfig
	case errors.Is(err, fs.ErrNotExist), errors.Is(err, errNotRegular):
		return exitNoInput
	default:
		return exitIOError
	}
}
