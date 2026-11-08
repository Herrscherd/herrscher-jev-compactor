package jev

import (
	"errors"
	"fmt"

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

func decide(call toolCall, answer callAnswer, keepThreshold float64) decision {
	switch {
	case call.pinned, answer.keepResult >= keepThreshold:
		return decisionKeep
	case answer.keepCall >= keepThreshold:
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
