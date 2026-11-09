package jev

import (
	"context"
	"errors"
	"log/slog"
	"math"
	"net/http"
	"strings"
	"sync"
	"time"

	contracts "github.com/Herrscherd/herrscher-contracts"
)

const (
	percent          = 100
	questionsPerCall = 2
	countParam       = "Count"
)

var (
	_ contracts.Compactor   = (*compactor)(nil)
	_ contracts.NoticeError = (*unavailableError)(nil)
)

type unavailableError struct {
	cause error
}

func (e *unavailableError) Error() string { return "jev compaction: " + e.cause.Error() }

func (e *unavailableError) Unwrap() error { return e.cause }

func (e *unavailableError) Notice() contracts.Notice {
	return unavailableNotice(failureReason(e.cause))
}

func failureReason(cause error) string {
	reason, _, _ := strings.Cut(cause.Error(), "\n")
	return reason
}

func unavailableNotice(reason string) contracts.Notice {
	return contracts.Notice{
		Code: contracts.CodeCompactUnavailable,
		Args: []contracts.Arg{contracts.Text("Reason", reason)},
	}
}

type compactor struct {
	opts     options
	client   *jevClient
	texts    *messages
	cache    *keepCache
	settings map[string]string
}

func newCompactor(cfg contracts.PluginConfig, texts *messages, httpClient *http.Client) (*compactor, error) {
	opts, err := parseOptions(cfg)
	if err != nil {
		return nil, err
	}
	var cache *keepCache
	if opts.incremental {
		cache = newKeepCache()
	}
	return &compactor{
		opts:     opts,
		cache:    cache,
		settings: effectiveSettings(cfg, texts),
		client: &jevClient{
			http:     refusingClient(httpClient, opts.timeout),
			endpoint: opts.endpoint,
			apiKey:   opts.apiKey,
			model:    opts.model,
			retries:  opts.retries,
			backoff:  opts.retryBackoff,
			now:      time.Now,
			sleep:    sleepContext,
		},
		texts: texts,
	}, nil
}

func (c *compactor) Compact(ctx context.Context, req contracts.CompactRequest, _ func(contracts.BackendEvent)) (contracts.CompactResult, error) {
	run := newCompactRun()
	if req.History == nil {
		return run.finish(contracts.CompactResult{Note: c.texts.english(noteNoHistory, nil)}, contracts.CompactSkipped), nil
	}
	history, err := req.History.ReadHistory(ctx)
	if err != nil {
		return run.finish(contracts.CompactResult{}, contracts.CompactFailed), &unavailableError{cause: err}
	}
	before := toolRunes(history)
	run.chars(before, before)
	journal := openDecisionLog(c.opts.decisionLog)
	report, err := c.plan(ctx, history, req, journal)
	run.examine(report)
	c.capture(history, report)
	if err != nil {
		if ctx.Err() != nil {
			return run.finish(contracts.CompactResult{}, contracts.CompactFailed), &unavailableError{cause: err}
		}
		return run.finish(c.fallback(err), contracts.CompactFailed), nil
	}
	if report.outOfReach {
		return c.reverted(run, report, noteOutOfReach, report.ceiling), nil
	}
	if len(report.edits) == 0 {
		if report.capped {
			return run.finish(contracts.CompactResult{Note: c.texts.english(noteSpendCapped, spendParams(report))}, contracts.CompactSkipped), nil
		}
		return run.finish(contracts.CompactResult{Note: c.noteWith(report, noteNothingToPrune, nil)}, contracts.CompactSkipped), nil
	}
	if planned, short := c.shortOfTarget(report, history); short {
		return c.reverted(run, report, noteReverted, planned), nil
	}
	if c.opts.dryRun {
		run.reduce(report, before, before-report.removed.runes)
		note := c.noteWith(report, noteDryRun, map[string]any{countParam: len(report.edits)})
		return run.finish(contracts.CompactResult{Note: note}, contracts.CompactDryRun), nil
	}
	outcome, err := req.History.PruneHistory(ctx, report.edits)
	if err != nil {
		return run.finish(contracts.CompactResult{}, contracts.CompactFailed), &unavailableError{cause: err}
	}
	reduction, estimated := c.reductionMeasured(ctx, req.History, outcome, history, report)
	return c.result(run, report, outcome, reduction, estimated), nil
}

func (c *compactor) reductionMeasured(ctx context.Context, editor contracts.HistoryEditor, outcome contracts.PruneOutcome, history contracts.History, report planReport) (float64, bool) {
	if c.opts.measureUnit() != unitEstimated {
		return reductionOf(outcome, textRunes(history)), false
	}
	after, err := editor.ReadHistory(ctx)
	if err != nil {
		slog.Warn("jev: history not read after the prune, the planned reduction is reported", "err", err)
		return c.plannedShare(report, history), true
	}
	unit := c.opts.measureUnit()
	return shareOf(unit.historySize(history)-unit.historySize(after), unit.historySize(history)), false
}

func (c *compactor) reverted(run *compactRun, report planReport, key string, reduction float64) contracts.CompactResult {
	slog.Debug("jev: short of the target, history left as it was", "reason", key, "reduction", reduction, "target", c.opts.minReduction)
	params := map[string]any{"Percent": wholePercent(reduction), "Target": wholePercent(c.opts.minReduction)}
	return run.finish(contracts.CompactResult{Note: c.noteWith(report, key, params)}, contracts.CompactSkipped)
}

