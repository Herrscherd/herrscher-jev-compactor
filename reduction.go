package jev

import (
	contracts "github.com/Herrscherd/herrscher-contracts"
)

const (
	unitNameCharacters = "characters"
	unitNameEstimated  = "tokens"
)

type reductionUnit uint8

const (
	unitCharacters reductionUnit = iota + 1
	unitEstimated
)

func parseReductionUnit(text string) (reductionUnit, bool) {
	switch text {
	case unitNameCharacters:
		return unitCharacters, true
	case unitNameEstimated:
		return unitEstimated, true
	}
	return 0, false
}

func (u reductionUnit) size(text string) int {
	if u == unitEstimated {
		return estimateTokens(text)
	}
	return runeCount(text)
}

func (u reductionUnit) historySize(history contracts.History) int {
	total := 0
	for index := range history.Messages {
		total += u.size(history.Messages[index].Text)
		for _, call := range history.Messages[index].Calls {
			total += u.size(call.Input) + u.size(call.Result)
		}
	}
	return total
}

func (r removal) in(unit reductionUnit) int {
	if unit == unitEstimated {
		return r.tokens
	}
	return r.runes
}
