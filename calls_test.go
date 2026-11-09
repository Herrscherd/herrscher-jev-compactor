package jev

import (
	"strings"
	"testing"

	contracts "github.com/Herrscherd/herrscher-contracts"
)

func TestCollectCallsSkipsCallsWithoutAResult(t *testing.T) {
	history := contracts.History{Messages: []contracts.HistoryMessage{
		{Role: contracts.RoleAssistant, Calls: []contracts.HistoryCall{
			{ID: "toolu_pending", Tool: "Bash", Input: `{}`},
			{ID: "toolu_done", Tool: "Read", Input: `{}`, Result: "body"},
		}},
	}}
	calls := collectCalls(history, 0)
	if len(calls) != 1 || calls[0].source.ID != "toolu_done" || calls[0].ref != "t1" {
		t.Fatalf("calls = %+v, want only the answered call, as t1", calls)
	}
}

func TestCollectCallsKeepsEmptyResultsOnlyWhenAsked(t *testing.T) {
	history := contracts.History{Messages: []contracts.HistoryMessage{
		{Role: contracts.RoleAssistant, Calls: []contracts.HistoryCall{
			{ID: "toolu_running", Tool: "Bash", Input: `{}`},
			{ID: "toolu_done", Tool: "Read", Input: `{}`, Result: "body"},
		}},
	}}
	if calls := collectCallsWith(history, preservation{}, false); len(calls) != 1 {
		t.Fatalf("without include_empty_results, calls = %+v, want only the answered call", calls)
	}
	calls := collectCallsWith(history, preservation{}, true)
	if len(calls) != 2 || calls[0].source.ID != "toolu_running" || calls[0].ref != "t1" {
		t.Fatalf("with include_empty_results, calls = %+v, want both calls, the empty one as t1", calls)
	}
}

func TestIncludeEmptyResultsLetsJevJudgeAnEmptyCall(t *testing.T) {
	history := contracts.History{Messages: []contracts.HistoryMessage{
		{Role: contracts.RoleUser},
		{Role: contracts.RoleAssistant, Calls: []contracts.HistoryCall{{ID: "toolu_empty", Tool: "Bash", Input: `{}`}}},
	}}
	edits, _ := planWith(t, history, map[string]string{settingRules: "Bash:prune", settingPreserveRecent: "0"})
	if _, pruned := editOf(edits, "toolu_empty"); pruned {
		t.Fatalf("an empty call was judged without include_empty_results: %+v", edits)
	}
	edits, _ = planWith(t, history, map[string]string{settingRules: "Bash:prune", settingPreserveRecent: "0", settingIncludeEmpty: "true"})
	if _, pruned := editOf(edits, "toolu_empty"); !pruned {
		t.Fatalf("an empty call was not judged with include_empty_results: %+v", edits)
	}
}

func TestCompactCallLineCutsNestedValuesAsTheirOwnObject(t *testing.T) {
	nested := `{"edits":[` + strings.Repeat(`"x",`, 100) + `"y"]}`
	call := toolCall{ref: "t1", source: contracts.HistoryCall{Tool: "Edit", Input: nested, Result: "ok"}}
	want := "t1 Edit edits=" + truncate(`{"edits":[`+strings.Repeat(`"x",`, 100), 60-len("edits=")) + " → ok 2ch"
	if got := compactCallLine(call, 60); got != want {
		t.Fatalf("compactCallLine = %q, want %q", got, want)
	}
}
