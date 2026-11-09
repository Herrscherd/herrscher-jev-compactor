package jev

import (
	"testing"

	"github.com/Herrscherd/herrscher-contracts/i18n"
)

func TestRichNotesReadSingularAndPluralInBothLanguages(t *testing.T) {
	one := map[string]any{"Count": 1, "Kept": 0, "Truncated": 1, "Pruned": 0, "Percent": 41, "Target": 25}
	many := map[string]any{"Count": 25, "Kept": 12, "Truncated": 5, "Pruned": 8, "Percent": 41, "Target": 25}
	cases := []struct {
		name   string
		lang   i18n.Language
		key    string
		params map[string]any
		want   string
	}{
		{"en pruned, one call", i18n.English, notePruned, one, "Jev reviewed 1 tool call: 0 kept, 1 truncated, 0 removed, the history is 41% smaller"},
		{"en pruned, many calls", i18n.English, notePruned, many, "Jev reviewed 25 tool calls: 12 kept, 5 truncated, 8 removed, the history is 41% smaller"},
		{"en short, one call", i18n.English, notePrunedShort, one, "Jev reviewed 1 tool call: 0 kept, 1 truncated, 0 removed, the history is 41% smaller, short of the 25% aimed for, so the next stage runs"},
		{"en short, many calls", i18n.English, notePrunedShort, many, "Jev reviewed 25 tool calls: 12 kept, 5 truncated, 8 removed, the history is 41% smaller, short of the 25% aimed for, so the next stage runs"},
		{"fr pruned, one call", i18n.French, notePruned, one, "Jev a examiné 1 appel d'outil : 0 gardé, 1 tronqué, 0 supprimé, l'historique est plus court de 41 %"},
		{"fr pruned, many calls", i18n.French, notePruned, many, "Jev a examiné 25 appels d'outil : 12 gardés, 5 tronqués, 8 supprimés, l'historique est plus court de 41 %"},
		{"fr short, one call", i18n.French, notePrunedShort, one, "Jev a examiné 1 appel d'outil : 0 gardé, 1 tronqué, 0 supprimé, l'historique est plus court de 41 %, sous les 25 % visés, donc l'étape suivante s'exécute"},
		{"fr short, many calls", i18n.French, notePrunedShort, many, "Jev a examiné 25 appels d'outil : 12 gardés, 5 tronqués, 8 supprimés, l'historique est plus court de 41 %, sous les 25 % visés, donc l'étape suivante s'exécute"},
	}
	texts := newMessages()
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := texts.in(tc.lang, tc.key, tc.params); got != tc.want {
				t.Fatalf("note = %q, want %q", got, tc.want)
			}
		})
	}
}
