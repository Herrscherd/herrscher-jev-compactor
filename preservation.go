package jev

import contracts "github.com/Herrscherd/herrscher-contracts"

type preservation struct {
	first        bool
	recent       int
	recentTokens int
	turns        int
}

func (p preservation) pinnedMessages(messages []contracts.HistoryMessage) []bool {
	pinned := make([]bool, len(messages))
	for index := range messages {
		pinned[index] = (p.first && index == 0) || index >= len(messages)-p.recent
	}
	p.keepRecentTokens(messages, pinned)
	p.keepAfterTurns(messages, pinned)
	return pinned
}

func (p preservation) keepRecentTokens(messages []contracts.HistoryMessage, pinned []bool) {
	if p.recentTokens == 0 {
		return
	}
	spent := 0
	for index := len(messages) - 1; index >= 0; index-- {
		spent += messageTokens(messages[index])
		if spent > p.recentTokens {
			return
		}
		pinned[index] = true
	}
}

func (p preservation) keepAfterTurns(messages []contracts.HistoryMessage, pinned []bool) {
	turn, found := nthLastUser(messages, p.turns)
	if !found {
		return
	}
	for index := turn + 1; index < len(messages); index++ {
		pinned[index] = true
	}
}

func nthLastUser(messages []contracts.HistoryMessage, nth int) (int, bool) {
	seen := 0
	for index := len(messages) - 1; index >= 0; index-- {
		if messages[index].Role != contracts.RoleUser {
			continue
		}
		seen++
		if seen == nth {
			return index, true
		}
	}
	return 0, false
}

func messageTokens(message contracts.HistoryMessage) int {
	tokens := estimateTokens(message.Text)
	for _, call := range message.Calls {
		tokens += estimateTokens(call.Input) + estimateTokens(call.Result)
	}
	return tokens
}
