package jev

import (
	"strings"
	"testing"
)

func TestEstimateTokensWeighsWordsDigitsAndSymbols(t *testing.T) {
	cases := map[string]int{
		"":                               0,
		"hello":                          1,
		"abcdefg":                        2,
		"123":                            2,
		"{}":                             2,
		"go test ./...":                  7,
		"é":                              1,
		"  \n\t ":                        0,
		strings.Repeat("\U0001F600", 10): 18,
		strings.Repeat("\u0085", 7):      7,
		"x\ufeffy\ufeffz":                3,
	}
	for text, want := range cases {
		if got := estimateTokens(text); got != want {
			t.Errorf("estimateTokens(%q) = %d, want %d", text, got, want)
		}
	}
}

func TestTruncateCountsRunesAndMarksTheCut(t *testing.T) {
	if got := truncate("héllo", 5); got != "héllo" {
		t.Fatalf("truncate kept %q, want the text whole", got)
	}
	if got := truncate("héllo world", 4); got != "hél…" {
		t.Fatalf("truncate = %q, want three runes and an ellipsis", got)
	}
}

func TestAbridgeKeepsHeadAndTail(t *testing.T) {
	text := strings.Repeat("a", 10) + strings.Repeat("b", 100) + strings.Repeat("c", 10)
	got := abridge(text, 10, 10)
	if got != strings.Repeat("a", 10)+"\n[… 100 chars omitted …]\n"+strings.Repeat("c", 10) {
		t.Fatalf("abridge = %q", got)
	}
	if short := abridge("short", 10, 10); short != "short" {
		t.Fatalf("abridge shortened a short text to %q", short)
	}
}

func TestFlatInputKeepsKeyOrder(t *testing.T) {
	cases := map[string]string{
		`{"z":"last  word","a":{ "n": 1 }}`: `z=last word a={"a":{"n":1}}`,
		`{"p":"  padded\t"}`:                `p= padded `,
		` not  json `:                       ` not json `,
		`["array"]`:                         `["array"]`,
	}
	for input, want := range cases {
		if got := flatInput(input); got != want {
			t.Errorf("flatInput(%s) = %q, want %q", input, got, want)
		}
	}
}
