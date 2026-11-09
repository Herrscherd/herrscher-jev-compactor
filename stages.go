package jev

import (
	"slices"
	"strconv"
	"strings"
)

type stageKind uint8

const (
	stageAbridge stageKind = iota + 1
	stageCollapse
	stageCompactCalls
	stageLeaveOut
	stageMerge
)

const stageCount = int(stageMerge)

type stageOrder [stageCount]stageKind

func stageKinds() []stageKind {
	return []stageKind{stageAbridge, stageCollapse, stageCompactCalls, stageLeaveOut, stageMerge}
}

func defaultStageOrder() stageOrder {
	return stageOrder{stageAbridge, stageCollapse, stageCompactCalls, stageLeaveOut, stageMerge}
}

func (k stageKind) name() string {
	switch k {
	case stageAbridge:
		return "abridge_texts"
	case stageCollapse:
		return "collapse_messages"
	case stageCompactCalls:
		return "compact_calls"
	case stageLeaveOut:
		return "leave_out"
	case stageMerge:
		return "merge_calls"
	}
	return strconv.Itoa(int(k))
}

func (k stageKind) label() string {
	switch k {
	case stageAbridge:
		return "texts abridged"
	case stageCollapse:
		return "old messages collapsed"
	case stageCompactCalls:
		return "old calls compacted"
	case stageLeaveOut:
		return "old messages left out"
	case stageMerge:
		return "old calls merged"
	}
	return strconv.Itoa(int(k))
}

func parseStageKind(name string) (stageKind, bool) {
	for _, kind := range stageKinds() {
		if kind.name() == name {
			return kind, true
		}
	}
	return 0, false
}

func parseStageOrder(text string) (stageOrder, bool) {
	names, ok := parseList(text, nonEmpty)
	if !ok {
		return stageOrder{}, false
	}
	var order stageOrder
	for index, name := range names {
		kind, known := parseStageKind(name)
		if !known || slices.Contains(order[:index], kind) {
			return stageOrder{}, false
		}
		order[index] = kind
	}
	return order, true
}

func (o stageOrder) kinds() []stageKind {
	var kinds []stageKind
	for _, kind := range o {
		if kind != 0 {
			kinds = append(kinds, kind)
		}
	}
	return kinds
}

func (o stageOrder) String() string {
	kinds := o.kinds()
	names := make([]string, 0, len(kinds))
	for _, kind := range kinds {
		names = append(names, kind.name())
	}
	return strings.Join(names, ",")
}
