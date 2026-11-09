package main

import (
	"encoding/json"
	"fmt"
	"io"
	"text/tabwriter"
)

const (
	percentScale = 100
	tablePadding = 2
)

func write(out io.Writer, result comparison, asJSON bool) error {
	if asJSON {
		encoder := json.NewEncoder(out)
		encoder.SetIndent("", "  ")
		return encoder.Encode(result)
	}
	return writeTable(out, result)
}

func report(stderr io.Writer, err error) error {
	_, writeErr := fmt.Fprintf(stderr, "jev-bench: %v\n", err)
	return writeErr
}

func writeTable(out io.Writer, result comparison) error {
	writer := tabwriter.NewWriter(out, 0, 0, tablePadding, ' ', 0)
	if _, err := fmt.Fprintf(writer, "snapshots: %d\n", result.Snapshots); err != nil {
		return err
	}
	if _, err := fmt.Fprintln(writer, "SETTING\tKEPT\tTRUNCATED\tPRUNED\tUNANSWERED\tREVERTED\tCHARS\tTOKENS"); err != nil {
		return err
	}
	for _, row := range result.Settings {
		if _, err := fmt.Fprintf(writer, "%s\t%d\t%d\t%d\t%d\t%d\t%s\t%s\n",
			row.Setting, row.Kept, row.Truncated, row.Pruned, row.Unanswered, row.Reverted,
			percent(row.CharsReduction), percent(row.TokensReduction)); err != nil {
			return err
		}
	}
	return writer.Flush()
}

func percent(ratio float64) string {
	return fmt.Sprintf("%.1f%%", ratio*percentScale)
}
