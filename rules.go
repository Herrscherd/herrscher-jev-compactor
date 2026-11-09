package jev

import (
	"path"
	"slices"
	"strconv"
	"strings"
)

const (
	keepName     = "keep"
	pruneName    = "prune"
	truncateName = "truncate"
)

type ruleAction uint8

const (
	actionKeep ruleAction = iota + 1
	actionPrune
	actionTruncate
)

func parseRuleAction(name string) (ruleAction, bool) {
	switch name {
	case keepName:
		return actionKeep, true
	case pruneName:
		return actionPrune, true
	case truncateName:
		return actionTruncate, true
	}
	return 0, false
}

func (a ruleAction) decision() decision {
	switch a {
	case actionKeep:
		return decisionKeep
	case actionPrune:
		return decisionPruneCall
	case actionTruncate:
		return decisionPruneResult
	}
	return decisionKeep
}

type rule struct {
	patterns   []string
	action     ruleAction
	callKeep   optionalRatio
	resultKeep optionalRatio
	head       int
	tail       optionalCount
}

type optionalCount struct {
	value int
	set   bool
}

func (c optionalCount) or(fallback int) int {
	if c.set {
		return c.value
	}
	return fallback
}

func parseRules(text string) ([]rule, bool) {
	var rules []rule
	for _, segment := range strings.Split(text, ";") {
		parsed, ok := parseRule(segment)
		if !ok {
			return nil, false
		}
		rules = append(rules, parsed)
	}
	return rules, true
}

func parseRule(text string) (rule, bool) {
	motifs, actions, found := strings.Cut(text, ":")
	if !found {
		return rule{}, false
	}
	patterns, ok := parseList(motifs, validGlob)
	if !ok {
		return rule{}, false
	}
	parsed := rule{patterns: patterns}
	tokens, ok := parseList(actions, nonEmpty)
	if !ok {
		return rule{}, false
	}
	for _, token := range tokens {
		if !parsed.apply(token) {
			return rule{}, false
		}
	}
	return parsed, true
}

func (r *rule) apply(token string) bool {
	name, value, valued := strings.Cut(token, "=")
	if !valued {
		action, known := parseRuleAction(name)
		return known && r.setAction(action)
	}
	switch name {
	case "threshold":
		ratio, ok := parseRatio(value)
		return ok && r.setCallKeep(ratio) && r.setResultKeep(ratio)
	case "call_threshold":
		ratio, ok := parseRatio(value)
		return ok && r.setCallKeep(ratio)
	case "result_threshold":
		ratio, ok := parseRatio(value)
		return ok && r.setResultKeep(ratio)
	case "head":
		runes, err := strconv.Atoi(value)
		return err == nil && runes >= 1 && r.setHead(runes)
	case "tail":
		runes, err := strconv.Atoi(value)
		return err == nil && runes >= 0 && r.setTail(runes)
	}
	return false
}

func (r *rule) setAction(action ruleAction) bool {
	if r.action != 0 {
		return false
	}
	r.action = action
	return true
}

func (r *rule) setCallKeep(ratio float64) bool {
	if r.callKeep.set {
		return false
	}
	r.callKeep = optionalRatio{value: ratio, set: true}
	return true
}

func (r *rule) setResultKeep(ratio float64) bool {
	if r.resultKeep.set {
		return false
	}
	r.resultKeep = optionalRatio{value: ratio, set: true}
	return true
}

func (r *rule) setHead(runes int) bool {
	if r.head != 0 {
		return false
	}
	r.head = runes
	return true
}

func (r *rule) setTail(runes int) bool {
	if r.tail.set {
		return false
	}
	r.tail = optionalCount{value: runes, set: true}
	return true
}

func (r *rule) matches(tool string) bool {
	return slices.ContainsFunc(r.patterns, func(pattern string) bool {
		return globMatches(pattern, tool)
	})
}

func globMatches(pattern, name string) bool {
	matched, err := path.Match(pattern, name)
	return err == nil && matched
}

func validGlob(pattern string) bool {
	_, err := path.Match(pattern, "")
	return nonEmpty(pattern) && err == nil
}

func nonEmpty(text string) bool { return text != "" }

func parseList(text string, valid func(string) bool) ([]string, bool) {
	var items []string
	for _, item := range strings.Split(text, ",") {
		item = strings.TrimSpace(item)
		if !valid(item) {
			return nil, false
		}
		items = append(items, item)
	}
	return items, true
}

type callPolicy struct {
	rules []rule
	pins  []string
}

func (p *callPolicy) ruleFor(tool string) rule {
	if p == nil {
		return rule{}
	}
	for _, candidate := range p.rules {
		if candidate.matches(tool) {
			return candidate
		}
	}
	return rule{}
}

func (p *callPolicy) pinned(input string) bool {
	return p != nil && slices.ContainsFunc(p.pins, func(pin string) bool {
		return strings.Contains(input, pin)
	})
}
