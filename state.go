package jev

import (
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"

	contracts "github.com/Herrscherd/herrscher-contracts"
)

const stateContext = "A coding assistant conversation is being compacted to free context. `history` is the whole conversation so far, oldest first; tool outputs are replaced by a short `result` note and long texts may be abridged. Each question asks whether one tool call, or the full output of that call, still needs to stay in the history verbatim. Whatever is not kept is deleted permanently, but the assistant can always re-run a tool or re-read a file."

const (
	fullInputRunes  = 1000
	shortInputRunes = 200
	lineInputRunes  = 60
	textHeadRunes   = 400
	textTailRunes   = 150
)

// ErrHistoryTooLarge is returned when the history cannot be shrunk into the token
// budget of the state Jev reads. Match it with errors.Is.
var ErrHistoryTooLarge = errors.New("history too large for Jev")

type stateCall struct {
	ID     string `json:"id"`
	Tool   string `json:"tool"`
	Input  string `json:"input"`
	Result string `json:"result"`
}

type historyEntry struct {
	Index     int    `json:"i"`
	Role      string `json:"role"`
	Text      string `json:"text"`
	ToolCalls []any  `json:"tool_calls,omitempty"`
	callLines bool
}

type compactionState struct {
	Context string         `json:"context"`
	Goal    string         `json:"goal"`
	History []historyEntry `json:"history"`
}

type fittedState struct {
	state  compactionState
	tokens int
	stage  string
}

type stateFitter struct {
	messages       []contracts.HistoryMessage
	calls          []toolCall
	byMessage      map[int][]toolCall
	goal           string
	preserveRecent int
	maxTokens      int
	baseTokens     int
	history        []historyEntry
	perEntry       []int
	tokens         int
}

func fitState(history contracts.History, calls []toolCall, goal string, opts options) (fittedState, error) {
	fitter := &stateFitter{
		messages:       history.Messages,
		calls:          calls,
		byMessage:      callsByMessage(calls),
		goal:           goal,
		preserveRecent: opts.preserveRecent,
		maxTokens:      opts.maxStateTokens,
	}
	base, err := fitter.measure(compactionState{Context: stateContext, Goal: goal, History: []historyEntry{}})
	if err != nil {
		return fittedState{}, err
	}
	fitter.baseTokens = base
	return fitter.fit()
}

func callsByMessage(calls []toolCall) map[int][]toolCall {
	byMessage := make(map[int][]toolCall)
	for _, call := range calls {
		byMessage[call.message] = append(byMessage[call.message], call)
	}
	return byMessage
}

func (f *stateFitter) measure(value any) (int, error) {
	encoded, err := encodeJSON(value)
	if err != nil {
		return 0, err
	}
	return estimateTokens(encoded), nil
}

func (f *stateFitter) entryTokens(entry *historyEntry) (int, error) {
	tokens, err := f.measure(entry)
	return tokens + 1, err
}

func (f *stateFitter) fits() bool { return f.tokens <= f.maxTokens }

func (f *stateFitter) fitted(stage string) fittedState {
	return fittedState{
		state:  compactionState{Context: stateContext, Goal: f.goal, History: f.history},
		tokens: f.tokens,
		stage:  stage,
	}
}

func (f *stateFitter) pinned(entry *historyEntry) bool {
	return isPinned(entry.Index, len(f.messages), f.preserveRecent)
}

func (f *stateFitter) fit() (fittedState, error) {
	for _, limit := range []int{fullInputRunes, shortInputRunes, lineInputRunes} {
		if err := f.rebuild(f.entries(limit)); err != nil {
			return fittedState{}, err
		}
		if f.fits() {
			return f.fitted("inputs<=" + strconv.Itoa(limit)), nil
		}
	}
	order := f.order()
	for _, stage := range []struct {
		name   string
		shrink func(*historyEntry) bool
	}{
		{"texts abridged", f.abridgeText},
		{"old messages collapsed", f.collapseText},
		{"old calls compacted", f.compactCalls},
	} {
		fitted, err := f.shrinkEach(order, stage.shrink)
		if err != nil {
			return fittedState{}, err
		}
		if fitted {
			return f.fitted(stage.name), nil
		}
	}
	if f.leaveOut(order) {
		return f.fitted("old messages left out"), nil
	}
	if err := f.rebuild(f.mergeCallRuns()); err != nil {
		return fittedState{}, err
	}
	if f.fits() {
		return f.fitted("old calls merged"), nil
	}
	return fittedState{}, fmt.Errorf("%w: about %d tokens after shrinking, limit %d", ErrHistoryTooLarge, f.tokens, f.maxTokens)
}

