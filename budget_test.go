package jev

import (
	"slices"
	"testing"

	contracts "github.com/Herrscherd/herrscher-contracts"
)

func TestBudgetExceededHonoursBothLimits(t *testing.T) {
	cases := []struct {
		name   string
		limits budget
		pruned int
		remove int
		total  int
		want   bool
	}{
		{"no limits", budget{}, 50, 1000, 1000, false},
		{"pruned at the cap", budget{maxPruned: 2}, 2, 0, 0, false},
		{"pruned over the cap", budget{maxPruned: 2}, 3, 0, 0, true},
		{"share default allows everything", budget{}, 0, 100, 100, false},
		{"share default refuses more than everything", budget{}, 0, 101, 100, true},
		{"share at the limit", budget{maxRemovedShare: 0.5}, 0, 50, 100, false},
		{"share over the limit", budget{maxRemovedShare: 0.5}, 0, 51, 100, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.limits.exceeded(tc.pruned, tc.remove, tc.total); got != tc.want {
				t.Fatalf("exceeded(%d, %d, %d) = %v, want %v", tc.pruned, tc.remove, tc.total, got, tc.want)
			}
		})
	}
}

func TestFitBudgetDemotesTheHighestKeepFirst(t *testing.T) {
	planned := []plannedEdit{
		pruneCall("a", 0.9, 10),
		pruneCall("b", 0.6, 10),
		pruneCall("c", 0.2, 10),
		pruneCall("d", ruleDecided, 10),
	}
	got := fitBudget(planned, budget{maxPruned: 2}, 1000)
	want := []string{"c", "d"}
	if ids := callIDs(got); !slices.Equal(ids, want) {
		t.Fatalf("fitBudget kept %v, want %v", ids, want)
	}
}

func TestFitBudgetDemotesRuleDecidedEditsLast(t *testing.T) {
	planned := []plannedEdit{
		pruneCall("rule", ruleDecided, 10),
		pruneCall("jev", 0.1, 10),
	}
	got := fitBudget(planned, budget{maxPruned: 1}, 1000)
	if ids := callIDs(got); !slices.Equal(ids, []string{"rule"}) {
		t.Fatalf("fitBudget kept %v, want the rule-decided edit only", ids)
	}
}

func TestFitBudgetKeepsTheShareWithinItsLimit(t *testing.T) {
	planned := []plannedEdit{
		pruneCall("a", 0.9, 60),
		pruneCall("b", 0.1, 30),
	}
	got := fitBudget(planned, budget{maxRemovedShare: 0.5}, 100)
	if ids := callIDs(got); !slices.Equal(ids, []string{"b"}) {
		t.Fatalf("fitBudget kept %v, want [b]", ids)
	}
}

func TestFitBudgetPreservesTheOrderOfKeptEdits(t *testing.T) {
	planned := []plannedEdit{
		pruneCall("x", 0.9, 1),
		pruneCall("y", 0.1, 1),
		pruneCall("z", 0.5, 1),
	}
	got := fitBudget(planned, budget{maxPruned: 2}, 1000)
	if ids := callIDs(got); !slices.Equal(ids, []string{"y", "z"}) {
		t.Fatalf("fitBudget kept %v, want [y z]", ids)
	}
}

func TestFitBudgetWithoutLimitsKeepsEverything(t *testing.T) {
	planned := []plannedEdit{pruneCall("a", 0.9, 10), pruneCall("b", 0.1, 10)}
	if got := fitBudget(planned, budget{}, 100); len(got) != 2 {
		t.Fatalf("fitBudget = %v, want both edits", callIDs(got))
	}
}

func pruneCall(id string, keep float64, removed int) plannedEdit {
	return plannedEdit{
		edit:    contracts.HistoryEdit{CallID: id, Action: contracts.PruneCall},
		keep:    keep,
		removed: removed,
	}
}

func callIDs(edits []contracts.HistoryEdit) []string {
	ids := make([]string, 0, len(edits))
	for _, edit := range edits {
		ids = append(ids, edit.CallID)
	}
	return ids
}
