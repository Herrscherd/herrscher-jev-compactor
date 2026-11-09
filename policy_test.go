package jev

import (
	"slices"
	"testing"

	contracts "github.com/Herrscherd/herrscher-contracts"
)

func planWith(t *testing.T, history contracts.History, settings map[string]string) ([]contracts.HistoryEdit, *fakeJev) {
	t.Helper()
	server := &fakeJev{answers: sessionAnswers()}
	stage := newTestCompactor(t, server, settings)
	edits, err := stage.plan(t.Context(), history, "")
	if err != nil {
		t.Fatalf("plan: %v", err)
	}
	return edits, server
}

func editOf(edits []contracts.HistoryEdit, callID string) (contracts.HistoryEdit, bool) {
	for _, edit := range edits {
		if edit.CallID == callID {
			return edit, true
		}
	}
	return contracts.HistoryEdit{}, false
}

func failedGrep() contracts.History {
	history := sessionHistory()
	history.Messages[1].Calls[2].ResultIsError = true
	return history
}

func TestPinnedInputsAreKeptWithoutAskingJev(t *testing.T) {
	edits, server := planWith(t, sessionHistory(), map[string]string{settingPinInputs: "parser.go"})
	if slices.Contains(server.askedQuestions(), "call_t1") || slices.Contains(server.askedQuestions(), "result_t1") {
		t.Fatalf("a pinned call was asked about: %v", server.askedQuestions())
	}
	if _, pruned := editOf(edits, "toolu_kept"); pruned {
		t.Fatalf("a pinned call was pruned: %+v", edits)
	}
	if _, pruned := editOf(edits, "toolu_head"); !pruned {
		t.Fatalf("the unpinned call was not judged: %+v", edits)
	}
}

func TestRulesDecideLocallyWithoutAskingJev(t *testing.T) {
	cases := []struct {
		name     string
		rules    string
		notAsked []string
		want     []contracts.HistoryEdit
	}{
		{
			name:     "keep skips the call",
			rules:    "Grep:keep",
			notAsked: []string{"call_t3", "result_t3"},
			want: []contracts.HistoryEdit{
				{CallID: "toolu_head", Action: contracts.PruneResult, KeepChars: 300},
			},
		},
		{
			name:     "prune removes the call",
			rules:    "Grep:prune",
			notAsked: []string{"call_t3", "result_t3"},
			want: []contracts.HistoryEdit{
				{CallID: "toolu_head", Action: contracts.PruneResult, KeepChars: 300},
				{CallID: "toolu_gone", Action: contracts.PruneCall},
			},
		},
		{
			name:     "truncate uses the rule head",
			rules:    "Bash:truncate,head=50",
			notAsked: []string{"call_t2", "result_t2", "call_t4", "result_t4"},
			want: []contracts.HistoryEdit{
				{CallID: "toolu_head", Action: contracts.PruneResult, KeepChars: 50},
				{CallID: "toolu_gone", Action: contracts.PruneCall},
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			edits, server := planWith(t, sessionHistory(), map[string]string{settingRules: tc.rules})
			for _, name := range tc.notAsked {
				if slices.Contains(server.askedQuestions(), name) {
					t.Fatalf("%s was asked to Jev: %v", name, server.askedQuestions())
				}
			}
			if !slices.Equal(edits, tc.want) {
				t.Fatalf("plan = %+v, want %+v", edits, tc.want)
			}
		})
	}
}

func TestKeepErrorsTurnsAPrunedCallIntoAPrunedResult(t *testing.T) {
	cases := []struct {
		name       string
		keepErrors string
		want       contracts.HistoryEdit
	}{
		{"off", "false", contracts.HistoryEdit{CallID: "toolu_gone", Action: contracts.PruneCall}},
		{"on", "true", contracts.HistoryEdit{CallID: "toolu_gone", Action: contracts.PruneResult, KeepChars: 300}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			edits, _ := planWith(t, failedGrep(), map[string]string{settingRules: "Grep:prune", settingKeepErrors: tc.keepErrors})
			got, found := editOf(edits, "toolu_gone")
			if !found || got != tc.want {
				t.Fatalf("edit for the failed call = %+v, %v, want %+v", got, found, tc.want)
			}
		})
	}
}

func TestMinResultCharsKeepsShortResultsWithoutAskingJev(t *testing.T) {
	edits, server := planWith(t, sessionHistory(), map[string]string{settingMinResultChars: "1600"})
	if len(edits) != 0 || len(server.askedQuestions()) != 0 {
		t.Fatalf("plan = %+v, asked %v, want nothing", edits, server.askedQuestions())
	}
}

func TestThresholdsFallBackToKeepThreshold(t *testing.T) {
	cases := []struct {
		name     string
		settings map[string]string
		want     bool
		kind     contracts.PruneAction
	}{
		{
			name:     "keep threshold decides both",
			settings: map[string]string{settingKeepThreshold: "0.95"},
			want:     true,
			kind:     contracts.PruneCall,
		},
		{
			name:     "explicit call threshold overrides the fallback",
			settings: map[string]string{settingKeepThreshold: "0.95", settingKeepCallThreshold: "0.5"},
			want:     true,
			kind:     contracts.PruneResult,
		},
		{
			name:     "explicit result threshold keeps the full result",
			settings: map[string]string{settingKeepThreshold: "0.95", settingKeepResultThreshold: "0.2"},
			want:     false,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			edits, _ := planWith(t, sessionHistory(), tc.settings)
			got, found := editOf(edits, "toolu_head")
			if found != tc.want || (found && got.Action != tc.kind) {
				t.Fatalf("edit for toolu_head = %+v, %v, want found %v with %v", got, found, tc.want, tc.kind)
			}
		})
	}
}
