package jev

import (
	"errors"
	"strings"
	"testing"

	contracts "github.com/Herrscherd/herrscher-contracts"
)

func testOptions(maxStateTokens int) options {
	return options{preserveRecent: 1, maxStateTokens: maxStateTokens, maxRequestTokens: defaultMaxRequestTokens}
}

func chattyHistory() contracts.History {
	var messages []contracts.HistoryMessage
	for index := range 12 {
		messages = append(messages,
			contracts.HistoryMessage{Role: contracts.RoleUser, Text: strings.Repeat("please look at this ", 60)},
			contracts.HistoryMessage{Role: contracts.RoleAssistant, Calls: []contracts.HistoryCall{
				{ID: "toolu_" + string(rune('a'+index)), Tool: "Read", Input: `{"file_path":"` + strings.Repeat("dir/", 80) + `x.go"}`, Result: "body"},
			}},
		)
	}
	return contracts.History{Messages: messages}
}

func fitAt(t *testing.T, history contracts.History, maxTokens int) (fittedState, error) {
	t.Helper()
	return fitState(history, collectCalls(history, 1), "goal", testOptions(maxTokens))
}

func TestFitStateSendsTheWholeHistoryWhenItFits(t *testing.T) {
	history := chattyHistory()
	fitted, err := fitAt(t, history, defaultMaxStateTokens)
	if err != nil {
		t.Fatalf("fitState: %v", err)
	}
	if fitted.stage != "inputs<=1000" || len(fitted.state.History) != len(history.Messages) {
		t.Fatalf("stage %q with %d entries, want the full history", fitted.stage, len(fitted.state.History))
	}
	call, isCall := fitted.state.History[1].ToolCalls[0].(stateCall)
	if !isCall || call.Result != "ok, 4 chars (omitted)" || call.ID != "t1" {
		t.Fatalf("first call = %+v, want its result replaced by a note", fitted.state.History[1].ToolCalls[0])
	}
}

func TestFitStateShrinksStageByStageUntilItFits(t *testing.T) {
	history := chattyHistory()
	full, err := fitAt(t, history, defaultMaxStateTokens)
	if err != nil {
		t.Fatal(err)
	}
	seen := make(map[string]bool)
	for budget := full.tokens; budget > 0; budget -= 25 {
		fitted, err := fitAt(t, history, budget)
		if errors.Is(err, ErrHistoryTooLarge) {
			break
		}
		if err != nil {
			t.Fatalf("fitState(%d): %v", budget, err)
		}
		if fitted.tokens > budget {
			t.Fatalf("stage %q used %d tokens over a budget of %d", fitted.stage, fitted.tokens, budget)
		}
		seen[fitted.stage] = true
	}
	for _, stage := range []string{"inputs<=1000", "inputs<=200", "inputs<=60", "texts abridged", "old messages collapsed", "old calls compacted", "old messages left out", "old calls merged"} {
		if !seen[stage] {
			t.Errorf("stage %q never reached, saw %v", stage, seen)
		}
	}
}

func TestFitStateRefusesAHistoryThatCannotShrinkEnough(t *testing.T) {
	if _, err := fitAt(t, chattyHistory(), 10); !errors.Is(err, ErrHistoryTooLarge) {
		t.Fatalf("fitState error = %v, want ErrHistoryTooLarge", err)
	}
}

func TestGoalTakesTheLastThreePrompts(t *testing.T) {
	history := contracts.History{Messages: []contracts.HistoryMessage{
		{Role: contracts.RoleUser, Text: "one"},
		{Role: contracts.RoleAssistant, Text: "reply"},
		{Role: contracts.RoleUser, Text: "two"},
		{Role: contracts.RoleUser, Text: "  "},
		{Role: contracts.RoleUser, Text: "three"},
		{Role: contracts.RoleUser, Text: "four"},
	}}
	if goal := goalOf(history, ""); goal != "two\nthree\nfour" {
		t.Fatalf("goal = %q", goal)
	}
}
