package jev

import (
	"errors"
	"testing"

	contracts "github.com/Herrscherd/herrscher-contracts"
)

func TestParseRulesAcceptsTheDocumentedForms(t *testing.T) {
	cases := []struct {
		name     string
		text     string
		patterns []string
		action   ruleAction
		head     int
		callKeep optionalRatio
		resKeep  optionalRatio
	}{
		{name: "keep several motifs", text: "Edit,Write:keep", patterns: []string{"Edit", "Write"}, action: actionKeep},
		{name: "prune", text: "Grep:prune", patterns: []string{"Grep"}, action: actionPrune},
		{name: "glob", text: "Read*:truncate", patterns: []string{"Read*"}, action: actionTruncate},
		{name: "both thresholds", text: "Bash:threshold=0.7,head=500", patterns: []string{"Bash"}, head: 500, callKeep: optionalRatio{value: 0.7, set: true}, resKeep: optionalRatio{value: 0.7, set: true}},
		{name: "call threshold only", text: "Bash:call_threshold=0.2", patterns: []string{"Bash"}, callKeep: optionalRatio{value: 0.2, set: true}},
		{name: "result threshold only", text: "Bash:result_threshold=1", patterns: []string{"Bash"}, resKeep: optionalRatio{value: 1, set: true}},
		{name: "spaces around motifs", text: " Edit , Write : keep ", patterns: []string{"Edit", "Write"}, action: actionKeep},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rules, ok := parseRules(tc.text)
			if !ok || len(rules) != 1 {
				t.Fatalf("parseRules(%q) = %+v, %v, want one rule", tc.text, rules, ok)
			}
			got := rules[0]
			if !equalStrings(got.patterns, tc.patterns) || got.action != tc.action || got.head != tc.head || got.callKeep != tc.callKeep || got.resultKeep != tc.resKeep {
				t.Fatalf("parseRules(%q) = %+v, want patterns %v action %v head %d", tc.text, got, tc.patterns, tc.action, tc.head)
			}
		})
	}
}

func TestParseRulesRefusesMalformedRules(t *testing.T) {
	cases := []struct {
		name string
		text string
	}{
		{"empty", ""},
		{"no colon", "Edit"},
		{"no action", "Edit:"},
		{"no motif", ":keep"},
		{"empty motif", "Edit,:keep"},
		{"bad glob", "[:keep"},
		{"unknown action", "Edit:delete"},
		{"two actions", "Edit:keep,prune"},
		{"threshold above one", "Edit:threshold=2"},
		{"threshold not a number", "Edit:call_threshold=high"},
		{"threshold twice", "Edit:threshold=0.5,call_threshold=0.4"},
		{"result threshold twice", "Edit:result_threshold=0.5,result_threshold=0.4"},
		{"head zero", "Edit:head=0"},
		{"head twice", "Edit:head=10,head=20"},
		{"unknown key", "Edit:limit=10"},
		{"trailing separator", "Edit:keep;"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if rules, ok := parseRules(tc.text); ok {
				t.Fatalf("parseRules(%q) = %+v, want refused", tc.text, rules)
			}
		})
	}
}

func TestRuleMatchesItsMotifs(t *testing.T) {
	rules, ok := parseRules("Edit,Read*:keep")
	if !ok {
		t.Fatal("parseRules refused a valid rule")
	}
	policy := &callPolicy{rules: rules}
	for tool, want := range map[string]bool{"Edit": true, "Read": true, "ReadFile": true, "Write": false, "Editor": false} {
		if got := policy.ruleFor(tool).action != 0; got != want {
			t.Errorf("ruleFor(%q) matched = %v, want %v", tool, got, want)
		}
	}
}

func TestRuleForKeepsTheFirstMatch(t *testing.T) {
	rules, ok := parseRules("Bash:keep;Ba*:prune")
	if !ok {
		t.Fatal("parseRules refused a valid policy")
	}
	policy := &callPolicy{rules: rules}
	if got := policy.ruleFor("Bash").action; got != actionKeep {
		t.Errorf("ruleFor(Bash) = %v, want keep", got)
	}
	if got := policy.ruleFor("Bashful").action; got != actionPrune {
		t.Errorf("ruleFor(Bashful) = %v, want prune", got)
	}
}

func TestNilPolicyDecidesNothing(t *testing.T) {
	var policy *callPolicy
	if policy.ruleFor("Bash").action != 0 {
		t.Error("a nil policy matched a rule")
	}
	if policy.pinned("anything") {
		t.Error("a nil policy pinned a call")
	}
}

func TestPinnedMatchesSubstringsOfTheInput(t *testing.T) {
	policy := &callPolicy{pins: []string{"parser.go", "secret"}}
	if !policy.pinned(`{"file_path":"src/parser.go"}`) {
		t.Error("an input containing a pin was not pinned")
	}
	if policy.pinned(`{"file_path":"lexer.go"}`) {
		t.Error("an input without any pin was pinned")
	}
}

func TestParseOptionsRefusesBadRulesWithInvalidSetting(t *testing.T) {
	for _, settings := range []map[string]string{
		{settingRules: "Edit:delete"},
		{settingPinInputs: "a,,b"},
		{settingMaxRemovedShare: "0"},
		{settingMaxPruned: "-1"},
		{settingKeepErrors: "yes"},
		{settingMinResultChars: "-3"},
	} {
		settings[settingAPIKey] = "key"
		if _, err := parseOptions(contracts.PluginConfig{Settings: settings}); !errors.Is(err, ErrInvalidSetting) {
			t.Errorf("parseOptions(%v) error = %v, want ErrInvalidSetting", settings, err)
		}
	}
}

func equalStrings(left, right []string) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index] != right[index] {
			return false
		}
	}
	return true
}
