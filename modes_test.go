package jev

import (
	"errors"
	"testing"

	contracts "github.com/Herrscherd/herrscher-contracts"
)

func TestParseOptionsRefusesBadModes(t *testing.T) {
	cases := map[string]string{
		settingMaxParallel: "0",
		settingOnShort:     "revert",
		settingDryRun:      "maybe",
	}
	for key, value := range cases {
		_, err := parseOptions(contracts.PluginConfig{Settings: map[string]string{settingAPIKey: "key", key: value}})
		if !errors.Is(err, ErrInvalidSetting) {
			t.Errorf("parseOptions(%s=%q) error = %v, want ErrInvalidSetting", key, value, err)
		}
	}
}

func TestParseOptionsReadsTheModes(t *testing.T) {
	opts, err := parseOptions(contracts.PluginConfig{Settings: map[string]string{
		settingAPIKey: "key", settingMaxParallel: "2", settingOnShort: "done", settingDryRun: "true",
	}})
	if err != nil {
		t.Fatalf("parseOptions: %v", err)
	}
	if opts.parallelism() != 2 || opts.shortRule() != onShortDone || !opts.dryRun {
		t.Fatalf("parseOptions = %+v, want parallel 2, on_short done, dry run", opts)
	}
}

func TestOptionsModesFallBackToTheDefaults(t *testing.T) {
	var opts options
	if opts.parallelism() != defaultMaxParallel || opts.shortRule() != onShortKeep {
		t.Fatalf("zero options = parallel %d, on_short %v, want the defaults", opts.parallelism(), opts.shortRule())
	}
}

func TestCompactDryRunPlansWithoutPruning(t *testing.T) {
	stage := newTestCompactor(t, &fakeJev{answers: sessionAnswers()}, map[string]string{settingDryRun: "true"})
	editor := &fakeHistory{history: sessionHistory(), outcome: contracts.PruneOutcome{Edited: 2, CharsBefore: 1000, CharsAfter: 600}}
	result, err := stage.Compact(t.Context(), contracts.CompactRequest{History: editor}, nil)
	if err != nil {
		t.Fatalf("Compact: %v", err)
	}
	if len(editor.pruned) != 0 {
		t.Fatalf("dry run pruned %+v, want nothing", editor.pruned)
	}
	if result.Done || result.Note != "Jev would prune 2 tool calls, dry run: the history is unchanged" {
		t.Fatalf("result = %+v, want the next stage and the dry run note", result)
	}
}

func TestCompactOnShortDoneStopsTheStack(t *testing.T) {
	stage := newTestCompactor(t, &fakeJev{answers: sessionAnswers()}, map[string]string{settingOnShort: "done"})
	editor := &fakeHistory{history: sessionHistory(), outcome: contracts.PruneOutcome{Edited: 1, CharsBefore: 1000, CharsAfter: 900}}
	result, err := stage.Compact(t.Context(), contracts.CompactRequest{History: editor}, nil)
	if err != nil {
		t.Fatalf("Compact: %v", err)
	}
	if !result.Done || result.Note != "Jev reviewed 5 tool calls: 3 kept, 1 truncated, 1 removed, the history is 9% smaller, short of the 25% aimed for, so the stack stops here" {
		t.Fatalf("result = %+v, want the stack to stop", result)
	}
}
