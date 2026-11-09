//go:build live

package jev

import (
	"context"
	"net/http"
	"os"
	"testing"

	contracts "github.com/Herrscherd/herrscher-contracts"
)

type pruningHistory struct {
	history contracts.History
}

func (p *pruningHistory) ReadHistory(context.Context) (contracts.History, error) {
	return p.history, nil
}

func (p *pruningHistory) PruneHistory(_ context.Context, edits []contracts.HistoryEdit) (contracts.PruneOutcome, error) {
	before := toolRunes(p.history)
	byID := make(map[string]contracts.HistoryEdit, len(edits))
	for _, edit := range edits {
		byID[edit.CallID] = edit
	}
	edited := 0
	for index := range p.history.Messages {
		calls := p.history.Messages[index].Calls
		for position := range calls {
			edit, found := byID[calls[position].ID]
			if !found {
				continue
			}
			applyEdit(&calls[position], edit)
			edited++
		}
	}
	return contracts.PruneOutcome{Edited: edited, CharsBefore: before, CharsAfter: toolRunes(p.history)}, nil
}

func applyEdit(call *contracts.HistoryCall, edit contracts.HistoryEdit) {
	if edit.Action == contracts.PruneCall {
		call.Input = ""
		call.Result = ""
		return
	}
	runes := []rune(call.Result)
	if edit.KeepChars < len(runes) {
		call.Result = string(runes[:edit.KeepChars])
	}
}

func fixtureHistory(t *testing.T) contracts.History {
	t.Helper()
	history, err := fixtureSnapshot(t).History.history()
	if err != nil {
		t.Fatalf("fixture history: %v", err)
	}
	return history
}

func TestLiveJevAnswersTheSessionHistory(t *testing.T) {
	key := os.Getenv(typesafeEnv)
	if key == "" {
		t.Skipf("set %s to run the live Jev test", typesafeEnv)
	}
	stage, err := newCompactor(contracts.PluginConfig{Settings: map[string]string{settingAPIKey: key}}, newMessages(), &http.Client{})
	if err != nil {
		t.Fatalf("newCompactor: %v", err)
	}
	editor := &pruningHistory{history: fixtureHistory(t)}
	before := toolRunes(editor.history)
	result, err := stage.Compact(t.Context(), contracts.CompactRequest{History: editor}, nil)
	if err != nil {
		t.Fatalf("Compact against the live API: %v", err)
	}
	if result.Stats == nil || result.Stats.Requests == 0 {
		t.Fatal("live compaction sent no request to Jev")
	}
	if result.Stats.Outcome == contracts.CompactFailed {
		t.Fatalf("live compaction failed: %q", result.Note)
	}
	if result.Note == "" {
		t.Fatal("live compaction returned no note")
	}
	after, err := editor.ReadHistory(t.Context())
	if err != nil {
		t.Fatalf("read the history after the live compaction: %v", err)
	}
	if result.Stats.CharsBefore != before {
		t.Fatalf("reported characters before = %d, the history held %d", result.Stats.CharsBefore, before)
	}
	if measured := toolRunes(after); result.Stats.CharsAfter != measured {
		t.Fatalf("reported characters after = %d, the pruned history holds %d", result.Stats.CharsAfter, measured)
	}
	if result.Stats.CharsAfter >= result.Stats.CharsBefore {
		t.Fatalf("reduction = %d -> %d characters, want fewer after the live prune", result.Stats.CharsBefore, result.Stats.CharsAfter)
	}
}
