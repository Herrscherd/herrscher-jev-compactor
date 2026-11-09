package jev

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync"
	"testing"

	contracts "github.com/Herrscherd/herrscher-contracts"
)

type fakeHistory struct {
	history contracts.History
	outcome contracts.PruneOutcome
	pruned  [][]contracts.HistoryEdit
}

func (f *fakeHistory) ReadHistory(context.Context) (contracts.History, error) {
	return f.history, nil
}

func (f *fakeHistory) PruneHistory(_ context.Context, edits []contracts.HistoryEdit) (contracts.PruneOutcome, error) {
	f.pruned = append(f.pruned, edits)
	return f.outcome, nil
}

type fakeJev struct {
	mu       sync.Mutex
	status   int
	answers  map[string]float64
	requests []jevRequest
	auth     []string
	echoAuth bool
}

func (f *fakeJev) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(r.Body)
	if err != nil {
		w.WriteHeader(http.StatusBadRequest)
		return
	}
	var request jevRequest
	if err := json.Unmarshal(body, &request); err != nil {
		w.WriteHeader(http.StatusBadRequest)
		return
	}
	f.mu.Lock()
	f.requests = append(f.requests, request)
	f.auth = append(f.auth, r.Header.Get("Authorization"))
	status := f.status
	f.mu.Unlock()
	if status != 0 {
		refusal := "quota exhausted"
		if f.echoAuth {
			refusal = "bad credentials: " + r.Header.Get("Authorization")
		}
		http.Error(w, refusal, status)
		return
	}
	answers := make(map[string]map[string]float64)
	for name := range request.Questions {
		if probability, found := f.answers[name]; found {
			answers[name] = map[string]float64{"noul": probability}
		}
	}
	if err := json.NewEncoder(w).Encode(map[string]any{"answers": answers}); err != nil {
		w.WriteHeader(http.StatusInternalServerError)
	}
}

func (f *fakeJev) askedQuestions() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	var names []string
	for _, request := range f.requests {
		for name := range request.Questions {
			names = append(names, name)
		}
	}
	slices.Sort(names)
	return names
}

func newTestCompactor(t *testing.T, server http.Handler, settings map[string]string) *compactor {
	t.Helper()
	tls := httptest.NewTLSServer(server)
	t.Cleanup(tls.Close)
	values := map[string]string{settingAPIKey: "test-key", settingEndpoint: tls.URL, settingPreserveRecent: "1"}
	for key, value := range settings {
		values[key] = value
	}
	built, err := newCompactor(contracts.PluginConfig{Settings: values}, newMessages(), tls.Client())
	if err != nil {
		t.Fatalf("newCompactor: %v", err)
	}
	return built
}

func sessionHistory() contracts.History {
	long := strings.Repeat("line of output\n", 100)
	return contracts.History{Messages: []contracts.HistoryMessage{
		{Role: contracts.RoleUser, Text: "fix the parser"},
		{Role: contracts.RoleAssistant, Text: "reading", Calls: []contracts.HistoryCall{
			{ID: "toolu_kept", Tool: "Read", Input: `{"file_path":"parser.go"}`, Result: long},
			{ID: "toolu_head", Tool: "Bash", Input: `{"command":"go test ./..."}`, Result: long},
			{ID: "toolu_gone", Tool: "Grep", Input: `{"pattern":"TODO"}`, Result: long},
			{ID: "toolu_short", Tool: "Bash", Input: `{"command":"ls"}`, Result: "a b c"},
		}},
		{Role: contracts.RoleUser, Text: "now the lexer"},
		{Role: contracts.RoleAssistant, Text: "on it", Calls: []contracts.HistoryCall{
			{ID: "toolu_recent", Tool: "Read", Input: `{"file_path":"lexer.go"}`, Result: long},
		}},
	}}
}

