package jev

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"

	contracts "github.com/Herrscherd/herrscher-contracts"
)

const nestedInputRunes = 200

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

func collectCalls(history contracts.History, preserveRecent int) []toolCall {
	return collectCallsWith(history, preservation{first: true, recent: preserveRecent}, false)
}

func collectCallsWith(history contracts.History, keep preservation, includeEmpty bool) []toolCall {
	var calls []toolCall
	pinned := keep.pinnedMessages(history.Messages)
	for index := range history.Messages {
		for _, call := range history.Messages[index].Calls {
			if call.Result == "" && !includeEmpty {
				continue
			}
			calls = append(calls, toolCall{
				ref:     "t" + strconv.Itoa(len(calls)+1),
				source:  call,
				message: index,
				pinned:  pinned[index],
			})
		}
	}
	return calls
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
		if err = decoder.Decode(&raw); err != nil {
			return nil, fmt.Errorf("%w: %w", errInputNotObject, err)
		}
		value, err := rawValueText(key, raw)
		if err != nil {
			return nil, err
		}
		pairs = append(pairs, inputPair{key: key, value: value})
	}
	return pairs, nil
}

func rawValueText(key string, raw json.RawMessage) (string, error) {
	var text string
	if json.Unmarshal(raw, &text) == nil {
		return text, nil
	}
	encodedKey, err := encodeJSON(key)
	if err != nil {
		return "", err
	}
	var compacted bytes.Buffer
	if err := json.Compact(&compacted, raw); err != nil {
		return "", fmt.Errorf("%w: %w", errInputNotObject, err)
	}
	return truncate("{"+encodedKey+":"+compacted.String()+"}", nestedInputRunes), nil
}
