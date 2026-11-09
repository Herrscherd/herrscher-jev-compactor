package jev

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"testing"

	contracts "github.com/Herrscherd/herrscher-contracts"
)

type unreadableHistory struct{}

func (unreadableHistory) ReadHistory(context.Context) (contracts.History, error) {
	return contracts.History{}, errors.New("history store down")
}

func (unreadableHistory) PruneHistory(context.Context, []contracts.HistoryEdit) (contracts.PruneOutcome, error) {
	return contracts.PruneOutcome{}, nil
}

type refusedPrune struct {
	fakeHistory
}

func (*refusedPrune) PruneHistory(context.Context, []contracts.HistoryEdit) (contracts.PruneOutcome, error) {
	return contracts.PruneOutcome{}, errors.New("prune refused")
}

func TestCompactStatsReportTheOutcomeOfEachPath(t *testing.T) {
	before := toolRunes(sessionHistory())
	cases := map[string]struct {
		server   http.Handler
		settings map[string]string
		history  contracts.HistoryEditor
		cancel   bool
		wantErr  bool
		want     contracts.CompactStats
	}{
		"done": {
			server:  &fakeJev{answers: sessionAnswers()},
			history: &fakeHistory{history: sessionHistory(), outcome: contracts.PruneOutcome{Edited: 2, CharsBefore: 1000, CharsAfter: 600}},
			want: contracts.CompactStats{
				Stage: compactorKind, Outcome: contracts.CompactDone, Calls: 5, Kept: 3, Truncated: 1, Pruned: 1,
				Pinned: 1, CharsBefore: 1000, CharsAfter: 600, Requests: 1,
			},
		},
		"short keeps the stack going": {
			server:  &fakeJev{answers: sessionAnswers()},
			history: &fakeHistory{history: sessionHistory(), outcome: contracts.PruneOutcome{Edited: 1, CharsBefore: 1000, CharsAfter: 900}},
			want: contracts.CompactStats{
				Stage: compactorKind, Outcome: contracts.CompactShort, Calls: 5, Kept: 3, Truncated: 1, Pruned: 1,
				Pinned: 1, CharsBefore: 1000, CharsAfter: 900, Requests: 1,
			},
		},
		"short stops the stack when on_short is done": {
			server:   &fakeJev{answers: sessionAnswers()},
			settings: map[string]string{settingOnShort: "done"},
			history:  &fakeHistory{history: sessionHistory(), outcome: contracts.PruneOutcome{Edited: 1, CharsBefore: 1000, CharsAfter: 900}},
			want: contracts.CompactStats{
				Stage: compactorKind, Outcome: contracts.CompactDone, Calls: 5, Kept: 3, Truncated: 1, Pruned: 1,
				Pinned: 1, CharsBefore: 1000, CharsAfter: 900, Requests: 1,
			},
		},
		"dry run changes nothing and reports the projection": {
			server:   &fakeJev{answers: sessionAnswers()},
			settings: map[string]string{settingDryRun: "true"},
			history:  &fakeHistory{history: sessionHistory()},
			want: contracts.CompactStats{
				Stage: compactorKind, Outcome: contracts.CompactDryRun, Calls: 5, Kept: 3, Truncated: 1, Pruned: 1,
				Pinned: 1, CharsBefore: before, CharsAfter: before - 2700, Requests: 1,
			},
		},
		"skipped when every call is pinned": {
			server:   &fakeJev{},
			settings: map[string]string{settingPreserveRecent: "10"},
			history:  &fakeHistory{history: sessionHistory()},
			want: contracts.CompactStats{
				Stage: compactorKind, Outcome: contracts.CompactSkipped, Calls: 5, Kept: 5, Pinned: 5,
				CharsBefore: before, CharsAfter: before,
			},
		},
		"skipped without a history": {
			server: &fakeJev{},
			want:   contracts.CompactStats{Stage: compactorKind, Outcome: contracts.CompactSkipped},
		},
		"failed when Jev refuses": {
			server:  &fakeJev{status: http.StatusUnauthorized},
			history: &fakeHistory{history: sessionHistory()},
			want: contracts.CompactStats{
				Stage: compactorKind, Outcome: contracts.CompactFailed, Calls: 5, Kept: 5, Pinned: 1,
				CharsBefore: before, CharsAfter: before, Requests: 1,
			},
		},
		"failed when the history cannot be read": {
			server:  &fakeJev{},
			history: unreadableHistory{},
			wantErr: true,
			want:    contracts.CompactStats{Stage: compactorKind, Outcome: contracts.CompactFailed},
		},
		"failed when the pruning is refused": {
			server:  &fakeJev{answers: sessionAnswers()},
			history: &refusedPrune{fakeHistory: fakeHistory{history: sessionHistory()}},
			wantErr: true,
			want: contracts.CompactStats{
				Stage: compactorKind, Outcome: contracts.CompactFailed, Calls: 5, Kept: 5, Pinned: 1,
				CharsBefore: before, CharsAfter: before, Requests: 1,
			},
		},
		"failed when the context is cancelled": {
			server:  &fakeJev{answers: sessionAnswers()},
			history: &fakeHistory{history: sessionHistory()},
			cancel:  true,
			wantErr: true,
			want: contracts.CompactStats{
				Stage: compactorKind, Outcome: contracts.CompactFailed, Calls: 5, Kept: 5, Pinned: 1,
				CharsBefore: before, CharsAfter: before, Requests: 1,
			},
		},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			stage := newTestCompactor(t, tc.server, tc.settings)
			ctx := t.Context()
			if tc.cancel {
				var cancel context.CancelFunc
				ctx, cancel = context.WithCancel(ctx)
				cancel()
			}
			result, err := stage.Compact(ctx, contracts.CompactRequest{History: tc.history}, nil)
			if (err != nil) != tc.wantErr {
				t.Fatalf("Compact error = %v, want error %t", err, tc.wantErr)
			}
			if result.Stats == nil {
				t.Fatal("Compact returned no stats")
			}
			got := *result.Stats
			got.Duration = 0
			got.RequestTokens = 0
			got.TokensRemoved = 0
			if got != tc.want {
				t.Fatalf("stats = %+v, want %+v", got, tc.want)
			}
		})
	}
}

func TestCompactStatsMeasureTheRequestAndTheTokensRemoved(t *testing.T) {
	server := &fakeJev{answers: sessionAnswers()}
	stage := newTestCompactor(t, server, nil)
	editor := &fakeHistory{history: sessionHistory(), outcome: contracts.PruneOutcome{Edited: 2, CharsBefore: 1000, CharsAfter: 600}}
	result, err := stage.Compact(t.Context(), contracts.CompactRequest{History: editor}, nil)
	if err != nil {
		t.Fatalf("Compact: %v", err)
	}
	long := strings.Repeat("line of output\n", 100)
	wantTokensRemoved := estimateTokens(long[defaultTruncateHead:]) + estimateTokens(long)
	if got := result.Stats.TokensRemoved; got != wantTokensRemoved {
		t.Fatalf("TokensRemoved = %d, want %d: the head-cut tail plus the pruned result", got, wantTokensRemoved)
	}
	sent, err := encodeJSON(server.requests[0])
	if err != nil {
		t.Fatal(err)
	}
	if got := result.Stats.RequestTokens; got != estimateTokens(sent) {
		t.Fatalf("RequestTokens = %d, want %d: the tokens of the body sent", got, estimateTokens(sent))
	}
}
