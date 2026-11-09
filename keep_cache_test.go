package jev

import (
	"slices"
	"strconv"
	"strings"
	"testing"

	contracts "github.com/Herrscherd/herrscher-contracts"
)

const cachedNote = "1 call kept from the cache, not asked again."

func incrementalStage(t *testing.T, settings map[string]string) (*compactor, *fakeJev) {
	t.Helper()
	server := &fakeJev{answers: sessionAnswers()}
	return newTestCompactor(t, server, settings), server
}

func planAgain(t *testing.T, stage *compactor, server *fakeJev, history contracts.History, req contracts.CompactRequest) planReport {
	t.Helper()
	server.requests = nil
	report, err := stage.plan(t.Context(), history, req, nil)
	if err != nil {
		t.Fatalf("plan: %v", err)
	}
	return report
}

func sessionRequest(name string) contracts.CompactRequest {
	return contracts.CompactRequest{Session: contracts.SessionInfo{Name: name, Incarnation: "1"}}
}

func TestIncrementalDoesNotAskAgainAboutTheCallsKept(t *testing.T) {
	stage, server := incrementalStage(t, map[string]string{settingIncremental: "true"})
	planAgain(t, stage, server, sessionHistory(), sessionRequest("main"))
	report := planAgain(t, stage, server, sessionHistory(), sessionRequest("main"))
	asked := server.askedQuestions()
	if slices.Contains(asked, "call_t1") || slices.Contains(asked, "result_t1") {
		t.Fatalf("a call kept by the first compaction was asked again: %v", asked)
	}
	if !slices.Contains(asked, "call_t2") {
		t.Fatalf("a call that was not kept was not asked again: %v", asked)
	}
	if report.cached != 1 {
		t.Fatalf("cached = %d, want 1", report.cached)
	}
}

func TestIncrementalAsksAgainWhenTheContentChanges(t *testing.T) {
	stage, server := incrementalStage(t, map[string]string{settingIncremental: "true"})
	planAgain(t, stage, server, sessionHistory(), sessionRequest("main"))
	changed := sessionHistory()
	changed.Messages[1].Calls[0].Result += " edited"
	report := planAgain(t, stage, server, changed, sessionRequest("main"))
	asked := server.askedQuestions()
	if !slices.Contains(asked, "call_t1") || report.cached != 0 {
		t.Fatalf("a changed kept call was not judged again: cached %d, asked %v", report.cached, asked)
	}
}

func TestIncrementalAsksAgainWhenTheGoalChanges(t *testing.T) {
	stage, server := incrementalStage(t, map[string]string{settingIncremental: "true"})
	planAgain(t, stage, server, sessionHistory(), sessionRequest("main"))
	req := sessionRequest("main")
	req.Instruction = "keep only the lexer"
	report := planAgain(t, stage, server, sessionHistory(), req)
	if !slices.Contains(server.askedQuestions(), "call_t1") || report.cached != 0 {
		t.Fatalf("a kept call was not judged again under a new goal: cached %d, asked %v", report.cached, server.askedQuestions())
	}
}

func TestIncrementalIsScopedToTheSession(t *testing.T) {
	stage, server := incrementalStage(t, map[string]string{settingIncremental: "true"})
	planAgain(t, stage, server, sessionHistory(), sessionRequest("main"))
	report := planAgain(t, stage, server, sessionHistory(), sessionRequest("other"))
	if report.cached != 0 || !slices.Contains(server.askedQuestions(), "call_t1") {
		t.Fatalf("another session reused the verdicts of the first: cached %d, asked %v", report.cached, server.askedQuestions())
	}
}

func TestIncrementalIsOffByDefault(t *testing.T) {
	stage, server := incrementalStage(t, nil)
	planAgain(t, stage, server, sessionHistory(), sessionRequest("main"))
	report := planAgain(t, stage, server, sessionHistory(), sessionRequest("main"))
	if report.cached != 0 || !slices.Contains(server.askedQuestions(), "call_t1") {
		t.Fatalf("calls were not asked again without incremental: cached %d, asked %v", report.cached, server.askedQuestions())
	}
}

func TestIncrementalNoteCountsTheCacheHits(t *testing.T) {
	stage, _ := incrementalStage(t, map[string]string{settingIncremental: "true"})
	editor := &fakeHistory{history: sessionHistory(), outcome: contracts.PruneOutcome{Edited: 2, CharsBefore: 1000, CharsAfter: 600}}
	req := sessionRequest("main")
	req.History = editor
	if _, err := stage.Compact(t.Context(), req, nil); err != nil {
		t.Fatalf("first Compact: %v", err)
	}
	result, err := stage.Compact(t.Context(), req, nil)
	if err != nil {
		t.Fatalf("second Compact: %v", err)
	}
	if !strings.Contains(result.Note, cachedNote) {
		t.Fatalf("note = %q, want it to count the cache hit", result.Note)
	}
}

func TestKeepCacheIsBounded(t *testing.T) {
	cache := newKeepCache()
	for index := range keepCacheLimit + 1 {
		cache.store(keepKey{call: strconv.Itoa(index)}, "digest")
	}
	if size := len(cache.digests); size > keepCacheLimit {
		t.Fatalf("cache holds %d entries, want at most %d", size, keepCacheLimit)
	}
}
