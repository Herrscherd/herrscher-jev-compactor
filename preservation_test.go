package jev

import (
	"errors"
	"slices"
	"testing"

	contracts "github.com/Herrscherd/herrscher-contracts"
)

func preservedHistory() []contracts.HistoryMessage {
	return []contracts.HistoryMessage{
		{Role: contracts.RoleUser, Text: "start"},
		{Role: contracts.RoleAssistant, Calls: []contracts.HistoryCall{{Tool: "read", Input: `{"path":"a"}`, Result: "alpha"}}},
		{Role: contracts.RoleUser, Text: "next"},
		{Role: contracts.RoleAssistant, Calls: []contracts.HistoryCall{{Tool: "read", Input: `{"path":"b"}`, Result: "beta"}}},
		{Role: contracts.RoleUser, Text: "last"},
		{Role: contracts.RoleAssistant, Text: "done"},
	}
}

func TestPinnedMessagesFollowThePreservation(t *testing.T) {
	messages := preservedHistory()
	lastTwo := messageTokens(messages[5]) + messageTokens(messages[4])
	cases := []struct {
		name string
		keep preservation
		want []bool
	}{
		{"first only", preservation{first: true}, []bool{true, false, false, false, false, false}},
		{"nothing", preservation{}, []bool{false, false, false, false, false, false}},
		{"two recent messages", preservation{recent: 2}, []bool{false, false, false, false, true, true}},
		{"first and two recent", preservation{first: true, recent: 2}, []bool{true, false, false, false, true, true}},
		{"after the last user message", preservation{turns: 1}, []bool{false, false, false, false, false, true}},
		{"after the second last user message", preservation{turns: 2}, []bool{false, false, false, true, true, true}},
		{"after the third last user message", preservation{turns: 3}, []bool{false, true, true, true, true, true}},
		{"more turns than user messages", preservation{turns: 9}, []bool{false, false, false, false, false, false}},
		{"token budget of the last two", preservation{recentTokens: lastTwo}, []bool{false, false, false, false, true, true}},
		{"token budget one short", preservation{recentTokens: lastTwo - 1}, []bool{false, false, false, false, false, true}},
		{"token budget and first", preservation{first: true, recentTokens: lastTwo}, []bool{true, false, false, false, true, true}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.keep.pinnedMessages(messages); !slices.Equal(got, tc.want) {
				t.Errorf("pinnedMessages() = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestCollectCallsWithMarksThePinnedCalls(t *testing.T) {
	history := contracts.History{Messages: preservedHistory()}
	calls := collectCallsWith(history, preservation{turns: 2})
	if len(calls) != 2 {
		t.Fatalf("collectCallsWith returned %d calls, want 2", len(calls))
	}
	if calls[0].pinned || !calls[1].pinned {
		t.Errorf("pinned flags = %v, %v, want false, true", calls[0].pinned, calls[1].pinned)
	}
}

func TestPreservationSettingsAreRead(t *testing.T) {
	cases := []struct {
		name   string
		pairs  map[string]string
		first  bool
		tokens int
		turns  int
	}{
		{"defaults", nil, true, 0, 0},
		{"first off", map[string]string{settingPreserveFirst: "false"}, false, 0, 0},
		{"first on", map[string]string{settingPreserveFirst: "true"}, true, 0, 0},
		{"tokens and turns", map[string]string{settingPreserveRecentBudget: "500", settingPreserveTurns: "2"}, true, 500, 2},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			opts, err := parseGoalSettings(t, tc.pairs)
			if err != nil {
				t.Fatalf("parseOptions: %v", err)
			}
			keep := opts.preservation()
			if keep.first != tc.first || keep.recentTokens != tc.tokens || keep.turns != tc.turns {
				t.Errorf("preservation() = %+v, want first %v, tokens %d, turns %d", keep, tc.first, tc.tokens, tc.turns)
			}
		})
	}
}

func TestPreservationSettingsRefuseBadValues(t *testing.T) {
	cases := []map[string]string{
		{settingPreserveFirst: "maybe"},
		{settingPreserveRecentBudget: "-1"},
		{settingPreserveRecentBudget: "lots"},
		{settingPreserveTurns: "-2"},
		{settingPreserveTurns: "two"},
	}
	for _, pairs := range cases {
		_, err := parseGoalSettings(t, pairs)
		if !errors.Is(err, ErrInvalidSetting) {
			t.Errorf("parseOptions(%v) error = %v, want ErrInvalidSetting", pairs, err)
		}
	}
}
