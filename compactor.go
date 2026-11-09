package jev

import (
	"context"
	"log/slog"
	"math"
	"net/http"
	"strings"
	"sync"

	contracts "github.com/Herrscherd/herrscher-contracts"
)

const (
	percent          = 100
	questionsPerCall = 2
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
	opts   options
	client *jevClient
	texts  *messages
}

func newCompactor(cfg contracts.PluginConfig, texts *messages, httpClient *http.Client) (*compactor, error) {
	opts, err := parseOptions(cfg)
	if err != nil {
		return nil, err
	}
	return &compactor{
		opts:   opts,
		client: &jevClient{http: refusingClient(httpClient, opts.timeout), endpoint: opts.endpoint, apiKey: opts.apiKey, model: opts.model},
		texts:  texts,
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
	report, err := c.plan(ctx, history, req.Instruction)
	run.examine(report)
	if err != nil {
		if ctx.Err() != nil {
			return run.finish(contracts.CompactResult{}, contracts.CompactFailed), &unavailableError{cause: err}
		}
		return run.finish(c.fallback(err), contracts.CompactFailed), nil
	}
	if len(report.edits) == 0 {
		return run.finish(contracts.CompactResult{Note: c.texts.english(noteNothingToPrune, nil)}, contracts.CompactSkipped), nil
	}
	if c.opts.dryRun {
		run.reduce(report, before, before-report.removed.runes)
		note := c.texts.english(noteDryRun, map[string]any{"Count": len(report.edits)})
		return run.finish(contracts.CompactResult{Note: note}, contracts.CompactDryRun), nil
	}
	outcome, err := req.History.PruneHistory(ctx, report.edits)
	if err != nil {
		return run.finish(contracts.CompactResult{}, contracts.CompactFailed), &unavailableError{cause: err}
	}
	return c.result(run, report, outcome, textRunes(history)), nil
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

func (c *compactor) result(run *compactRun, report planReport, outcome contracts.PruneOutcome, unchangedRunes int) contracts.CompactResult {
	if outcome.Edited == 0 {
		return run.finish(contracts.CompactResult{Note: c.texts.english(noteNothingToPrune, nil)}, contracts.CompactSkipped)
	}
	run.reduce(report, outcome.CharsBefore, outcome.CharsAfter)
	reduction := reductionOf(outcome, unchangedRunes)
	params := map[string]any{
		"Count":     run.stats.Calls,
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
	return run.finish(contracts.CompactResult{
		Note:   c.texts.english(key, params),
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

type sentUsage struct {
	requests int
	tokens   int
}

func (u sentUsage) plus(other sentUsage) sentUsage {
	return sentUsage{requests: u.requests + other.requests, tokens: u.tokens + other.tokens}
}

func (c *compactor) askAll(ctx context.Context, state compactionState, batches [][]toolCall) (map[string]callAnswer, sentUsage, error) {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	results := make([]map[string]callAnswer, len(batches))
	sent := make([]sentUsage, len(batches))
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
			answers, usage, err := c.askBatch(ctx, state, batch)
			sent[index] = usage
			if err != nil {
				firstFail.Do(func() {
					failure = err
					cancel()
				})
				return
			}
			results[index] = answers
		})
	}
	wg.Wait()
	var total sentUsage
	for _, usage := range sent {
		total = total.plus(usage)
	}
	if failure != nil {
		return nil, total, failure
	}
	merged := make(map[string]callAnswer)
	for _, answers := range results {
		for ref, answer := range answers {
			merged[ref] = answer
		}
	}
	return merged, total, nil
}

func (c *compactor) askBatch(ctx context.Context, state compactionState, batch []toolCall) (map[string]callAnswer, sentUsage, error) {
	questions := make(map[string]question, questionsPerCall*len(batch))
	for _, call := range batch {
		for name, asked := range questionsFor(call) {
			questions[name] = asked
		}
	}
	body, err := c.client.body(state, questions)
	if err != nil {
		return nil, sentUsage{}, err
	}
	usage := sentUsage{requests: 1, tokens: estimateTokens(body)}
	answers, err := c.client.post(ctx, body)
	if err != nil {
		return nil, usage, err
	}
	decided := make(map[string]callAnswer, len(batch))
	for _, call := range batch {
		keepCall, err := noul(answers, callQuestionPrefix+call.ref)
		if err != nil {
			return nil, usage, err
		}
		keepResult, err := noul(answers, resultQuestionPrefix+call.ref)
		if err != nil {
			return nil, usage, err
		}
		decided[call.ref] = callAnswer{keepCall: keepCall, keepResult: keepResult}
	}
	return decided, usage, nil
}