func sessionAnswers() map[string]float64 {
	return map[string]float64{
		"call_t1": 0.9, "result_t1": 0.8,
		"call_t2": 0.7, "result_t2": 0.2,
		"call_t3": 0.1, "result_t3": 0.1,
		"call_t4": 0.9, "result_t4": 0.1,
	}
}

func TestCompactPrunesWhatJevLetsGoAndStopsTheStack(t *testing.T) {
	server := &fakeJev{answers: sessionAnswers()}
	stage := newTestCompactor(t, server, nil)
	editor := &fakeHistory{history: sessionHistory(), outcome: contracts.PruneOutcome{Edited: 2, CharsBefore: 1000, CharsAfter: 600}}
	result, err := stage.Compact(t.Context(), contracts.CompactRequest{History: editor}, nil)
	if err != nil {
		t.Fatalf("Compact: %v", err)
	}
	want := []contracts.HistoryEdit{
		{CallID: "toolu_head", Action: contracts.PruneResult, KeepChars: defaultTruncateHead},
		{CallID: "toolu_gone", Action: contracts.PruneCall},
	}
	if len(editor.pruned) != 1 || !slices.Equal(editor.pruned[0], want) {
		t.Fatalf("pruned %+v, want %+v", editor.pruned, want)
	}
	if !result.Done || result.TokensMeasured || result.Notice.Code != contracts.CodeCompacted {
		t.Fatalf("result = %+v, want done, unmeasured, compacted", result)
	}
	if result.Note != "Jev reviewed 5 tool calls: 3 kept, 1 truncated, 1 removed, the history is 38% smaller" {
		t.Fatalf("note = %q", result.Note)
	}
	wantAsked := []string{"call_t1", "call_t2", "call_t3", "call_t4", "result_t1", "result_t2", "result_t3", "result_t4"}
	if asked := server.askedQuestions(); !slices.Equal(asked, wantAsked) {
		t.Fatalf("asked %v, want %v: the pinned call t5 must not be asked", asked, wantAsked)
	}
	if server.auth[0] != "Bearer test-key" || server.requests[0].Model != defaultModel {
		t.Fatalf("request auth %q model %q", server.auth[0], server.requests[0].Model)
	}
	if goal := server.requests[0].State.Goal; goal != "fix the parser\nnow the lexer" {
		t.Fatalf("goal = %q, want the user prompts", goal)
	}
}

func TestCompactLetsTheNextStageRunWhenTheReductionFallsShort(t *testing.T) {
	stage := newTestCompactor(t, &fakeJev{answers: sessionAnswers()}, nil)
	editor := &fakeHistory{history: sessionHistory(), outcome: contracts.PruneOutcome{Edited: 1, CharsBefore: 1000, CharsAfter: 900}}
	result, err := stage.Compact(t.Context(), contracts.CompactRequest{History: editor, Instruction: "keep the parser"}, nil)
	if err != nil {
		t.Fatalf("Compact: %v", err)
	}
	if result.Done {
		t.Fatalf("result = %+v, want the stack to go on", result)
	}
	if result.Note != "Jev reviewed 5 tool calls: 3 kept, 1 truncated, 1 removed, the history is 9% smaller, short of the 25% aimed for, so the next stage runs" {
		t.Fatalf("note = %q", result.Note)
	}
}

func TestCompactUsesTheOperatorInstructionAsGoal(t *testing.T) {
	server := &fakeJev{answers: sessionAnswers()}
	stage := newTestCompactor(t, server, nil)
	editor := &fakeHistory{history: sessionHistory(), outcome: contracts.PruneOutcome{Edited: 2, CharsBefore: 10, CharsAfter: 1}}
	if _, err := stage.Compact(t.Context(), contracts.CompactRequest{History: editor, Instruction: " keep the parser "}, nil); err != nil {
		t.Fatalf("Compact: %v", err)
	}
	if goal := server.requests[0].State.Goal; goal != "keep the parser" {
		t.Fatalf("goal = %q, want the instruction", goal)
	}
}

