package jev

import (
	"errors"
	"fmt"
	"strings"
	"testing"

	contracts "github.com/Herrscherd/herrscher-contracts"
)

func TestDecideFollowsTheThreshold(t *testing.T) {
	cases := []struct {
		name   string
		pinned bool
		answer callAnswer
		want   decision
	}{
		{"pinned", true, callAnswer{}, decisionKeep},
		{"result needed", false, callAnswer{keepCall: 0, keepResult: 0.5}, decisionKeep},
		{"call needed", false, callAnswer{keepCall: 0.5, keepResult: 0.49}, decisionPruneResult},
		{"neither", false, callAnswer{keepCall: 0.49, keepResult: 0.49}, decisionPruneCall},
	}
	for _, tc := range cases {
		if got := decide(toolCall{pinned: tc.pinned}, tc.answer, 0.5); got != tc.want {
			t.Errorf("%s: decide = %v, want %v", tc.name, got, tc.want)
		}
	}
}

func TestEditForLeavesShortResultsAlone(t *testing.T) {
	short := toolCall{source: contracts.HistoryCall{ID: "a", Result: strings.Repeat("x", 420)}}
	if _, prune := editFor(short, decisionPruneResult, truncation{head: 300}); prune {
		t.Fatal("a result within the slack of the head was pruned")
	}
	long := toolCall{source: contracts.HistoryCall{ID: "b", Result: strings.Repeat("x", 421)}}
	edit, prune := editFor(long, decisionPruneResult, truncation{head: 300})
	if !prune || edit != (contracts.HistoryEdit{CallID: "b", Action: contracts.PruneResult, KeepChars: 300}) {
		t.Fatalf("editFor = %+v, %v", edit, prune)
	}
	if err := contracts.ValidateHistoryEdits([]contracts.HistoryEdit{edit}); err != nil {
		t.Fatalf("the edit does not validate: %v", err)
	}
}

func TestBatchCallsRefusesAStateWithNoRoomLeft(t *testing.T) {
	calls := []toolCall{{ref: "t1", source: contracts.HistoryCall{Tool: "Read"}}}
	if _, err := batchCalls(calls, 1000, 1000); !errors.Is(err, ErrNoRoomForQuestions) {
		t.Fatalf("batchCalls error = %v, want ErrNoRoomForQuestions", err)
	}
	batches, err := batchCalls(calls, 0, defaultMaxRequestTokens)
	if err != nil || len(batches) != 1 {
		t.Fatalf("batchCalls = %v, %v, want one batch", batches, err)
	}
}

func TestPruneCallCountsTheNoteThatReplacesTheResult(t *testing.T) {
	call := toolCall{source: contracts.HistoryCall{ID: "a", Result: strings.Repeat("x", 500)}}
	got := removalOf(call, contracts.HistoryEdit{CallID: "a", Action: contracts.PruneCall})
	want := 500 - runeCount(prunedCallNote)
	if got.runes != want {
		t.Fatalf("removal runes = %d, want %d: the note the backend writes stays in the history", got.runes, want)
	}
}

func TestPruneResultCountsTheNoteAndItsSeparator(t *testing.T) {
	call := toolCall{source: contracts.HistoryCall{ID: "b", Result: strings.Repeat("x", 1000)}}
	got := removalOf(call, contracts.HistoryEdit{CallID: "b", Action: contracts.PruneResult, KeepChars: 300})
	inserted := "\n" + fmt.Sprintf(prunedResultNote, 700)
	if want := 700 - runeCount(inserted); got.runes != want {
		t.Fatalf("removal runes = %d, want %d", got.runes, want)
	}
	if want := estimateTokens(strings.Repeat("x", 700)) - estimateTokens(inserted); got.tokens != want {
		t.Fatalf("removal tokens = %d, want %d", got.tokens, want)
	}
}