func (f *stateFitter) entries(inputRunes int) []historyEntry {
	var entries []historyEntry
	for index := range f.messages {
		message := f.messages[index]
		own := f.byMessage[index]
		if strings.TrimSpace(message.Text) == "" && len(own) == 0 {
			continue
		}
		entry := historyEntry{Index: index, Role: message.Role.String(), Text: message.Text}
		for _, call := range own {
			entry.ToolCalls = append(entry.ToolCalls, stateCall{
				ID:     call.ref,
				Tool:   call.source.Tool,
				Input:  truncate(call.source.Input, inputRunes),
				Result: fmt.Sprintf("%s, %d chars (omitted)", call.status(), call.resultRunes()),
			})
		}
		entries = append(entries, entry)
	}
	return entries
}

func (f *stateFitter) rebuild(entries []historyEntry) error {
	f.history = entries
	f.perEntry = make([]int, len(entries))
	f.tokens = f.baseTokens
	for index := range entries {
		tokens, err := f.entryTokens(&entries[index])
		if err != nil {
			return err
		}
		f.perEntry[index] = tokens
		f.tokens += tokens
	}
	return nil
}

func (f *stateFitter) order() []int {
	order := make([]int, 0, len(f.history))
	for _, wantPinned := range []bool{false, true} {
		for index := range f.history {
			if f.pinned(&f.history[index]) == wantPinned {
				order = append(order, index)
			}
		}
	}
	return order
}

func (f *stateFitter) shrinkEach(order []int, shrink func(*historyEntry) bool) (bool, error) {
	for _, index := range order {
		entry := &f.history[index]
		if !shrink(entry) {
			continue
		}
		tokens, err := f.entryTokens(entry)
		if err != nil {
			return false, err
		}
		f.tokens += tokens - f.perEntry[index]
		f.perEntry[index] = tokens
		if f.fits() {
			return true, nil
		}
	}
	return false, nil
}

func (*stateFitter) abridgeText(entry *historyEntry) bool {
	if runeCount(entry.Text) <= textHeadRunes+textTailRunes+abridgeSlack {
		return false
	}
	entry.Text = abridge(entry.Text, textHeadRunes, textTailRunes)
	return true
}

func (f *stateFitter) collapseText(entry *historyEntry) bool {
	if f.pinned(entry) || entry.Text == "" {
		return false
	}
	entry.Text = fmt.Sprintf("[%s %d chars omitted %s]", ellipsis, runeCount(f.messages[entry.Index].Text), ellipsis)
	return true
}

func (f *stateFitter) compactCalls(entry *historyEntry) bool {
	own := f.byMessage[entry.Index]
	if f.pinned(entry) || len(own) == 0 {
		return false
	}
	lines := make([]any, 0, len(own))
	for _, call := range own {
		lines = append(lines, compactCallLine(call, lineInputRunes))
	}
	entry.ToolCalls = lines
	entry.callLines = true
	return true
}

func (f *stateFitter) leaveOut(order []int) bool {
	left := make(map[int]bool)
	for _, index := range order {
		entry := &f.history[index]
		if f.pinned(entry) || len(entry.ToolCalls) > 0 {
			continue
		}
		left[index] = true
		f.tokens -= f.perEntry[index]
		if f.fits() {
			break
		}
	}
	kept := make([]historyEntry, 0, len(f.history)-len(left))
	for index := range f.history {
		if !left[index] {
			kept = append(kept, f.history[index])
		}
	}
	f.history = kept
	return f.fits()
}

func (f *stateFitter) foldable(entry *historyEntry) bool {
	return !f.pinned(entry) && entry.Text == "" && entry.callLines
}

func (f *stateFitter) mergeCallRuns() []historyEntry {
	merged := make([]historyEntry, 0, len(f.history))
	for index := range f.history {
		entry := f.history[index]
		if last := len(merged) - 1; last >= 0 && f.foldable(&merged[last]) && f.foldable(&entry) && merged[last].Role == entry.Role {
			merged[last].ToolCalls = slices.Concat(merged[last].ToolCalls, entry.ToolCalls)
			continue
		}
		merged = append(merged, entry)
	}
	return merged
}
