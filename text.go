package jev

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"
	"unicode"
	"unicode/utf8"
)

const (
	digitTenths     = 5
	symbolTenths    = 9
	wordTenths      = 10
	lettersPerToken = 6
	tenthsPerToken  = 10
	ellipsis        = "…"
	abridgeSlack    = 40
)

func estimateTokens(text string) int {
	tenths := 0
	for index := 0; index < len(text); {
		char, size := utf8.DecodeRuneInString(text[index:])
		switch {
		case isASCIIDigit(char):
			run := asciiRun(text[index:], isASCIIDigit)
			tenths += run * digitTenths
			index += run
		case isASCIILetter(char):
			run := asciiRun(text[index:], isASCIILetter)
			tenths += (1 + (run-1)/lettersPerToken) * wordTenths
			index += run
		case unicode.IsSpace(char):
			index += size
		default:
			tenths += symbolTenths
			index += size
		}
	}
	return (tenths + tenthsPerToken - 1) / tenthsPerToken
}

func isASCIIDigit(char rune) bool { return char >= '0' && char <= '9' }

func isASCIILetter(char rune) bool {
	return (char >= 'a' && char <= 'z') || (char >= 'A' && char <= 'Z')
}

func asciiRun(text string, accepts func(rune) bool) int {
	length := 0
	for length < len(text) && accepts(rune(text[length])) {
		length++
	}
	return length
}

func runeCount(text string) int { return utf8.RuneCountInString(text) }

func runePrefix(text string, limit int) string {
	if limit <= 0 {
		return ""
	}
	seen := 0
	for index := range text {
		if seen == limit {
			return text[:index]
		}
		seen++
	}
	return text
}

func runeSuffix(text string, limit int) string {
	if limit <= 0 {
		return ""
	}
	end := len(text)
	for seen := 0; seen < limit && end > 0; seen++ {
		_, size := utf8.DecodeLastRuneInString(text[:end])
		end -= size
	}
	return text[end:]
}

func truncate(text string, limit int) string {
	if runeCount(text) <= limit {
		return text
	}
	return runePrefix(text, limit-1) + ellipsis
}

func abridge(text string, head, tail int) string {
	total := runeCount(text)
	if total <= head+tail+abridgeSlack {
		return text
	}
	return fmt.Sprintf("%s\n[%s %d chars omitted %s]\n%s", runePrefix(text, head), ellipsis, total-head-tail, ellipsis, runeSuffix(text, tail))
}

func collapseSpaces(text string) string {
	return strings.Join(strings.Fields(text), " ")
}

func encodeJSON(value any) (string, error) {
	var buffer bytes.Buffer
	encoder := json.NewEncoder(&buffer)
	encoder.SetEscapeHTML(false)
	if err := encoder.Encode(value); err != nil {
		return "", fmt.Errorf("jev: encode: %w", err)
	}
	return strings.TrimSuffix(buffer.String(), "\n"), nil
}
