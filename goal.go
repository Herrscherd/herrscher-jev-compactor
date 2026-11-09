package jev

import (
	"strings"

	contracts "github.com/Herrscherd/herrscher-contracts"
)

type goalSource uint8

const (
	goalSourceAuto goalSource = iota + 1
	goalSourcePrompts
	goalSourceFixed
)

func parseGoalSource(text string) (goalSource, bool) {
	switch text {
	case "auto":
		return goalSourceAuto, true
	case "prompts":
		return goalSourcePrompts, true
	case "fixed":
		return goalSourceFixed, true
	}
	return 0, false
}

type goalSpec struct {
	source      goalSource
	text        string
	prompts     int
	promptRunes int
}

func defaultGoalSpec() goalSpec {
	return options{}.goalSettings()
}

func goalOf(history contracts.History, instruction string) string {
	return defaultGoalSpec().goal(history, instruction)
}

func (s goalSpec) goal(history contracts.History, instruction string) string {
	if s.source == goalSourceFixed {
		return s.text
	}
	if trimmed := strings.TrimSpace(instruction); s.source == goalSourceAuto && trimmed != "" {
		return trimmed
	}
	return s.fromPrompts(history)
}

func (s goalSpec) fromPrompts(history contracts.History) string {
	var prompts []string
	for index := range history.Messages {
		message := history.Messages[index]
		if message.Role == contracts.RoleUser && strings.TrimSpace(message.Text) != "" {
			prompts = append(prompts, truncate(message.Text, s.promptRunes))
		}
	}
	if len(prompts) > s.prompts {
		prompts = prompts[len(prompts)-s.prompts:]
	}
	return strings.Join(prompts, "\n")
}
