package jev

import (
	"errors"
	"slices"
	"strings"
	"testing"

	contracts "github.com/Herrscherd/herrscher-contracts"
)

func parseSetting(key, value string) (options, error) {
	return parseOptions(contracts.PluginConfig{Settings: map[string]string{settingAPIKey: "key", key: value}})
}

func TestStateInputRunesAreRead(t *testing.T) {
	cases := []struct {
		value string
		want  inputLadder
	}{
		{"", defaultInputLadder()},
		{"1000,200,60", inputLadder{full: 1000, short: 200, line: 60}},
		{"500,100,30", inputLadder{full: 500, short: 100, line: 30}},
		{"300,300,300", inputLadder{full: 300, short: 300, line: 300}},
	}
	for _, tc := range cases {
		opts, err := parseSetting(settingStateInputRunes, tc.value)
		if err != nil {
			t.Fatalf("parseSetting(%q): %v", tc.value, err)
		}
		if got := opts.stateLadder(); got != tc.want {
			t.Errorf("stateLadder(%q) = %+v, want %+v", tc.value, got, tc.want)
		}
	}
}

func TestStateTextLengthsAreRead(t *testing.T) {
	cases := []struct {
		key   string
		value string
		read  func(options) int
		want  int
	}{
		{settingStateTextHead, "", options.stateHead, defaultTextHeadRunes},
		{settingStateTextHead, "50", options.stateHead, 50},
		{settingStateTextTail, "", options.stateTail, defaultTextTailRunes},
		{settingStateTextTail, "20", options.stateTail, 20},
	}
	for _, tc := range cases {
		opts, err := parseSetting(tc.key, tc.value)
		if err != nil {
			t.Fatalf("parseSetting(%s=%q): %v", tc.key, tc.value, err)
		}
		if got := tc.read(opts); got != tc.want {
			t.Errorf("%s=%q reads %d, want %d", tc.key, tc.value, got, tc.want)
		}
	}
}

func TestStateStagesAreRead(t *testing.T) {
	cases := []struct {
		value string
		want  []stageKind
	}{
		{"", stageKinds()},
		{"leave_out,merge_calls", []stageKind{stageLeaveOut, stageMerge}},
		{"merge_calls,abridge_texts", []stageKind{stageMerge, stageAbridge}},
	}
	for _, tc := range cases {
		opts, err := parseSetting(settingStateStages, tc.value)
		if err != nil {
			t.Fatalf("parseSetting(%q): %v", tc.value, err)
		}
		if got := opts.stateStageKinds(); !slices.Equal(got, tc.want) {
			t.Errorf("stateStageKinds(%q) = %v, want %v", tc.value, got, tc.want)
		}
	}
}

func TestStateContextIsRead(t *testing.T) {
	opts, err := parseSetting(settingStateContext, "a custom context")
	if err != nil {
		t.Fatalf("parseSetting: %v", err)
	}
	if got := opts.stateContextText(); got != "a custom context" {
		t.Errorf("stateContextText() = %q, want the configured text", got)
	}
	if got := (options{}).stateContextText(); got != defaultStateContext {
		t.Errorf("stateContextText() without setting = %q, want the built-in text", got)
	}
}

func TestStateSettingsRefuseBadValues(t *testing.T) {
	cases := []struct {
		key   string
		value string
	}{
		{settingStateInputRunes, "10,20,30"},
		{settingStateInputRunes, "1000,200"},
		{settingStateInputRunes, "1000,0,60"},
		{settingStateInputRunes, "1000,200,lots"},
		{settingStateTextHead, "0"},
		{settingStateTextTail, "-3"},
		{settingStateStages, "abridge_texts,nope"},
		{settingStateStages, "leave_out,leave_out"},
		{settingStateStages, "leave_out,,merge_calls"},
	}
	for _, tc := range cases {
		_, err := parseSetting(tc.key, tc.value)
		if !errors.Is(err, ErrInvalidSetting) {
			t.Errorf("parseSetting(%s=%q) error = %v, want ErrInvalidSetting", tc.key, tc.value, err)
		}
	}
}

func TestStateContextReachesTheState(t *testing.T) {
	history := chattyHistory()
	opts := testOptions(defaultMaxStateTokens)
	opts.stateContext = "a custom context"
	fitted, err := fitState(history, collectCalls(history, 1), "goal", opts)
	if err != nil {
		t.Fatalf("fitState: %v", err)
	}
	if fitted.state.Context != "a custom context" {
		t.Fatalf("state context = %q, want the configured text", fitted.state.Context)
	}
}

func TestAbridgeUsesTheConfiguredHeadAndTail(t *testing.T) {
	text := strings.Repeat("0123456789", 100)
	fitter := &stateFitter{head: 10, tail: 5}
	entry := historyEntry{Text: text}
	if !fitter.abridgeText(&entry) {
		t.Fatal("abridgeText refused a text of 1000 runes")
	}
	if !strings.HasPrefix(entry.Text, "0123456789\n") || !strings.HasSuffix(entry.Text, "\n56789") {
		t.Fatalf("abridged text = %q, want 10 runes of head and 5 of tail", entry.Text)
	}
}

func TestStateStagesRunOnlyTheConfiguredOnes(t *testing.T) {
	cases := []struct {
		name     string
		stages   string
		allowed  []string
		required string
	}{
		{"leave out only", "leave_out", []string{"old messages left out"}, "old messages left out"},
		{"merge only", "merge_calls", []string{"old calls merged"}, ""},
		{"collapse then abridge", "collapse_messages,abridge_texts", []string{"old messages collapsed", "texts abridged"}, "old messages collapsed"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			opts, err := parseSetting(settingStateStages, tc.stages)
			if err != nil {
				t.Fatalf("parseSetting: %v", err)
			}
			seen := sweepStages(t, chattyHistory(), opts, tc.allowed)
			if tc.required != "" && !seen[tc.required] {
				t.Errorf("stage %q never reached, saw %v", tc.required, seen)
			}
		})
	}
}

func sweepStages(t *testing.T, history contracts.History, opts options, allowed []string) map[string]bool {
	t.Helper()
	opts.preserveRecent = 1
	opts.maxRequestTokens = defaultMaxRequestTokens
	seen := make(map[string]bool)
	for budget := defaultMaxStateTokens; budget > 0; budget -= 25 {
		opts.maxStateTokens = budget
		fitted, err := fitState(history, collectCalls(history, 1), "goal", opts)
		if errors.Is(err, ErrHistoryTooLarge) {
			break
		}
		if err != nil {
			t.Fatalf("fitState(%d): %v", budget, err)
		}
		if !strings.HasPrefix(fitted.stage, "inputs<=") && !slices.Contains(allowed, fitted.stage) {
			t.Fatalf("budget %d reached stage %q, want one of %v", budget, fitted.stage, allowed)
		}
		seen[fitted.stage] = true
	}
	return seen
}
