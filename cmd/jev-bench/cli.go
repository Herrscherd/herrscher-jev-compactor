package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
)

var errUsage = errors.New("jev-bench usage: [-json] [-settings file] snapshot")

type options struct {
	json      bool
	snapshots []string
	settings  []string
}

func execute(args []string, stdout, stderr io.Writer) exit {
	opts, err := parseArgs(args, stderr)
	if errors.Is(err, flag.ErrHelp) {
		return exitSuccess
	}
	if err != nil {
		return fail(stderr, err)
	}
	result, err := compare(opts.snapshots, opts.settings)
	if err != nil {
		return fail(stderr, err)
	}
	if writeErr := write(stdout, result, opts.json); writeErr != nil {
		return fail(stderr, writeErr)
	}
	return exitSuccess
}

func fail(stderr io.Writer, err error) exit {
	if reportErr := report(stderr, err); reportErr != nil {
		return exitIOError
	}
	return exitOf(err)
}

func parseArgs(args []string, stderr io.Writer) (options, error) {
	var opts options
	flags := flag.NewFlagSet("jev-bench", flag.ContinueOnError)
	flags.SetOutput(stderr)
	flags.BoolVar(&opts.json, "json", false, "print the comparison as JSON")
	flags.Func("settings", "settings file of key=value lines laid over the recorded settings, repeatable", func(path string) error {
		opts.settings = append(opts.settings, path)
		return nil
	})
	rest := args
	for {
		if err := flags.Parse(rest); err != nil {
			if errors.Is(err, flag.ErrHelp) {
				return options{}, err
			}
			return options{}, fmt.Errorf("%w: %w", errUsage, err)
		}
		if flags.NArg() == 0 {
			break
		}
		opts.snapshots = append(opts.snapshots, flags.Arg(0))
		rest = flags.Args()[1:]
	}
	if len(opts.snapshots) == 0 {
		return options{}, fmt.Errorf("%w: at least one snapshot file", errUsage)
	}
	return opts, nil
}
