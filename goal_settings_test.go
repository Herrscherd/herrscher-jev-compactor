package jev

import (
	"errors"
	"testing"

	contracts "github.com/Herrscherd/herrscher-contracts"
)

func parseGoalSettings(t *testing.T, pairs map[string]string) (options, error) {
	t.Helper()
	settings := map[string]string{settingAPIKey: "key"}
	for key, value := range pairs {
		settings[key] = value
	}
	return parseOptions(contracts.PluginConfig{Settings: settings})
}

func goalHistory() contracts.History {
	return contracts.History{Messages: []contracts.HistoryMessage{
		{Role: contracts.RoleUser, Text: "one"},
		{Role: contracts.RoleAssistant, Text: "reply"},
		{Role: contracts.RoleUser, Text: "two"},
		{Role: contracts.RoleUser, Text: "three"},
		{Role: contracts.RoleUser, Text: "four"},
	}}
}

func TestGoalSourceIsRead(t *testing.T) {
	cases := []struct {
		value string
		want  goalSource
	}{
		{"", goalSourceAuto},
		{"auto", goalSourceAuto},
		{"prompts", goalSourcePrompts},
		{"fixed", goalSourceFixed},
	}
	for _, tc := range cases {
		opts, err := parseGoalSettings(t, map[string]string{settingGoalSource: tc.value, settingGoalText: "ship it"})
		if err != nil {
			t.Fatalf("goal_source=%q: %v", tc.value, err)
		}
		if got := opts.goalSettings().source; got != tc.want {
			t.Errorf("goal_source=%q reads %v, want %v", tc.value, got, tc.want)
		}
	}
}

func TestGoalSettingsRefuseBadValues(t *testing.T) {
	cases := []map[string]string{
		{settingGoalSource: "everything"},
		{settingGoalSource: "fixed"},
		{settingGoalSource: "fixed", settingGoalText: "   "},
		{settingGoalPrompts: "0"},
		{settingGoalPrompts: "many"},
		{settingGoalPromptRunes: "-1"},
		{settingGoalPromptRunes: "0"},
	}
	for _, pairs := range cases {
		_, err := parseGoalSettings(t, pairs)
		if !errors.Is(err, ErrInvalidSetting) {
			t.Errorf("parseOptions(%v) error = %v, want ErrInvalidSetting", pairs, err)
		}
	}
}

func TestGoalSettingsKeepTheDefaults(t *testing.T) {
	opts, err := parseGoalSettings(t, nil)
	if err != nil {
		t.Fatalf("parseOptions: %v", err)
	}
	want := goalSpec{source: goalSourceAuto, prompts: defaultGoalPrompts, promptRunes: defaultGoalPromptRunes}
	if got := opts.goalSettings(); got != want {
		t.Errorf("goalSettings() = %+v, want %+v", got, want)
	}
	if got := defaultGoalSpec(); got != want {
		t.Errorf("defaultGoalSpec() = %+v, want %+v", got, want)
	}
}

func TestGoalFollowsItsSource(t *testing.T) {
	cases := []struct {
		name        string
		pairs       map[string]string
		instruction string
		want        string
	}{
		{"auto takes the instruction", nil, "  ship it  ", "ship it"},
		{"auto falls back to the prompts", nil, " ", "two\nthree\nfour"},
		{"prompts ignore the instruction", map[string]string{settingGoalSource: "prompts"}, "ship it", "two\nthree\nfour"},
		{"fixed ignores everything", map[string]string{settingGoalSource: "fixed", settingGoalText: " reply fast "}, "ship it", "reply fast"},
		{"fewer prompts", map[string]string{settingGoalSource: "prompts", settingGoalPrompts: "2"}, "", "three\nfour"},
		{"more prompts than there are", map[string]string{settingGoalSource: "prompts", settingGoalPrompts: "9"}, "", "one\ntwo\nthree\nfour"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			opts, err := parseGoalSettings(t, tc.pairs)
			if err != nil {
				t.Fatalf("parseOptions: %v", err)
			}
			if got := opts.goalSettings().goal(goalHistory(), tc.instruction); got != tc.want {
				t.Errorf("goal = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestGoalPromptRunesCutEachPrompt(t *testing.T) {
	history := contracts.History{Messages: []contracts.HistoryMessage{
		{Role: contracts.RoleUser, Text: "abcdefgh"},
	}}
	opts, err := parseGoalSettings(t, map[string]string{settingGoalSource: "prompts", settingGoalPromptRunes: "4"})
	if err != nil {
		t.Fatalf("parseOptions: %v", err)
	}
	if got, want := opts.goalSettings().goal(history, ""), truncate("abcdefgh", 4); got != want {
		t.Errorf("goal = %q, want %q", got, want)
	}
}