func TestCompactWithoutAHistoryEditorSkipsWithoutCallingJev(t *testing.T) {
	server := &fakeJev{}
	stage := newTestCompactor(t, server, nil)
	result, err := stage.Compact(t.Context(), contracts.CompactRequest{}, nil)
	if err != nil || result.Done || result.Note == "" {
		t.Fatalf("Compact = %+v, %v, want a note and the next stage", result, err)
	}
	if len(server.requests) != 0 {
		t.Fatal("Jev was called for a backend that cannot edit its history")
	}
}

func TestCompactWithOnlyPinnedCallsPrunesNothing(t *testing.T) {
	server := &fakeJev{}
	stage := newTestCompactor(t, server, map[string]string{settingPreserveRecent: "10"})
	editor := &fakeHistory{history: sessionHistory()}
	result, err := stage.Compact(t.Context(), contracts.CompactRequest{History: editor}, nil)
	if err != nil || result.Done || len(editor.pruned) != 0 || len(server.requests) != 0 {
		t.Fatalf("Compact = %+v, %v, pruned %v, requests %d, want nothing done", result, err, editor.pruned, len(server.requests))
	}
}

func redirectingJev(w http.ResponseWriter, r *http.Request) {
	http.Redirect(w, r, "/elsewhere", http.StatusFound)
}

func malformedJev(w http.ResponseWriter, _ *http.Request) {
	if _, err := io.WriteString(w, `{"answers":`); err != nil {
		w.WriteHeader(http.StatusInternalServerError)
	}
}

func TestCompactFallsBackToTheNextStageWhenJevFails(t *testing.T) {
	cases := map[string]struct {
		server http.Handler
		tune   func(*options)
		want   error
	}{
		"refused":         {server: &fakeJev{status: http.StatusUnauthorized, echoAuth: true}, want: ErrJevRefused},
		"malformed":       {server: http.HandlerFunc(malformedJev), want: ErrJevMalformed},
		"missing answer":  {server: &fakeJev{answers: map[string]float64{"call_t1": 1}}, want: ErrJevMissingAnswer},
		"redirected":      {server: http.HandlerFunc(redirectingJev), want: ErrJevRedirected},
		"history too big": {server: &fakeJev{}, tune: func(o *options) { o.maxStateTokens = 10 }, want: ErrHistoryTooLarge},
		"no room":         {server: &fakeJev{}, tune: func(o *options) { o.maxRequestTokens = 1 }, want: ErrNoRoomForQuestions},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			stage := newTestCompactor(t, tc.server, nil)
			if tc.tune != nil {
				tc.tune(&stage.opts)
			}
			if _, err := stage.plan(t.Context(), sessionHistory(), "", nil); !errors.Is(err, tc.want) {
				t.Fatalf("plan error = %v, want %v", err, tc.want)
			}
			assertFallsBack(t, stage)
		})
	}
}

func assertFallsBack(t *testing.T, stage *compactor) {
	t.Helper()
	editor := &fakeHistory{history: sessionHistory()}
	result, err := stage.Compact(t.Context(), contracts.CompactRequest{History: editor}, nil)
	if err != nil {
		t.Fatalf("Compact error = %v, want the next stage to run", err)
	}
	if result.Done || result.Notice.Code != contracts.CodeCompactUnavailable || !strings.HasPrefix(result.Note, "Jev pruned nothing, the next stage compacts: ") {
		t.Fatalf("result = %+v, want an unavailable notice and the next stage", result)
	}
	if len(editor.pruned) != 0 {
		t.Fatal("a failed Jev call still pruned the history")
	}
}

