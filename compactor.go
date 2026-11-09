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
	if req.History == nil {
		return contracts.CompactResult{Note: c.texts.english(noteNoHistory, nil)}, nil
	}
	history, err := req.History.ReadHistory(ctx)
	if err != nil {
		return contracts.CompactResult{}, &unavailableError{cause: err}
	}
	edits, err := c.plan(ctx, history, req.Instruction)
	if err != nil {
		if ctx.Err() != nil {
			return contracts.CompactResult{}, &unavailableError{cause: err}
		}
		return c.fallback(err), nil
	}
	if len(edits) == 0 {
		return contracts.CompactResult{Note: c.texts.english(noteNothingToPrune, nil)}, nil
	}
	if c.opts.dryRun {
		return contracts.CompactResult{Note: c.texts.english(noteDryRun, map[string]any{"Count": len(edits)})}, nil
	}
	outcome, err := req.History.PruneHistory(ctx, edits)
	if err != nil {
		return contracts.CompactResult{}, &unavailableError{cause: err}
	}
	return c.result(outcome, textRunes(history)), nil
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

func (c *compactor) result(outcome contracts.PruneOutcome, unchangedRunes int) contracts.CompactResult {
	if outcome.Edited == 0 {
		return contracts.CompactResult{Note: c.texts.english(noteNothingToPrune, nil)}
	}
	reduction := reductionOf(outcome, unchangedRunes)
	params := map[string]any{"Count": outcome.Edited, "Percent": wholePercent(reduction)}
	key, done := notePruned, true
	if reduction < c.opts.minReduction {
		key, done = notePrunedShort, false
		params["Target"] = wholePercent(c.opts.minReduction)
		if c.opts.shortRule() == onShortDone {
			key, done = notePrunedStopped, true
		}
	}
	return contracts.CompactResult{
		Note:   c.texts.english(key, params),
		Done:   done,
		Notice: contracts.Notice{Code: contracts.CodeCompacted},
	}
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

func (c *compactor) askAll(ctx context.Context, state compactionState, batches [][]toolCall) (map[string]callAnswer, error) {
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
			answers, err := c.askBatch(ctx, state, batch)
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
	if failure != nil {
		return nil, failure
	}
	merged := make(map[string]callAnswer)
	for _, answers := range results {
		for ref, answer := range answers {
			merged[ref] = answer
		}
	}
	return merged, nil
}

func (c *compactor) askBatch(ctx context.Context, state compactionState, batch []toolCall) (map[string]callAnswer, error) {
	questions := make(map[string]question, questionsPerCall*len(batch))
	for _, call := range batch {
		for name, asked := range questionsFor(call) {
			questions[name] = asked
		}
	}
	answers, err := c.client.ask(ctx, state, questions)
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
