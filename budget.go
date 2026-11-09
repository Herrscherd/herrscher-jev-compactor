package jev

import (
	"cmp"
	"slices"

	contracts "github.com/Herrscherd/herrscher-contracts"
)

const (
	ruleDecided = -1
	fullShare   = 1
)

type plannedEdit struct {
	edit    contracts.HistoryEdit
	keep    float64
	removed int
}

type budget struct {
	maxPruned       int
	maxRemovedShare float64
}

func (b budget) exceeded(pruned, removed, historyRunes int) bool {
	if b.maxPruned > 0 && pruned > b.maxPruned {
		return true
	}
	share := orDefault(b.maxRemovedShare, fullShare)
	return float64(removed) > share*float64(historyRunes)
}

func fitBudget(planned []plannedEdit, limits budget, historyRunes int) []contracts.HistoryEdit {
	removed := 0
	for _, edit := range planned {
		removed += edit.removed
	}
	dropped := make([]bool, len(planned))
	pruned := len(planned)
	for _, index := range demotionOrder(planned) {
		if !limits.exceeded(pruned, removed, historyRunes) {
			break
		}
		dropped[index] = true
		pruned--
		removed -= planned[index].removed
	}
	edits := make([]contracts.HistoryEdit, 0, pruned)
	for index, edit := range planned {
		if !dropped[index] {
			edits = append(edits, edit.edit)
		}
	}
	return edits
}

func demotionOrder(planned []plannedEdit) []int {
	order := make([]int, len(planned))
	for index := range order {
		order[index] = index
	}
	slices.SortStableFunc(order, func(a, b int) int {
		return cmp.Compare(planned[b].keep, planned[a].keep)
	})
	return order
}
