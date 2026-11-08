package jev

import (
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"

	contracts "github.com/Herrscherd/herrscher-contracts"
)

const (
	nestedInputRunes = 200
	goalPrompts      = 3
	goalPromptRunes  = 500
)

var errInputNotObject = errors.New("tool input is not a JSON object")

type toolCall struct {
	ref     string
	source  contracts.HistoryCall
	message int
	pinned  bool
}

func (c toolCall) resultRunes() int { return runeCount(c.source.Result) }

func (c toolCall) status() string {
	if c.source.ResultIsError {
		return "error"
	}
	return "ok"
}

func isPinned(index, total, preserveRecent int) bool {
	return index == 0 || index >= total-preserveRecent
}

func collectCalls(history contracts.History, preserveRecent int) []toolCall {
	var calls []toolCall
	total := len(history.Messages)
	for index := range history.Messages {
		for _, call := range history.Messages[index].Calls {
			calls = append(calls, toolCall{
				ref:     "t" + strconv.Itoa(len(calls)+1),
				source:  call,
				message: index,
				pinned:  isPinned(index, total, preserveRecent),
			})
		}
	}
	return calls
}

func goalOf(history contracts.History, instruction string) string {
	if instruction = strings.TrimSpace(instruction); instruction != "" {
		return instruction
	}
	var prompts []string
	for index := range history.Messages {
		message := history.Messages[index]
		if message.Role == contracts.RoleUser && strings.TrimSpace(message.Text) != "" {
			prompts = append(prompts, truncate(message.Text, goalPromptRunes))
		}
	}
	if len(prompts) > goalPrompts {
		prompts = prompts[len(prompts)-goalPrompts:]
	}
	return strings.Join(prompts, "\n")
}

func compactCallLine(call toolCall, inputRunes int) string {
	return fmt.Sprintf("%s %s %s → %s %dch", call.ref, call.source.Tool, truncate(flatInput(call.source.Input), inputRunes), call.status(), call.resultRunes())
}

type inputPair struct {
	key   string
	value string
}

func flatInput(input string) string {
	pairs, err := inputPairs(input)
	if err != nil {
		return collapseSpaces(input)
	}
	parts := make([]string, 0, len(pairs))
	for _, pair := range pairs {
		parts = append(parts, pair.key+"="+collapseSpaces(pair.value))
	}
	return strings.Join(parts, " ")
}

func inputPairs(input string) ([]inputPair, error) {
	decoder := json.NewDecoder(strings.NewReader(input))
	if token, err := decoder.Token(); err != nil || token != json.Delim('{') {
		return nil, errInputNotObject
	}
	var pairs []inputPair
	for decoder.More() {
		token, err := decoder.Token()
		if err != nil {
			return nil, fmt.Errorf("%w: %w", errInputNotObject, err)
		}
		key, isKey := token.(string)
		if !isKey {
			return nil, errInputNotObject
		}
		var raw json.RawMessage
		if err := decoder.Decode(&raw); err != nil {
			return nil, fmt.Errorf("%w: %w", errInputNotObject, err)
		}
		pairs = append(pairs, inputPair{key: key, value: rawValueText(raw)})
	}
	return pairs, nil
}

func rawValueText(raw json.RawMessage) string {
	var text string
	if json.Unmarshal(raw, &text) == nil {
		return text
	}
	return truncate(string(raw), nestedInputRunes)
}
