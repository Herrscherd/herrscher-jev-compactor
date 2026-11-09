package jev

import (
	"time"

	contracts "github.com/Herrscherd/herrscher-contracts"
)

type compactRun struct {
	started time.Time
	stats   contracts.CompactStats
}

func newCompactRun() *compactRun {
	return &compactRun{started: time.Now(), stats: contracts.CompactStats{Stage: compactorKind}}
}

func (r *compactRun) finish(result contracts.CompactResult, outcome contracts.CompactOutcome) contracts.CompactResult {
	r.stats.Outcome = outcome
	r.stats.Kept = r.stats.Calls - r.stats.Truncated - r.stats.Pruned
	r.stats.Duration = time.Since(r.started)
	result.Stats = &r.stats
	return result
}

func (r *compactRun) examine(report planReport) {
	r.stats.Calls = report.calls
	r.stats.Pinned = report.pinned
	r.stats.Requests = report.sent.requests
	r.stats.RequestTokens = report.sent.tokens
}

func (r *compactRun) chars(before, after int) {
	r.stats.CharsBefore = before
	r.stats.CharsAfter = after
}

func (r *compactRun) reduce(report planReport, before, after int) {
	r.chars(before, after)
	for _, edit := range report.edits {
		switch edit.Action {
		case contracts.PruneResult:
			r.stats.Truncated++
		case contracts.PruneCall:
			r.stats.Pruned++
		}
	}
	r.stats.TokensRemoved = report.removed.tokens
}
