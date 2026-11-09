package jev

import (
	"strconv"
	"strings"
)

const inputLevels = 3

type inputLadder struct {
	full  int
	short int
	line  int
}

func defaultInputLadder() inputLadder {
	return inputLadder{full: defaultFullInputRunes, short: defaultShortInputRunes, line: defaultLineInputRunes}
}

func (l inputLadder) levels() []int {
	return []int{l.full, l.short, l.line}
}

func (l inputLadder) String() string {
	parts := make([]string, 0, inputLevels)
	for _, runes := range l.levels() {
		parts = append(parts, strconv.Itoa(runes))
	}
	return strings.Join(parts, ",")
}

func parseInputLadder(text string) (inputLadder, bool) {
	fields, ok := parseList(text, nonEmpty)
	if !ok || len(fields) != inputLevels {
		return inputLadder{}, false
	}
	var runes []int
	for _, field := range fields {
		value, err := strconv.Atoi(field)
		if err != nil || value < 1 || (len(runes) > 0 && value > runes[len(runes)-1]) {
			return inputLadder{}, false
		}
		runes = append(runes, value)
	}
	return inputLadder{full: runes[0], short: runes[1], line: runes[2]}, true
}