func (c *compactor) noteWith(report planReport, key string, params map[string]any) string {
	note := c.texts.english(key, params)
	if report.cached > 0 {
		note += ". " + c.texts.english(noteCacheHits, map[string]any{countParam: report.cached})
	}
	if report.state.tokens > 0 {
		note += ". " + c.texts.english(noteState, map[string]any{"Tokens": report.state.tokens, "Stage": report.state.stage})
	}
	if !report.capped {
		return note
	}
	return note + " " + c.texts.english(noteSpendCapped, spendParams(report))
}

func spendParams(report planReport) map[string]any {
	return map[string]any{"Requests": report.sent.requests, "Tokens": report.sent.tokens}
}

func (c *compactor) fallback(cause error) contracts.CompactResult {
	reason := failureReason(cause)
	slog.Warn("jev: compaction skipped, the next stage runs", "reason", reason)
	return contracts.CompactResult{
		Note:   c.texts.english(noteFailed, map[string]any{"Reason": reason}),
		Notice: unavailableNotice(reason),
	}
}

func textRunes(history contracts.History) int {
	total := 0
	for index := range history.Messages {
		total += runeCount(history.Messages[index].Text)
	}
	return total
}

func (c *compactor) result(run *compactRun, report planReport, outcome contracts.PruneOutcome, reduction float64, estimated bool) contracts.CompactResult {
	if outcome.Edited == 0 {
		return run.finish(contracts.CompactResult{Note: c.noteWith(report, noteNothingToPrune, nil)}, contracts.CompactSkipped)
	}
	run.reduce(report, outcome.CharsBefore, outcome.CharsAfter)
	params := map[string]any{
		countParam:  run.stats.Calls,
		"Kept":      run.kept(),
		"Truncated": run.stats.Truncated,
		"Pruned":    run.stats.Pruned,
		"Percent":   wholePercent(reduction),
	}
	key, done := notePruned, true
	if reduction < c.opts.minReduction {
		key, done = notePrunedShort, false
		params["Target"] = wholePercent(c.opts.minReduction)
		if c.opts.shortRule() == onShortDone {
			key, done = notePrunedStopped, true
		}
	}
	ended := contracts.CompactShort
	if done {
		ended = contracts.CompactDone
	}
	note := c.noteWith(report, key, params)
	if estimated {
		note += ". " + c.texts.english(noteEstimated, nil)
	}
	return run.finish(contracts.CompactResult{
		Note:   note,
		Done:   done,
		Notice: contracts.Notice{Code: contracts.CodeCompacted},
	}, ended)
}

func reductionOf(outcome contracts.PruneOutcome, unchangedRunes int) float64 {
	before := outcome.CharsBefore + unchangedRunes
	if before <= 0 {
		return 0
	}
	return float64(outcome.CharsBefore-outcome.CharsAfter) / float64(before)
}

func wholePercent(ratio float64) int {
	return int(math.Floor(ratio * percent))
}

func shareOf(part, whole int) float64 {
	if whole <= 0 {
		return 0
	}
	return float64(part) / float64(whole)
}

type sentUsage struct {
	requests int
	tokens   int
}

func (u sentUsage) plus(other sentUsage) sentUsage {
	return sentUsage{requests: u.requests + other.requests, tokens: u.tokens + other.tokens}
}

func (c *compactor) askAll(ctx context.Context, state compactionState, batches [][]toolCall, spend *spendLedger) (map[string]callAnswer, error) {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	results := make([]map[string]callAnswer, len(batches))
	slots := make(chan struct{}, c.opts.parallelism())
	var (
		wg        sync.WaitGroup
		firstFail sync.Once
		failure   error
	)
	for index, batch := range batches {
		wg.Go(func() {
			slots <- struct{}{}
			defer func() { <-slots }()
			decided, batchErr := c.askBatch(ctx, state, batch, spend)
			if errors.Is(batchErr, ErrSpendCapped) {
				return
			}
			if batchErr != nil {
				firstFail.Do(func() {
					failure = batchErr
					cancel()
				})
				return
			}
			results[index] = decided
		})
	}
	wg.Wait()
	if failure != nil {
		return nil, failure
	}
	answers := make(map[string]callAnswer)
	for _, decided := range results {
		for ref, answer := range decided {
			answers[ref] = answer
		}
	}
	return answers, nil
}

func (c *compactor) askBatch(ctx context.Context, state compactionState, batch []toolCall, spend *spendLedger) (map[string]callAnswer, error) {
	questions := make(map[string]question, questionsPerCall*len(batch))
	for _, call := range batch {
		for name, asked := range questionsFor(call) {
			questions[name] = asked
		}
	}
	body, err := c.client.body(state, questions)
	if err != nil {
		return nil, err
	}
	answers, err := c.client.post(ctx, body, spend)
	if err != nil {
		return nil, err
	}
	decided := make(map[string]callAnswer, len(batch))
	for _, call := range batch {
		keepCall, err := noul(answers, callQuestionPrefix+call.ref)
		if err != nil {
			return nil, err
		}
		keepResult, err := noul(answers, resultQuestionPrefix+call.ref)
		if err != nil {
			return nil, err
		}
		decided[call.ref] = callAnswer{keepCall: keepCall, keepResult: keepResult}
	}
	return decided, nil
}
