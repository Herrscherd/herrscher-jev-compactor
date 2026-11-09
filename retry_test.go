package jev

import (
	"context"
	"net/http"
	"slices"
	"sync"
	"testing"
	"time"

	contracts "github.com/Herrscherd/herrscher-contracts"
)

type scriptedReply struct {
	status     int
	retryAfter string
}

type scriptedJev struct {
	script []scriptedReply
	served *fakeJev
	mu     sync.Mutex
	seen   int
}

func (s *scriptedJev) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	index := s.seen
	s.seen++
	s.mu.Unlock()
	if index >= len(s.script) {
		s.served.ServeHTTP(w, r)
		return
	}
	reply := s.script[index]
	if reply.retryAfter != "" {
		w.Header().Set("Retry-After", reply.retryAfter)
	}
	http.Error(w, "busy", reply.status)
}

func (s *scriptedJev) requests() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.seen
}

type sleepRecorder struct {
	delays []time.Duration
}

func (s *sleepRecorder) sleep(ctx context.Context, delay time.Duration) error {
	s.delays = append(s.delays, delay)
	return ctx.Err()
}

func retryingStage(t *testing.T, replies []scriptedReply, settings map[string]string) (*compactor, *scriptedJev, *sleepRecorder) {
	t.Helper()
	server := &scriptedJev{script: replies, served: &fakeJev{answers: sessionAnswers()}}
	stage := newTestCompactor(t, server, settings)
	recorder := &sleepRecorder{}
	stage.client.sleep = recorder.sleep
	return stage, server, recorder
}

func compactSession(t *testing.T, stage *compactor) (contracts.CompactResult, *fakeHistory, error) {
	t.Helper()
	editor := &fakeHistory{history: sessionHistory(), outcome: contracts.PruneOutcome{Edited: 2, CharsBefore: 1000, CharsAfter: 0}}
	result, err := stage.Compact(t.Context(), contracts.CompactRequest{History: editor}, nil)
	return result, editor, err
}

func TestRetriesRecoverFromRateLimitAndServerErrors(t *testing.T) {
	stage, server, recorder := retryingStage(t,
		[]scriptedReply{{status: http.StatusTooManyRequests}, {status: http.StatusServiceUnavailable}},
		map[string]string{settingRetries: "3", settingRetryBackoff: "1s"})
	result, editor, err := compactSession(t, stage)
	if err != nil {
		t.Fatalf("Compact: %v", err)
	}
	if server.requests() != 3 {
		t.Fatalf("requests = %d, want 3: two refusals then the answer", server.requests())
	}
	if !slices.Equal(recorder.delays, []time.Duration{time.Second, 2 * time.Second}) {
		t.Fatalf("waits = %v, want 1s then 2s", recorder.delays)
	}
	if len(editor.pruned) != 1 || !result.Done {
		t.Fatalf("pruned = %d, done = %v, want the retried plan applied", len(editor.pruned), result.Done)
	}
}

func TestRetryAfterInSecondsSetsTheWait(t *testing.T) {
	stage, server, recorder := retryingStage(t,
		[]scriptedReply{{status: http.StatusTooManyRequests, retryAfter: "5"}},
		map[string]string{settingRetries: "1", settingRetryBackoff: "1s"})
	if _, _, err := compactSession(t, stage); err != nil {
		t.Fatalf("Compact: %v", err)
	}
	if server.requests() != 2 || !slices.Equal(recorder.delays, []time.Duration{5 * time.Second}) {
		t.Fatalf("requests = %d, waits = %v, want 2 requests and a 5s wait", server.requests(), recorder.delays)
	}
}

func TestRetryAfterAsHTTPDateSetsTheWait(t *testing.T) {
	now := time.Date(2026, time.November, 9, 10, 0, 0, 0, time.UTC)
	stage, _, recorder := retryingStage(t,
		[]scriptedReply{{status: http.StatusServiceUnavailable, retryAfter: now.Add(7 * time.Second).Format(http.TimeFormat)}},
		map[string]string{settingRetries: "1", settingRetryBackoff: "1s"})
	stage.client.now = func() time.Time { return now }
	if _, _, err := compactSession(t, stage); err != nil {
		t.Fatalf("Compact: %v", err)
	}
	if !slices.Equal(recorder.delays, []time.Duration{7 * time.Second}) {
		t.Fatalf("waits = %v, want the 7s the date gives", recorder.delays)
	}
}

func TestRetryAfterBeyondTheCapIsNotWaitedFor(t *testing.T) {
	stage, server, recorder := retryingStage(t,
		[]scriptedReply{{status: http.StatusTooManyRequests, retryAfter: "3600"}},
		map[string]string{settingRetries: "3", settingRetryBackoff: "1s"})
	result, _, err := compactSession(t, stage)
	if err != nil {
		t.Fatalf("Compact: %v", err)
	}
	if server.requests() != 1 || len(recorder.delays) != 0 {
		t.Fatalf("requests = %d, waits = %v, want the refusal to stand at once", server.requests(), recorder.delays)
	}
	if result.Done || result.Notice.Code != contracts.CodeCompactUnavailable {
		t.Fatalf("result = %+v, want the stage to step aside", result)
	}
}

