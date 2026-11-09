package jev

import (
	"errors"
	"testing"

	contracts "github.com/Herrscherd/herrscher-contracts"
)

func TestReductionUnitDefaultsToCharacters(t *testing.T) {
	opts, err := parseOptions(contracts.PluginConfig{Settings: map[string]string{settingAPIKey: "key"}})
	if err != nil {
		t.Fatalf("parseOptions: %v", err)
	}
	if opts.measureUnit() != unitCharacters {
		t.Fatalf("measureUnit = %v, want characters", opts.measureUnit())
	}
	var zero options
	if zero.measureUnit() != unitCharacters {
		t.Fatalf("zero options measureUnit = %v, want characters", zero.measureUnit())
	}
}

func TestReductionUnitReadsTheTwoUnitsAndRefusesOthers(t *testing.T) {
	opts, err := parseOptions(contracts.PluginConfig{Settings: map[string]string{settingAPIKey: "key", settingReductionUnit: "tokens"}})
	if err != nil {
		t.Fatalf("parseOptions: %v", err)
	}
	if opts.measureUnit() != unitEstimated {
		t.Fatalf("measureUnit = %v, want estimated", opts.measureUnit())
	}
	_, err = parseOptions(contracts.PluginConfig{Settings: map[string]string{settingAPIKey: "key", settingReductionUnit: "words"}})
	if !errors.Is(err, ErrInvalidSetting) {
		t.Fatalf("parseOptions(reduction_unit=words) error = %v, want ErrInvalidSetting", err)
	}
}

func TestReductionUnitTokensMeasuresTheTargetInTokens(t *testing.T) {
	outcome := contracts.PruneOutcome{Edited: 2, CharsBefore: 1000, CharsAfter: 0}
	history := sessionHistory()

	chars := newTestCompactor(t, &fakeJev{answers: sessionAnswers()}, map[string]string{settingMinReduction: "0.9"})
	charEditor := &fakeHistory{history: history, outcome: outcome}
	charResult, err := chars.Compact(t.Context(), contracts.CompactRequest{History: charEditor}, nil)
	if err != nil {
		t.Fatalf("Compact in characters: %v", err)
	}
	if !charResult.Done {
		t.Fatalf("characters: Done = false, want the outcome's characters to reach 0.9")
	}

	tokens := newTestCompactor(t, &fakeJev{answers: sessionAnswers()}, map[string]string{settingMinReduction: "0.9", settingReductionUnit: "tokens"})
	tokenEditor := &fakeHistory{history: history, outcome: outcome}
	tokenResult, err := tokens.Compact(t.Context(), contracts.CompactRequest{History: tokenEditor}, nil)
	if err != nil {
		t.Fatalf("Compact in tokens: %v", err)
	}
	if tokenResult.Done {
		t.Fatalf("tokens: Done = true, want the plan's share in tokens to fall short of 0.9")
	}
	if len(tokenEditor.pruned) != 1 {
		t.Fatalf("tokens: PruneHistory calls = %d, want the edits applied under keep", len(tokenEditor.pruned))
	}
}

func TestReductionUnitTokensReachesATargetItsPlanMeets(t *testing.T) {
	stage := newTestCompactor(t, &fakeJev{answers: sessionAnswers()}, map[string]string{settingMinReduction: "0.2", settingReductionUnit: "tokens"})
	editor := &fakeHistory{history: sessionHistory(), outcome: contracts.PruneOutcome{Edited: 2, CharsBefore: 1000, CharsAfter: 600}}
	result, err := stage.Compact(t.Context(), contracts.CompactRequest{History: editor}, nil)
	if err != nil {
		t.Fatalf("Compact: %v", err)
	}
	if !result.Done {
		t.Fatalf("Done = false, want the plan's token share to reach 0.2")
	}
}

func TestRevertInTokensLeavesTheHistoryWhenNoPruningCouldReachTheTarget(t *testing.T) {
	server := &fakeJev{answers: sessionAnswers()}
	stage := newTestCompactor(t, server, map[string]string{settingOnShort: "revert", settingMinReduction: "0.9", settingReductionUnit: "tokens"})
	editor := &fakeHistory{history: sessionHistory()}
	result, err := stage.Compact(t.Context(), contracts.CompactRequest{History: editor}, nil)
	if err != nil {
		t.Fatalf("Compact: %v", err)
	}
	if len(editor.pruned) != 0 || result.Done {
		t.Fatalf("pruned = %d, done = %v, want the history left alone in tokens", len(editor.pruned), result.Done)
	}
	if got := server.askedQuestions(); len(got) != 0 {
		t.Fatalf("Jev was asked %v, want no question", got)
	}
}
