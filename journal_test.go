package jev

import (
	"bytes"
	"log/slog"
	"strings"
	"testing"
)

func captureDebug(t *testing.T) *bytes.Buffer {
	t.Helper()
	var buf bytes.Buffer
	previous := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug})))
	t.Cleanup(func() { slog.SetDefault(previous) })
	return &buf
}

func TestEveryDecisionIsJournaledWithItsReason(t *testing.T) {
	journal := captureDebug(t)
	planWith(t, sessionHistory(), map[string]string{settingPinInputs: "parser.go", settingRules: "Grep:prune"})
	lines := strings.Split(strings.TrimSpace(journal.String()), "\n")
	for _, want := range []string{
		`msg="jev: call decided" ref=`,
		`tool=Grep verdict=prune_call reason=rule`,
		`rule=Grep`,
		`reason=pinned`,
		`reason=threshold`,
		`p_call=`,
		`p_result=`,
		`call_threshold=0.5 result_threshold=0.5`,
	} {
		if !containsLine(lines, want) {
			t.Errorf("no journal line with %q in:\n%s", want, journal)
		}
	}
}

func TestBudgetDemotionIsJournaled(t *testing.T) {
	journal := captureDebug(t)
	planWith(t, sessionHistory(), map[string]string{settingRules: "Grep:prune;Bash:prune", settingMaxPruned: "1"})
	if !containsLine(strings.Split(journal.String(), "\n"), "verdict=keep reason=budget") {
		t.Fatalf("no call demoted by the budget was journaled:\n%s", journal)
	}
}

func TestKeepErrorsIsJournaledAsTheReason(t *testing.T) {
	journal := captureDebug(t)
	planWith(t, failedGrep(), map[string]string{settingRules: "Grep:prune", settingKeepErrors: "true"})
	if !containsLine(strings.Split(journal.String(), "\n"), "verdict=prune_result reason=error") {
		t.Fatalf("a failed call kept by keep_errors was not journaled as an error:\n%s", journal)
	}
}

func containsLine(lines []string, fragment string) bool {
	for _, line := range lines {
		if strings.Contains(line, fragment) {
			return true
		}
	}
	return false
}