func TestJevRefusalNeverLeaksTheAPIKey(t *testing.T) {
	stage := newTestCompactor(t, &fakeJev{status: http.StatusUnauthorized, echoAuth: true}, nil)
	refusal := stage.client.ask(t.Context(), compactionState{}, nil)
	if !errors.Is(refusal, ErrJevRefused) || !strings.Contains(refusal.Error(), "Bearer "+redactedMarker) {
		t.Fatalf("ask error = %v, want a refusal echoing the redacted header", refusal)
	}
	result, err := stage.Compact(t.Context(), contracts.CompactRequest{History: &fakeHistory{history: sessionHistory()}}, nil)
	if err != nil {
		t.Fatalf("Compact: %v", err)
	}
	for place, text := range map[string]string{"error": refusal.Error(), "note": result.Note, "notice": fmt.Sprint(result.Notice)} {
		if strings.Contains(text, "test-key") {
			t.Errorf("the %s leaks the API key: %s", place, text)
		}
	}
}

func TestCompactFailsWhenItsContextIsCancelled(t *testing.T) {
	stage := newTestCompactor(t, &fakeJev{answers: sessionAnswers()}, nil)
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	editor := &fakeHistory{history: sessionHistory()}
	if _, err := stage.Compact(ctx, contracts.CompactRequest{History: editor}, nil); !errors.Is(err, context.Canceled) {
		t.Fatalf("Compact error = %v, want context.Canceled", err)
	}
	if len(editor.pruned) != 0 {
		t.Fatal("a cancelled compaction still pruned the history")
	}
}

func TestAskAllReportsTheRefusalNotTheCancelledBatch(t *testing.T) {
	slowThenRefused := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		if strings.Contains(string(body), callQuestionPrefix+"t1") {
			<-r.Context().Done()
			return
		}
		http.Error(w, "bad credentials", http.StatusUnauthorized)
	})
	stage := newTestCompactor(t, slowThenRefused, nil)
	calls := collectCalls(sessionHistory(), 1)
	_, err := stage.askAll(t.Context(), compactionState{}, [][]toolCall{{calls[0]}, {calls[1]}}, newSpendLedger(0, 0))
	if !errors.Is(err, ErrJevRefused) {
		t.Fatalf("askAll error = %v, want ErrJevRefused", err)
	}
}

func TestCompactCountsMessageTextInTheReduction(t *testing.T) {
	stage := newTestCompactor(t, &fakeJev{answers: sessionAnswers()}, nil)
	history := sessionHistory()
	history.Messages[0].Text = strings.Repeat("a long prompt ", 1000)
	editor := &fakeHistory{history: history, outcome: contracts.PruneOutcome{Edited: 2, CharsBefore: 1000, CharsAfter: 200}}
	result, err := stage.Compact(t.Context(), contracts.CompactRequest{History: editor}, nil)
	if err != nil {
		t.Fatalf("Compact: %v", err)
	}
	if result.Done {
		t.Fatalf("result = %+v, want the next stage: the text the pruning left alone outweighs the outputs", result)
	}
}

func TestCompactSplitsTheQuestionsAcrossRequests(t *testing.T) {
	server := &fakeJev{answers: sessionAnswers()}
	stage := newTestCompactor(t, server, nil)
	history := sessionHistory()
	fitted, err := fitState(history, collectCalls(history, 1), "fix the parser\nnow the lexer", stage.opts)
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := encodeJSON(questionsFor(collectCalls(history, 1)[0]))
	if err != nil {
		t.Fatal(err)
	}
	stage.opts.maxRequestTokens = fitted.tokens + requestOverheadTokens + 2*estimateTokens(encoded) + 10
	editor := &fakeHistory{history: history, outcome: contracts.PruneOutcome{Edited: 2, CharsBefore: 10, CharsAfter: 1}}
	if _, err := stage.Compact(t.Context(), contracts.CompactRequest{History: editor}, nil); err != nil {
		t.Fatalf("Compact: %v", err)
	}
	if len(server.requests) < 2 || len(server.askedQuestions()) != 8 {
		t.Fatalf("%d requests asking %v, want the 8 questions split", len(server.requests), server.askedQuestions())
	}
}
