//go:build !unix && !windows

package jev

import "os"

func lockFile(*os.File) (func(), error) { return func() {}, nil }
