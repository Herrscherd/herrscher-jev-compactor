package jev

import (
	"context"
	"errors"
	"fmt"
	"log/slog"

	contracts "github.com/Herrscherd/herrscher-contracts"
)

const (
	requestOverheadTokens = 20
	truncateSlackRunes    = 120
	questionType          = "noul"
	callQuestionPrefix    = "call_"
	resultQuestionPrefix  = "result_"
)

// ErrNoRoomForQuestions is returned when the state Jev reads leaves no room in a
// request for the questions about even one tool call. Match it with errors.Is.
var ErrNoRoomForQuestions = errors.New("state leaves no room for questions")

type question struct {
	Type         string `json:"type"`
	Instructions string `json:"instructions"`
}

type callAnswer struct {
	keepCall   float64
	keepResult float64
}

type thresholds struct {
	call   float64
	result float64
}

type decision int

const (
	decisionKeep decision = iota + 1
	decisionPruneResult
	decisionPruneCall
)

func questionsFor(call toolCall) map[string]question {
	return map[string]question{
		callQuestionPrefix + call.ref: {
			Type:         questionType,
			Instructions: fmt.Sprintf("Tool call %s (%s) should stay in the history: knowing this call was made, with its input, still matters for what the assistant does next", call.ref, call.source.Tool),
		},
		resultQuestionPrefix + call.ref: {
			Type:         questionType,
			Instructions: fmt.Sprintf("The full output of tool call %s (%s, %d chars) should stay in the history verbatim: the assistant still needs its contents and re-running the tool would not do", call.ref, call.source.Tool, call.resultRunes()),
		},
	}
}

func batchCalls(candidates []toolCall, stateTokens, maxRequestTokens int) ([][]toolCall, error) {
	budget := maxRequestTokens - stateTokens - requestOverheadTokens
	var batches [][]toolCall
	var current []toolCall
	currentTokens := 0
	for _, call := range candidates {
		encoded, err := encodeJSON(questionsFor(call))
		if err != nil {
			return nil, err
		}
		tokens := estimateTokens(encoded)
		if len(current) > 0 && currentTokens+tokens > budget {
			batches = append(batches, current)
			current, currentTokens = nil, 0
		}
		if len(current) == 0 && tokens > budget {
			return nil, fmt.Errorf("%w: about %d of %d tokens", ErrNoRoomForQuestions, stateTokens, maxRequestTokens)
		}
		current = append(current, call)
		currentTokens += tokens
	}
	if len(current) > 0 {
		batches = append(batches, current)
	}
	return batches, nil
}

func decideWith(call toolCall, answer callAnswer, keep thresholds) decision {
	switch {
	case call.pinned, answer.keepResult >= keep.result:
		return decisionKeep
	case answer.keepCall >= keep.call:
		return decisionPruneResult
	default:
		return decisionPruneCall
	}
}

func editFor(call toolCall, verdict decision, truncateHead int) (contracts.HistoryEdit, bool) {
	switch verdict {
	case decisionKeep:
		return contracts.HistoryEdit{}, false
	case decisionPruneResult:
		if call.resultRunes() <= truncateHead+truncateSlackRunes {
			return contracts.HistoryEdit{}, false
		}
		return contracts.HistoryEdit{CallID: call.source.ID, Action: contracts.PruneResult, KeepChars: truncateHead}, true
	case decisionPruneCall:
		return contracts.HistoryEdit{CallID: call.source.ID, Action: contracts.PruneCall}, true
	}
	return contracts.HistoryEdit{}, false
}

func removedRunes(call toolCall, edit contracts.HistoryEdit) int {
	switch edit.Action {
	case contracts.PruneResult:
		return max(call.resultRunes()-edit.KeepChars, 0)
	case contracts.PruneCall:
		return call.resultRunes()
	}
	return 0
}

func historyRunes(history contracts.History) int {
	total := textRunes(history)
	for index := range history.Messages {
		for _, call := range history.Messages[index].Calls {
			total += runeCount(call.Input) + runeCount(call.Result)
		}
	}
	return total
}

func (o options) thresholdsFor(picked rule) thresholds {
	return thresholds{
		call:   picked.callKeep.or(o.callKeepThreshold()),
		result: picked.resultKeep.or(o.resultKeepThreshold()),
	}
}

func (c *compactor) plan(ctx context.Context, history contracts.History, instruction string) ([]contracts.HistoryEdit, error) {
	calls := collectCalls(history, c.opts.preserveRecent)
	decided, asked := c.decideLocally(calls)
	if len(asked) > 0 {
		answers, err := c.judge(ctx, history, calls, asked, instruction)
		if err != nil {
			return nil, err
		}
		c.decideAsked(asked, answers, decided)
	}
	return c.withinBudget(history, calls, decided), nil
}

func (c *compactor) decideLocally(calls []toolCall) (map[string]plannedEdit, []toolCall) {
	decided := make(map[string]plannedEdit)
	var asked []toolCall
	for _, call := range calls {
		if call.pinned || c.kept(call) {
			continue
		}
		picked := c.opts.policy.ruleFor(call.source.Tool)
		if picked.action == 0 {
			asked = append(asked, call)
			continue
		}
		if edit, ok := c.verdictEdit(call, picked.action.decision(), orDefault(picked.head, c.opts.truncateHead), ruleDecided); ok {
			decided[call.ref] = edit
		}
	}
	return decided, asked
}

func (c *compactor) decideAsked(asked []toolCall, answers map[string]callAnswer, decided map[string]plannedEdit) {
	for _, call := range asked {
		picked := c.opts.policy.ruleFor(call.source.Tool)
		answer := answers[call.ref]
		verdict := decideWith(call, answer, c.opts.thresholdsFor(picked))
		keep := max(answer.keepCall, answer.keepResult)
		if edit, ok := c.verdictEdit(call, verdict, orDefault(picked.head, c.opts.truncateHead), keep); ok {
			decided[call.ref] = edit
		}
	}
}

func (c *compactor) kept(call toolCall) bool {
	return call.resultRunes() < c.opts.minResultChars || c.opts.policy.pinned(call.source.Input)
}

func (c *compactor) judge(ctx context.Context, history contracts.History, calls, asked []toolCall, instruction string) (map[string]callAnswer, error) {
	fitted, err := fitState(history, calls, c.opts.goalSettings().goal(history, instruction), c.opts)
	if err != nil {
		return nil, err
	}
	slog.Debug("jev: state fitted", "stage", fitted.stage, "tokens", fitted.tokens)
	batches, err := batchCalls(asked, fitted.tokens, c.opts.maxRequestTokens)
	if err != nil {
		return nil, err
	}
	return c.askAll(ctx, fitted.state, batches)
}

func (c *compactor) verdictEdit(call toolCall, verdict decision, head int, keep float64) (plannedEdit, bool) {
	if c.opts.keepErrors && call.source.ResultIsError && verdict == decisionPruneCall {
		verdict = decisionPruneResult
	}
	edit, prune := editFor(call, verdict, head)
	if !prune {
		return plannedEdit{}, false
	}
	return plannedEdit{edit: edit, keep: keep, removed: removedRunes(call, edit)}, true
}

func (c *compactor) withinBudget(history contracts.History, calls []toolCall, decided map[string]plannedEdit) []contracts.HistoryEdit {
	var planned []plannedEdit
	for _, call := range calls {
		if edit, ok := decided[call.ref]; ok {
			planned = append(planned, edit)
		}
	}
	return fitBudget(planned, c.opts.budget(), historyRunes(history))
}