func TestRetriesGiveUpAfterTheLimit(t *testing.T) {
	stage, server, recorder := retryingStage(t,
		[]scriptedReply{{status: 503}, {status: 503}, {status: 503}, {status: 503}},
		map[string]string{settingRetries: "2", settingRetryBackoff: "1s"})
	result, _, err := compactSession(t, stage)
	if err != nil {
		t.Fatalf("Compact: %v", err)
	}
	if server.requests() != 3 || !slices.Equal(recorder.delays, []time.Duration{time.Second, 2 * time.Second}) {
		t.Fatalf("requests = %d, waits = %v, want 3 requests and waits 1s, 2s", server.requests(), recorder.delays)
	}
	if result.Done || result.Notice.Code != contracts.CodeCompactUnavailable {
		t.Fatalf("result = %+v, want the stage to step aside after the last retry", result)
	}
}

func TestOtherClientErrorsAreNotRetried(t *testing.T) {
	for _, status := range []int{http.StatusBadRequest, http.StatusUnauthorized, http.StatusForbidden, http.StatusNotFound} {
		stage, server, recorder := retryingStage(t,
			[]scriptedReply{{status: status}},
			map[string]string{settingRetries: "3", settingRetryBackoff: "1s"})
		if _, _, err := compactSession(t, stage); err != nil {
			t.Fatalf("status %d: Compact: %v", status, err)
		}
		if server.requests() != 1 || len(recorder.delays) != 0 {
			t.Fatalf("status %d: requests = %d, waits = %v, want no retry", status, server.requests(), recorder.delays)
		}
	}
}

func TestRetriesStopWhenTheContextEnds(t *testing.T) {
	stage, server, _ := retryingStage(t,
		[]scriptedReply{{status: http.StatusTooManyRequests}, {status: http.StatusTooManyRequests}},
		map[string]string{settingRetries: "3", settingRetryBackoff: "1s"})
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	stage.client.sleep = func(waitCtx context.Context, _ time.Duration) error {
		cancel()
		return sleepContext(waitCtx, time.Hour)
	}
	editor := &fakeHistory{history: sessionHistory()}
	if _, err := stage.Compact(ctx, contracts.CompactRequest{History: editor}, nil); err == nil {
		t.Fatalf("Compact error = nil, want the cancelled context reported")
	}
	if server.requests() != 1 {
		t.Fatalf("requests = %d, want no request after the context ended", server.requests())
	}
}

func TestNothingIsRetriedByDefault(t *testing.T) {
	stage, server, recorder := retryingStage(t,
		[]scriptedReply{{status: http.StatusTooManyRequests}},
		nil)
	if _, _, err := compactSession(t, stage); err != nil {
		t.Fatalf("Compact: %v", err)
	}
	if server.requests() != 1 || len(recorder.delays) != 0 {
		t.Fatalf("requests = %d, waits = %v, want the default to send once", server.requests(), recorder.delays)
	}
}

func TestBackoffDoublesAndStopsAtTheCap(t *testing.T) {
	cases := []struct {
		base    time.Duration
		attempt int
		want    time.Duration
	}{
		{time.Second, 0, time.Second},
		{time.Second, 3, 8 * time.Second},
		{time.Second, 10, maxRetryDelay},
		{time.Minute, 0, maxRetryDelay},
	}
	for _, tc := range cases {
		if got := backoffDelay(tc.base, tc.attempt); got != tc.want {
			t.Errorf("backoffDelay(%v, %d) = %v, want %v", tc.base, tc.attempt, got, tc.want)
		}
	}
}

func TestParseRetryAfterReadsSecondsAndDates(t *testing.T) {
	now := time.Date(2026, time.November, 9, 10, 0, 0, 0, time.UTC)
	cases := map[string]time.Duration{
		"":     0,
		"12":   12 * time.Second,
		"-4":   0,
		"soon": 0,
		now.Add(9 * time.Second).Format(http.TimeFormat): 9 * time.Second,
		now.Add(-time.Minute).Format(http.TimeFormat):    0,
	}
	for value, want := range cases {
		if got := parseRetryAfter(value, now); got != want {
			t.Errorf("parseRetryAfter(%q) = %v, want %v", value, got, want)
		}
	}
}

func TestRetrySettingsAreReadAndBounded(t *testing.T) {
	opts, err := parseOptions(contracts.PluginConfig{Settings: map[string]string{settingAPIKey: "key", settingRetries: "4", settingRetryBackoff: "250ms"}})
	if err != nil {
		t.Fatalf("parseOptions: %v", err)
	}
	if opts.retries != 4 || opts.retryBackoff != 250*time.Millisecond {
		t.Fatalf("retries = %d, backoff = %v, want 4 and 250ms", opts.retries, opts.retryBackoff)
	}
	for key, value := range map[string]string{settingRetries: "11", settingRetryBackoff: "0s"} {
		if _, err := parseOptions(contracts.PluginConfig{Settings: map[string]string{settingAPIKey: "key", key: value}}); err == nil {
			t.Errorf("parseOptions(%s=%q) error = nil, want a refused setting", key, value)
		}
	}
}
