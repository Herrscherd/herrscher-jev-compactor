package main

import (
	"errors"
	"flag"
	"fmt"
	"io"

	"github.com/Herrscherd/herrscher-contracts/i18n"

	jev "github.com/Herrscherd/herrscher-jev-compactor"
)

var errNoSnapshot = errors.New("at least one snapshot file is needed")

type usageError struct {
	cause error
}

func (e usageError) Error() string {
	return e.cause.Error()
}

func (e usageError) Unwrap() error {
	return e.cause
}

type options struct {
	json      bool
	lang      i18n.Language
	snapshots []string
	settings  []string
}

func execute(args []string, stdout, stderr io.Writer) exit {
	catalog, err := jev.Catalog()
	if err != nil {
		if _, reportErr := fmt.Fprintf(stderr, "jev-bench: %v\n", err); reportErr != nil {
			return exitIOError
		}
		return exitIOError
	}
	opts, err := parseArgs(args)
	out := output{stdout: stdout, stderr: stderr, texts: catalog, lang: opts.lang}
	if errors.Is(err, flag.ErrHelp) {
		if usageErr := out.usage(); usageErr != nil {
			return out.fail(usageErr)
		}
		return exitSuccess
	}
	if err != nil {
		return out.fail(err)
	}
	result, err := compare(opts.snapshots, opts.settings)
	if err != nil {
		return out.fail(err)
	}
	if err := out.write(result, opts.json); err != nil {
		return out.fail(err)
	}
	return exitSuccess
}

func parseArgs(args []string) (options, error) {
	opts := options{lang: i18n.English}
	flags := flag.NewFlagSet("jev-bench", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	flags.Usage = func() {}
	flags.BoolVar(&opts.json, "json", false, "")
	flags.Func("lang", "", func(value string) error {
		lang, err := i18n.ParseLanguage(value)
		if err != nil {
			return err
		}
		opts.lang = lang
		return nil
	})
	flags.Func("settings", "", func(path string) error {
		opts.settings = append(opts.settings, path)
		return nil
	})
	rest := args
	for {
		if err := flags.Parse(rest); err != nil {
			if errors.Is(err, flag.ErrHelp) {
				return opts, err
			}
			return opts, usageError{cause: err}
		}
		if flags.NArg() == 0 {
			break
		}
		opts.snapshots = append(opts.snapshots, flags.Arg(0))
		rest = flags.Args()[1:]
	}
	if len(opts.snapshots) == 0 {
		return opts, usageError{cause: errNoSnapshot}
	}
	return opts, nil
}
