package jev

import (
	"net/http"
	"os"
	"testing"

	contracts "github.com/Herrscherd/herrscher-contracts"
)

func TestLiveJevAnswersTheSessionHistory(t *testing.T) {
	key := os.Getenv(typesafeEnv)
	if key == "" {
		t.Skipf("set %s to run the live Jev test", typesafeEnv)
	}
	stage, err := newCompactor(contracts.PluginConfig{Settings: map[string]string{settingAPIKey: key}}, newMessages(), &http.Client{})
	if err != nil {
		t.Fatalf("newCompactor: %v", err)
	}
	editor := &fakeHistory{history: sessionHistory(), outcome: contracts.PruneOutcome{Edited: 0}}
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
}
