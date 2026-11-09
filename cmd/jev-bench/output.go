package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"strings"
	"text/tabwriter"

	"github.com/Herrscherd/herrscher-contracts/i18n"
)

const (
	percentScale = 100
	tablePadding = 2
)

const (
	usageLine        = "jev.bench.usage.line"
	flagLang         = "jev.bench.flag.lang"
	flagJSON         = "jev.bench.flag.json"
	flagSettings     = "jev.bench.flag.settings"
	errorPrefix      = "jev.bench.error.prefix"
	errorUsage       = "jev.bench.error.usage"
	errorNoSnapshot  = "jev.bench.error.no_snapshot"
	errorSettingsLn  = "jev.bench.error.settings_line"
	errorNotRegular  = "jev.bench.error.not_regular"
	errorTooLarge    = "jev.bench.error.too_large"
	errorFile        = "jev.bench.error.file"
	tableSnapshots   = "jev.bench.table.snapshots"
	columnRecorded   = "jev.bench.column.recorded"
	columnSetting    = "jev.bench.column.setting"
	columnKept       = "jev.bench.column.kept"
	columnTruncated  = "jev.bench.column.truncated"
	columnPruned     = "jev.bench.column.pruned"
	columnUnanswered = "jev.bench.column.unanswered"
	columnCached     = "jev.bench.column.cached"
	columnReverted   = "jev.bench.column.reverted"
	columnChars      = "jev.bench.column.chars"
	columnEstimated  = "jev.bench.column.tokens"
	paramPath        = "Path"
)

type output struct {
	stdout io.Writer
	stderr io.Writer
	texts  *i18n.Catalog
	lang   i18n.Language
}

func (o output) text(key string, params map[string]any) string {
	message, err := o.texts.Message(o.lang, key, params)
	if err != nil {
		slog.Warn("jev-bench: render message", "key", key, "err", err)
		return key
	}
	return message
}

func (o output) write(result comparison, asJSON bool) error {
	if asJSON {
		encoder := json.NewEncoder(o.stdout)
		encoder.SetIndent("", "  ")
		return encoder.Encode(result)
	}
	return o.table(result)
}

func (o output) fail(err error) exit {
	if _, reportErr := fmt.Fprintln(o.stderr, o.text(errorPrefix, map[string]any{"Error": o.describe(err)})); reportErr != nil {
		return exitIOError
	}
	return exitOf(err)
}

func (o output) usage() error {
	_, err := fmt.Fprintf(o.stderr, "%s\n%s\n%s\n%s\n",
		o.text(usageLine, nil), o.text(flagLang, nil), o.text(flagJSON, nil), o.text(flagSettings, nil))
	return err
}

func (o output) describe(err error) string {
	if errors.Is(err, errNoSnapshot) {
		return o.text(errorNoSnapshot, nil)
	}
	if _, ok := errors.AsType[usageError](err); ok {
		return o.text(errorUsage, map[string]any{"Cause": err.Error()})
	}
	if line, ok := errors.AsType[lineError](err); ok {
		return o.text(errorSettingsLn, map[string]any{paramPath: line.path, "Line": line.line})
	}
	if file, ok := errors.AsType[fileError](err); ok {
		return o.describeFile(file)
	}
	if pathErr, ok := errors.AsType[*fs.PathError](err); ok {
		return pathErr.Err.Error()
	}
	return err.Error()
}

func (o output) describeFile(file fileError) string {
	switch {
	case errors.Is(file.kind, errNotRegular):
		return o.text(errorNotRegular, map[string]any{paramPath: file.path})
	case errors.Is(file.kind, errSettingsTooLarge):
		return o.text(errorTooLarge, map[string]any{paramPath: file.path, "Limit": maxSettingsBytes})
	}
	return o.text(errorFile, map[string]any{paramPath: file.path, "Error": o.describe(file.kind)})
}

func (o output) table(result comparison) error {
	writer := tabwriter.NewWriter(o.stdout, 0, 0, tablePadding, ' ', 0)
	if _, err := fmt.Fprintln(writer, o.text(tableSnapshots, map[string]any{"Snapshots": result.Snapshots})); err != nil {
		return err
	}
	if _, err := fmt.Fprintln(writer, o.header()); err != nil {
		return err
	}
	for _, row := range result.Settings {
		if _, err := fmt.Fprintf(writer, "%s\t%d\t%d\t%d\t%d\t%d\t%d\t%s\t%s\n",
			o.label(row.Setting), row.Kept, row.Truncated, row.Pruned, row.Unanswered, row.Cached, row.Reverted,
			percent(row.CharsReduction), percent(row.TokensReduction)); err != nil {
			return err
		}
	}
	return writer.Flush()
}

func (o output) header() string {
	names := []string{
		columnSetting, columnKept, columnTruncated, columnPruned,
		columnUnanswered, columnCached, columnReverted, columnChars, columnEstimated,
	}
	cells := make([]string, 0, len(names))
	for _, name := range names {
		cells = append(cells, o.text(name, nil))
	}
	return strings.Join(cells, "\t")
}

func (o output) label(setting string) string {
	if setting == recordedName {
		return o.text(columnRecorded, nil)
	}
	return setting
}

func percent(ratio float64) string {
	return fmt.Sprintf("%.1f%%", ratio*percentScale)
}
