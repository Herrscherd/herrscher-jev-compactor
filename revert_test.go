package jev

import (
	"strings"
	"testing"

	contracts "github.com/Herrscherd/herrscher-contracts"
)

func TestRevertLeavesTheHistoryWhenThePlanFallsShort(t *testing.T) {
	server := &fakeJev{answers: sessionAnswers()}
	stage := newTestCompactor(t, server, map[string]string{settingOnShort: "revert", settingMinReduction: "0.6"})
	editor := &fakeHistory{history: sessionHistory(), outcome: contracts.PruneOutcome{Edited: 2, CharsBefore: 1000, CharsAfter: 600}}
	result, err := stage.Compact(t.Context(), contracts.CompactRequest{History: editor}, nil)
	if err != nil {
		t.Fatalf("Compact: %v", err)
	}
	if len(editor.pruned) != 0 {
		t.Fatalf("PruneHistory called with %v, want the history left alone", editor.pruned)
	}
	if result.Done || result.Stats.Outcome != contracts.CompactSkipped {
		t.Fatalf("Done = %v, outcome = %v, want not done and skipped", result.Done, result.Stats.Outcome)
	}
	if !strings.Contains(result.Note, "left as it was") {
		t.Fatalf("note = %q, want the reason the history was left alone", result.Note)
	}
}

func TestRevertDoesNotAskJevWhenNoPruningCouldReachTheTarget(t *testing.T) {
	server := &fakeJev{answers: sessionAnswers()}
	stage := newTestCompactor(t, server, map[string]string{settingOnShort: "revert", settingMinReduction: "0.9"})
	editor := &fakeHistory{history: sessionHistory()}
	result, err := stage.Compact(t.Context(), contracts.CompactRequest{History: editor}, nil)
	if err != nil {
		t.Fatalf("Compact: %v", err)
	}
	if got := server.askedQuestions(); len(got) != 0 {
		t.Fatalf("Jev was asked %v, want no question at all", got)
	}
	if len(editor.pruned) != 0 {
		t.Fatalf("PruneHistory called with %v, want the history left alone", editor.pruned)
	}
	if result.Stats.Requests != 0 || result.Done {
		t.Fatalf("requests = %d, done = %v, want no request and not done", result.Stats.Requests, result.Done)
	}
	if !strings.Contains(result.Note, "Jev was not asked") {
		t.Fatalf("note = %q, want the reason Jev was not asked", result.Note)
	}
}

func TestRevertAppliesTheEditsWhenThePlanReachesTheTarget(t *testing.T) {
	server := &fakeJev{answers: sessionAnswers()}
	stage := newTestCompactor(t, server, map[string]string{settingOnShort: "revert", settingMinReduction: "0.3"})
	editor := &fakeHistory{history: sessionHistory(), outcome: contracts.PruneOutcome{Edited: 2, CharsBefore: 1000, CharsAfter: 600}}
	result, err := stage.Compact(t.Context(), contracts.CompactRequest{History: editor}, nil)
	if err != nil {
		t.Fatalf("Compact: %v", err)
	}
	if len(editor.pruned) != 1 {
		t.Fatalf("PruneHistory calls = %d, want 1", len(editor.pruned))
	}
	if !result.Done {
		t.Fatalf("Done = false, want the stack stopped once the target is reached")
	}
}

func TestKeepStillAppliesAShortReduction(t *testing.T) {
	server := &fakeJev{answers: sessionAnswers()}
	stage := newTestCompactor(t, server, map[string]string{settingMinReduction: "0.6"})
	editor := &fakeHistory{history: sessionHistory(), outcome: contracts.PruneOutcome{Edited: 2, CharsBefore: 1000, CharsAfter: 600}}
	result, err := stage.Compact(t.Context(), contracts.CompactRequest{History: editor}, nil)
	if err != nil {
		t.Fatalf("Compact: %v", err)
	}
	if len(editor.pruned) != 1 {
		t.Fatalf("PruneHistory calls = %d, want the default keep to apply the edits", len(editor.pruned))
	}
	if result.Done {
		t.Fatalf("Done = true, want the next stage to run after a short reduction")
	}
}

func TestTokenReductionIsMeasuredOnTheHistoryAfterThePrune(t *testing.T) {
	server := &fakeJev{answers: sessionAnswers()}
	stage := newTestCompactor(t, server, map[string]string{settingReductionUnit: "tokens", settingMinReduction: "0.01"})
	unchanged := sessionHistory()
	editor := &fakeHistory{history: sessionHistory(), afterPrune: &unchanged, outcome: contracts.PruneOutcome{Edited: 2, CharsBefore: 1000, CharsAfter: 600}}
	result, err := stage.Compact(t.Context(), contracts.CompactRequest{History: editor}, nil)
	if err != nil {
		t.Fatalf("Compact: %v", err)
	}
	if len(editor.pruned) != 1 {
		t.Fatalf("PruneHistory calls = %d, want 1", len(editor.pruned))
	}
	if result.Done || !strings.Contains(result.Note, "is 0% smaller") {
		t.Fatalf("done = %v, note = %q, want the real token share of 0%% and not done", result.Done, result.Note)
	}
}

func TestDryRunReportsTheRevertTheRealRunWouldMake(t *testing.T) {
	server := &fakeJev{answers: sessionAnswers()}
	stage := newTestCompactor(t, server, map[string]string{settingDryRun: "true", settingOnShort: "revert", settingMinReduction: "0.6"})
	editor := &fakeHistory{history: sessionHistory()}
	result, err := stage.Compact(t.Context(), contracts.CompactRequest{History: editor}, nil)
	if err != nil {
		t.Fatalf("Compact: %v", err)
	}
	if result.Stats.Outcome != contracts.CompactSkipped || !strings.Contains(result.Note, "left as it was") {
		t.Fatalf("outcome = %v, note = %q, want the revert reported by the dry run", result.Stats.Outcome, result.Note)
	}
}
