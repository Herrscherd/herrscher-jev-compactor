package jev

import (
	"errors"
	"strings"
	"testing"

	contracts "github.com/Herrscherd/herrscher-contracts"
)

func TestSpendLedgerStopsAtTheRequestCap(t *testing.T) {
	ledger := newSpendLedger(2, 0)
	for range 2 {
		if err := ledger.reserve(1); err != nil {
			t.Fatalf("reserve under the cap: %v", err)
		}
	}
	if err := ledger.reserve(1); !errors.Is(err, ErrSpendCapped) {
		t.Fatalf("reserve past the cap = %v, want ErrSpendCapped", err)
	}
	if got := ledger.total(); got.requests != 2 {
		t.Fatalf("requests = %d, want 2: the refused attempt is not counted", got.requests)
	}
}

func TestSpendLedgerStopsAtTheTokenCap(t *testing.T) {
	ledger := newSpendLedger(0, 10)
	if err := ledger.reserve(6); err != nil {
		t.Fatalf("reserve(6): %v", err)
	}
	if err := ledger.reserve(5); !errors.Is(err, ErrSpendCapped) {
		t.Fatalf("reserve(5) past 10 tokens = %v, want ErrSpendCapped", err)
	}
	if err := ledger.reserve(4); err != nil {
		t.Fatalf("reserve(4) landing on the cap: %v", err)
	}
	if got := ledger.total(); got.tokens != 10 || got.requests != 2 {
		t.Fatalf("total = %+v, want 2 requests and 10 tokens", got)
	}
}

func TestSpendLedgerWithoutCapsCountsEverything(t *testing.T) {
	ledger := newSpendLedger(0, 0)
	for range 50 {
		if err := ledger.reserve(1000); err != nil {
			t.Fatalf("reserve without a cap: %v", err)
		}
	}
	if got := ledger.total(); got.requests != 50 || got.tokens != 50000 {
		t.Fatalf("total = %+v, want 50 requests and 50000 tokens", got)
	}
}

func TestAskAllSendsNothingPastTheCap(t *testing.T) {
	stage := newTestCompactor(t, &fakeJev{answers: sessionAnswers()}, nil)
	calls := collectCalls(sessionHistory(), 1)
	spend := newSpendLedger(1, 0)
	merged, err := stage.askAll(t.Context(), compactionState{}, [][]toolCall{{calls[0]}, {calls[1]}}, spend)
	if err != nil {
		t.Fatalf("askAll: %v, want a capped batch to be skipped, not failed", err)
	}
	if !spend.capped() {
		t.Fatal("capped = false, want true when a batch was not sent")
	}
	if len(merged) != 1 {
		t.Fatalf("answers = %d, want the one batch that fitted under the cap", len(merged))
	}
}

func TestDecideAskedKeepsCallsWithoutAnAnswer(t *testing.T) {
	stage := newTestCompactor(t, &fakeJev{answers: sessionAnswers()}, nil)
	calls := collectCalls(sessionHistory(), 1)
	decided := make(map[string]plannedEdit)
	stage.decideAsked(calls, make(map[string]callAnswer), decided, nil)
	if len(decided) != 0 {
		t.Fatalf("decided = %v, want every call without an answer kept", decided)
	}
}

func TestSpendCapStopsARetryAndKeepsTheCalls(t *testing.T) {
	stage, server, _ := retryingStage(t,
		[]scriptedReply{{status: 503}},
		map[string]string{settingRetries: "1", settingRetryBackoff: "1s", settingMaxRequests: "1"})
	result, editor, err := compactSession(t, stage)
	if err != nil {
		t.Fatalf("Compact: %v", err)
	}
	if server.requests() != 1 {
		t.Fatalf("requests = %d, want the retry refused by the cap", server.requests())
	}
	if len(editor.pruned) != 0 {
		t.Fatal("a capped compaction still pruned calls it had not judged")
	}
	if result.Done || result.Stats.Outcome != contracts.CompactSkipped {
		t.Fatalf("result = done %v, outcome %v, want the stage to step aside", result.Done, result.Stats.Outcome)
	}
	if result.Stats.Requests != 1 {
		t.Fatalf("Stats.Requests = %d, want the one request that was sent", result.Stats.Requests)
	}
	if !strings.Contains(result.Note, "spend cap reached after 1 requests") {
		t.Fatalf("note = %q, want the spend cap said in it", result.Note)
	}
}

func TestSpendCapSettingsAreReadAndBounded(t *testing.T) {
	opts, err := parseOptions(contracts.PluginConfig{Settings: map[string]string{settingAPIKey: "key", settingMaxRequests: "3", settingMaxSent: "1000"}})
	if err != nil {
		t.Fatalf("parseOptions: %v", err)
	}
	if opts.maxRequests != 3 || opts.maxSentTokens != 1000 {
		t.Fatalf("maxRequests = %d, maxSentTokens = %d, want 3 and 1000", opts.maxRequests, opts.maxSentTokens)
	}
	for _, key := range []string{settingMaxRequests, settingMaxSent} {
		if _, err := parseOptions(contracts.PluginConfig{Settings: map[string]string{settingAPIKey: "key", key: "-1"}}); err == nil {
			t.Errorf("parseOptions(%s=-1) error = nil, want a refused setting", key)
		}
	}
}
