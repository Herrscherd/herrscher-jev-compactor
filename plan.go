package jev

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"

	contracts "github.com/Herrscherd/herrscher-contracts"
)

const (
	requestOverheadTokens = 20
	truncateSlackRunes    = 120
	questionType          = "noul"
	callQuestionPrefix    = "call_"
	resultQuestionPrefix  = "result_"
	prunedResultNote      = "[herrscher pruned the rest of this result: %d characters]"
	prunedCallNote        = "[herrscher pruned the result of this call]"
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

const (
	reasonPinned    = "pinned"
	reasonRule      = "rule"
	reasonThreshold = "threshold"
	reasonBudget    = "budget"
	reasonError     = "error"
	reasonSpendCap  = "spend_cap"
	reasonCache     = "cache"
	reasonShort     = "short"
)

func (d decision) String() string {
	switch d {
	case decisionKeep:
		return "keep"
	case decisionPruneResult:
		return "prune_result"
	case decisionPruneCall:
		return "prune_call"
	}
	return ""
}

func logDecision(journal *decisionLog, call toolCall, verdict decision, reason string, attrs ...any) {
	fields := []any{"ref", call.ref, "tool", call.source.Tool, "verdict", verdict.String(), "reason", reason, "result_size", call.resultRunes()}
	fields = append(fields, attrs...)
	slog.Debug(decisionLogEvent, fields...)
	journal.write(fields)
}

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

type truncation struct {
	head int
	tail int
}

func (o options) truncationFor(picked rule) truncation {
	return truncation{head: picked.head.or(o.truncateHead), tail: picked.tail.or(o.truncateTail)}
}

func editFor(call toolCall, verdict decision, cut truncation) (contracts.HistoryEdit, bool) {
	switch verdict {
	case decisionKeep:
		return contracts.HistoryEdit{}, false
	case decisionPruneResult:
		if call.resultRunes() <= cut.head+cut.tail+truncateSlackRunes {
			return contracts.HistoryEdit{}, false
		}
		return contracts.HistoryEdit{CallID: call.source.ID, Action: contracts.PruneResult, KeepChars: cut.head, KeepTail: cut.tail}, true
	case decisionPruneCall:
		return contracts.HistoryEdit{CallID: call.source.ID, Action: contracts.PruneCall}, true
	}
	return contracts.HistoryEdit{}, false
}

func removedText(call toolCall, edit contracts.HistoryEdit) string {
	switch edit.Action {
	case contracts.PruneResult:
		result := call.source.Result
		head := runePrefix(result, edit.KeepChars)
		tail := runeSuffix(result, edit.KeepTail)
		if len(head)+len(tail) >= len(result) {
			return ""
		}
		return result[len(head) : len(result)-len(tail)]
	case contracts.PruneCall:
		return call.source.Result
	}
	return ""
}

func insertedText(call toolCall, edit contracts.HistoryEdit, span string) string {
	if edit.Action == contracts.PruneCall {
		return prunedCallNote
	}
	note := fmt.Sprintf(prunedResultNote, runeCount(span))
	if runePrefix(call.source.Result, edit.KeepChars) == "" {
		return note
	}
	return "\n" + note
}

func removalOf(call toolCall, edit contracts.HistoryEdit) removal {
	span := removedText(call, edit)
	inserted := insertedText(call, edit, span)
	return removal{
		runes:  max(0, runeCount(span)-runeCount(inserted)),
		tokens: max(0, estimateTokens(span)-estimateTokens(inserted)),
	}
}

type removal struct {
	runes  int
	tokens int
}

func removedTotals(calls []toolCall, edits []contracts.HistoryEdit) removal {
	var total removal
	byID := make(map[string]toolCall, len(calls))
	for _, call := range calls {
		byID[call.source.ID] = call
	}
	for _, edit := range edits {
		net := removalOf(byID[edit.CallID], edit)
		total.runes += net.runes
		total.tokens += net.tokens
	}
	return total
}

func toolRunes(history contracts.History) int {
	total := 0
	for index := range history.Messages {
		for _, call := range history.Messages[index].Calls {
			total += runeCount(call.Input) + runeCount(call.Result)
		}
	}
	return total
}

func (o options) thresholdsFor(picked rule) thresholds {
	return thresholds{
		call:   picked.callKeep.or(picked.threshold.or(o.callKeepThreshold())),
		result: picked.resultKeep.or(picked.threshold.or(o.resultKeepThreshold())),
	}
}

type planReport struct {
	edits      []contracts.HistoryEdit
	calls      int
	pinned     int
	removed    removal
	sent       sentUsage
	ceiling    float64
	outOfReach bool
	capped     bool
	cached     int
	unanswered int
	state      stateReport
	goal       string
	judgments  []Judgment
}

type stateReport struct {
	tokens int
	stage  string
}

type answerFunc func(ctx context.Context, calls, asked []toolCall) (answerSet, error)

type answerSet struct {
	answers map[string]callAnswer
	sent    sentUsage
	capped  bool
	state   stateReport
}

func (c *compactor) plan(ctx context.Context, history contracts.History, req contracts.CompactRequest, journal *decisionLog) (planReport, error) {
	goal := c.opts.goalSettings().goal(history, req.Instruction)
	return c.decide(ctx, history, req.Session, goal, c.liveAnswers(history, goal), journal)
}

func (c *compactor) liveAnswers(history contracts.History, goal string) answerFunc {
	return func(ctx context.Context, calls, asked []toolCall) (answerSet, error) {
		spend := newSpendLedger(c.opts.maxRequests, c.opts.maxSentTokens)
		answers, fitted, err := c.judge(ctx, history, calls, asked, goal, spend)
		return answerSet{
			answers: answers,
			sent:    spend.total(),
			capped:  spend.capped(),
			state:   stateReport{tokens: fitted.tokens, stage: fitted.stage},
		}, err
	}
}

func (c *compactor) decide(ctx context.Context, history contracts.History, session contracts.SessionInfo, goal string, answer answerFunc, journal *decisionLog) (planReport, error) {
	calls := collectCallsWith(history, c.opts.preservation(), c.opts.includeEmpty)
	report := planReport{calls: len(calls), pinned: c.pinnedCount(calls), goal: goal}
	if c.opts.revertsShort() {
		report.ceiling = c.reductionCeiling(calls, history)
		if report.ceiling < c.opts.minReduction {
			report.outOfReach = true
			return report, nil
		}
	}
	decided, unknown := c.decideLocally(calls, journal)
	scope := c.scopeFor(session, goal)
	asked := scope.partition(unknown, journal)
	report.cached = len(unknown) - len(asked)
	if len(asked) > 0 {
		set, err := answer(ctx, calls, asked)
		report.sent = set.sent
		report.capped = set.capped
		report.state = set.state
		report.judgments = judgmentsOf(asked, set.answers)
		report.unanswered = len(asked) - len(report.judgments)
		if err != nil {
			return report, err
		}
		scope.remember(c.decideAsked(asked, set.answers, decided, journal))
	}
	report.edits = c.withinBudget(history, calls, decided, journal)
	report.removed = removedTotals(calls, report.edits)
	return report, nil
}

func judgmentsOf(asked []toolCall, answers map[string]callAnswer) []Judgment {
	var judgments []Judgment
	for _, call := range asked {
		if answer, answered := answers[call.ref]; answered {
			judgments = append(judgments, Judgment{CallID: call.source.ID, PCall: answer.keepCall, PResult: answer.keepResult})
		}
	}
	return judgments
}

func (c *compactor) shortOfTarget(report planReport, history contracts.History) (float64, bool) {
	if !c.opts.revertsShort() {
		return 0, false
	}
	planned := c.plannedShare(report, history)
	return planned, planned < c.opts.minReduction
}

func (c *compactor) reductionCeiling(calls []toolCall, history contracts.History) float64 {
	unit := c.opts.measureUnit()
	removable := 0
	for _, call := range calls {
		if !c.kept(call) {
			removable += unit.size(call.source.Input) + unit.size(call.source.Result)
		}
	}
	return shareOf(removable, unit.historySize(history))
}

func (c *compactor) plannedShare(report planReport, history contracts.History) float64 {
	unit := c.opts.measureUnit()
	return shareOf(report.removed.in(unit), unit.historySize(history))
}

func (c *compactor) pinnedCount(calls []toolCall) int {
	count := 0
	for _, call := range calls {
		if c.isPinned(call) {
			count++
		}
	}
	return count
}

func (c *compactor) decideLocally(calls []toolCall, journal *decisionLog) (map[string]plannedEdit, []toolCall) {
	decided := make(map[string]plannedEdit)
	var asked []toolCall
	for _, call := range calls {
		if c.kept(call) {
			logDecision(journal, call, decisionKeep, c.keptReason(call))
			continue
		}
		picked := c.opts.policy.ruleFor(call.source.Tool)
		if picked.action == 0 {
			asked = append(asked, call)
			continue
		}
		verdict, flipped := c.keepErrorsVerdict(call, picked.action.decision())
		logDecision(journal, call, verdict, reasonFor(flipped, reasonRule), "rule", strings.Join(picked.patterns, ","))
		if edit, ok := c.verdictEdit(call, verdict, c.opts.truncationFor(picked), ruleDecided); ok {
			decided[call.ref] = edit
		}
	}
	return decided, asked
}

func (c *compactor) decideAsked(asked []toolCall, answers map[string]callAnswer, decided map[string]plannedEdit, journal *decisionLog) []toolCall {
	var kept []toolCall
	for _, call := range asked {
		answer, answered := answers[call.ref]
		if !answered {
			logDecision(journal, call, decisionKeep, reasonSpendCap)
			continue
		}
		picked := c.opts.policy.ruleFor(call.source.Tool)
		limits := c.opts.thresholdsFor(picked)
		verdict, flipped := c.keepErrorsVerdict(call, decideWith(call, answer, limits))
		logDecision(journal, call, verdict, reasonFor(flipped, reasonThreshold),
			"p_call", answer.keepCall, "p_result", answer.keepResult,
			"call_threshold", limits.call, "result_threshold", limits.result)
		if verdict == decisionKeep {
			kept = append(kept, call)
			continue
		}
		keep := max(answer.keepCall, answer.keepResult)
		if edit, ok := c.verdictEdit(call, verdict, c.opts.truncationFor(picked), keep); ok {
			decided[call.ref] = edit
		}
	}
	return kept
}

func reasonFor(flipped bool, reason string) string {
	if flipped {
		return reasonError
	}
	return reason
}

func (c *compactor) isPinned(call toolCall) bool {
	return call.pinned || c.opts.policy.pinned(call.source.Input)
}

func (c *compactor) keptReason(call toolCall) string {
	if c.isPinned(call) {
		return reasonPinned
	}
	return reasonShort
}

func (c *compactor) kept(call toolCall) bool {
	return c.isPinned(call) || call.resultRunes() < c.opts.minResultChars
}

func (c *compactor) judge(ctx context.Context, history contracts.History, calls, asked []toolCall, goal string, spend *spendLedger) (map[string]callAnswer, fittedState, error) {
	fitted, err := fitState(history, calls, goal, c.opts)
	if err != nil {
		return nil, fittedState{}, err
	}
	slog.Debug("jev: state fitted", "stage", fitted.stage, "tokens", fitted.tokens)
	batches, err := batchCalls(asked, fitted.tokens, c.opts.maxRequestTokens)
	if err != nil {
		return nil, fitted, err
	}
	answers, err := c.askAll(ctx, fitted.state, batches, spend)
	return answers, fitted, err
}

func (c *compactor) keepErrorsVerdict(call toolCall, verdict decision) (decision, bool) {
	if c.opts.keepErrors && call.source.ResultIsError && verdict == decisionPruneCall {
		return decisionPruneResult, true
	}
	return verdict, false
}

func (c *compactor) verdictEdit(call toolCall, verdict decision, cut truncation, keep float64) (plannedEdit, bool) {
	edit, prune := editFor(call, verdict, cut)
	if !prune {
		return plannedEdit{}, false
	}
	return plannedEdit{edit: edit, keep: keep, removed: removalOf(call, edit).in(c.opts.measureUnit())}, true
}

func (c *compactor) withinBudget(history contracts.History, calls []toolCall, decided map[string]plannedEdit, journal *decisionLog) []contracts.HistoryEdit {
	var planned []plannedEdit
	for _, call := range calls {
		if edit, ok := decided[call.ref]; ok {
			planned = append(planned, edit)
		}
	}
	edits := fitBudget(planned, c.opts.budget(), c.opts.measureUnit().historySize(history))
	c.logBudgetDemotions(calls, decided, edits, journal)
	return edits
}

func (c *compactor) logBudgetDemotions(calls []toolCall, decided map[string]plannedEdit, edits []contracts.HistoryEdit, journal *decisionLog) {
	kept := make(map[string]bool, len(edits))
	for _, edit := range edits {
		kept[edit.CallID] = true
	}
	for _, call := range calls {
		if _, ok := decided[call.ref]; ok && !kept[call.source.ID] {
			logDecision(journal, call, decisionKeep, reasonBudget)
		}
	}
}
