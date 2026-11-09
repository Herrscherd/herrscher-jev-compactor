package jev

import (
	"errors"
	"fmt"
	"math"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

	contracts "github.com/Herrscherd/herrscher-contracts"
)

const (
	settingAPIKey               = "api_key"
	settingModel                = "model"
	settingEndpoint             = "endpoint"
	settingKeepThreshold        = "keep_threshold"
	settingKeepCallThreshold    = "keep_call_threshold"
	settingKeepResultThreshold  = "keep_result_threshold"
	settingPreserveRecent       = "preserve_recent"
	settingTruncateHead         = "truncate_head"
	settingTruncateTail         = "truncate_tail"
	settingMinReduction         = "min_reduction"
	settingMaxStateTokens       = "max_state_tokens"
	settingMaxRequestTokens     = "max_request_tokens"
	settingTimeout              = "timeout"
	settingMaxParallel          = "max_parallel"
	settingTriggerPercent       = "trigger_percent"
	settingOnShort              = "on_short"
	settingReductionUnit        = "reduction_unit"
	settingDryRun               = "dry_run"
	settingRules                = "rules"
	settingPinInputs            = "pin_inputs"
	settingKeepErrors           = "keep_errors"
	settingIncludeEmpty         = "include_empty_results"
	settingMinResultChars       = "min_result_chars"
	settingMaxPruned            = "max_pruned"
	settingMaxRemovedShare      = "max_removed_share"
	settingStateInputRunes      = "state_input_runes"
	settingStateTextHead        = "state_text_head"
	settingStateTextTail        = "state_text_tail"
	settingStateContext         = "state_context"
	settingStateStages          = "state_stages"
	settingGoalSource           = "goal_source"
	settingGoalText             = "goal_text"
	settingGoalPrompts          = "goal_prompts"
	settingGoalPromptRunes      = "goal_prompt_runes"
	settingPreserveFirst        = "preserve_first"
	settingPreserveRecentBudget = "preserve_recent_tokens"
	settingPreserveTurns        = "preserve_turns"
	settingRetries              = "retries"
	settingRetryBackoff         = "retry_backoff"
	settingMaxRequests          = "max_requests"
	settingMaxSent              = "max_sent_tokens"
	settingDecisionLog          = "decision_log"
	settingIncremental          = "incremental"
	settingReplayDir            = "replay_dir"
)

const (
	typesafeEnv             = "TYPESAFE_API_KEY"
	defaultModel            = "jev-latest"
	defaultEndpoint         = "https://api.typesafe.ai/v1/systemone"
	defaultKeepThreshold    = 0.5
	defaultPreserveRecent   = 6
	defaultTruncateHead     = 300
	defaultTruncateTail     = 0
	defaultMinReduction     = 0.25
	defaultMaxStateTokens   = 25000
	defaultMaxRequestTokens = 30000
	defaultTimeout          = 2 * time.Minute
	defaultMaxParallel      = 4
	defaultOnShort          = onShortKeep
	defaultKeepErrors       = false
	defaultIncludeEmpty     = false
	defaultIncremental      = false
	defaultMinResultChars   = 0
	defaultMaxPruned        = 0
	defaultFullInputRunes   = 1000
	defaultShortInputRunes  = 200
	defaultLineInputRunes   = 60
	defaultTextHeadRunes    = 400
	defaultTextTailRunes    = 150
	defaultGoalPrompts      = 3
	defaultGoalPromptRunes  = 500
	defaultPreserveFirst    = true
	defaultRetries          = 0
	defaultRetryBackoff     = time.Second
)

type onShort uint8

const (
	onShortKeep onShort = iota + 1
	onShortDone
	onShortRevert
)

func parseOnShort(text string) (onShort, bool) {
	switch text {
	case keepName:
		return onShortKeep, true
	case "done":
		return onShortDone, true
	case "revert":
		return onShortRevert, true
	}
	return 0, false
}

// ErrInvalidSetting is returned when a setting of the stage holds a value it
// does not accept. Match it with errors.Is.
var ErrInvalidSetting = errors.New("invalid jev setting")

// ErrMissingAPIKey is returned when the stage is built without a TypeSafe API
// key. Match it with errors.Is.
var ErrMissingAPIKey = errors.New("jev needs a TypeSafe API key")

var (
	_ contracts.NoticeError = (*settingError)(nil)
	_ contracts.NoticeError = (*missingKeyError)(nil)
)

type settingError struct {
	key   string
	value string
}

func (e *settingError) Error() string {
	return fmt.Sprintf("%s: %s=%q", ErrInvalidSetting, e.key, e.value)
}

func (e *settingError) Unwrap() error { return ErrInvalidSetting }

func (e *settingError) Notice() contracts.Notice {
	return contracts.Notice{
		Code: contracts.CodeSettingInvalid,
		Args: []contracts.Arg{contracts.Text("Setting", e.key), contracts.Text("Value", e.value)},
	}
}

type incoherentError struct {
	message string
	setting string
	value   string
}

func (e *incoherentError) Error() string {
	return fmt.Sprintf("%s: %s", ErrInvalidSetting, e.message)
}

func (e *incoherentError) Unwrap() error { return ErrInvalidSetting }

func (e *incoherentError) Notice() contracts.Notice {
	return contracts.Notice{
		Code: contracts.CodeSettingInvalid,
		Args: []contracts.Arg{contracts.Text("Setting", e.setting), contracts.Text("Value", e.value)},
	}
}

type missingKeyError struct{}

func (*missingKeyError) Error() string {
	return fmt.Sprintf("%s: set %s", ErrMissingAPIKey, typesafeEnv)
}

func (*missingKeyError) Unwrap() error { return ErrMissingAPIKey }

func (*missingKeyError) Notice() contracts.Notice {
	return contracts.Notice{
		Code: contracts.CodeSettingMissing,
		Args: []contracts.Arg{contracts.Text("Settings", settingAPIKey+" ("+typesafeEnv+")")},
	}
}

type optionalRatio struct {
	value float64
	set   bool
}

func (r optionalRatio) or(fallback float64) float64 {
	if r.set {
		return r.value
	}
	return fallback
}

type optionalFlag struct {
	value bool
	set   bool
}

func (f optionalFlag) or(fallback bool) bool {
	if f.set {
		return f.value
	}
	return fallback
}

type options struct {
	apiKey               string
	model                string
	endpoint             string
	keepThreshold        float64
	keepCall             optionalRatio
	keepResult           optionalRatio
	preserveRecent       int
	truncateHead         int
	truncateTail         int
	minReduction         float64
	maxStateTokens       int
	maxRequestTokens     int
	timeout              time.Duration
	maxParallel          int
	triggerPercent       int
	onShort              onShort
	reductionUnit        reductionUnit
	dryRun               bool
	policy               *callPolicy
	keepErrors           bool
	includeEmpty         bool
	incremental          bool
	minResultChars       int
	maxPruned            int
	maxRemovedShare      float64
	stateInputRunes      inputLadder
	stateTextHead        int
	stateTextTail        int
	stateContext         string
	stateStages          stageOrder
	goalSource           goalSource
	goalText             string
	goalPrompts          int
	goalPromptRunes      int
	preserveFirst        optionalFlag
	preserveRecentTokens int
	preserveTurns        int
	retries              int
	retryBackoff         time.Duration
	maxRequests          int
	maxSentTokens        int
	decisionLog          string
	replayDir            string
}

func (o options) preservation() preservation {
	return preservation{
		first:        o.preserveFirst.or(defaultPreserveFirst),
		recent:       o.preserveRecent,
		recentTokens: o.preserveRecentTokens,
		turns:        o.preserveTurns,
	}
}

func (o options) parallelism() int {
	return orDefault(o.maxParallel, defaultMaxParallel)
}

func (o options) shortRule() onShort {
	return orDefault(o.onShort, defaultOnShort)
}

func (o options) revertsShort() bool {
	return o.shortRule() == onShortRevert
}

func (o options) measureUnit() reductionUnit {
	return orDefault(o.reductionUnit, unitCharacters)
}

func (o options) callKeepThreshold() float64 {
	return o.keepCall.or(o.keepThreshold)
}

func (o options) resultKeepThreshold() float64 {
	return o.keepResult.or(o.keepThreshold)
}

func (o options) budget() budget {
	return budget{maxPruned: o.maxPruned, maxRemovedShare: o.maxRemovedShare}
}

func (o options) stateLadder() inputLadder {
	return orDefault(o.stateInputRunes, defaultInputLadder())
}

func (o options) stateHead() int {
	return o.stateTextHead
}

func (o options) stateTail() int {
	return o.stateTextTail
}

func (o options) stateContextText() string {
	return orDefault(o.stateContext, defaultStateContext)
}

func (o options) stateStageKinds() []stageKind {
	return orDefault(o.stateStages, defaultStageOrder()).kinds()
}

func (o options) goalSettings() goalSpec {
	return goalSpec{
		source:      orDefault(o.goalSource, goalSourceAuto),
		text:        o.goalText,
		prompts:     orDefault(o.goalPrompts, defaultGoalPrompts),
		promptRunes: orDefault(o.goalPromptRunes, defaultGoalPromptRunes),
	}
}

func orDefault[T comparable](value, fallback T) T {
	var zero T
	if value == zero {
		return fallback
	}
	return value
}

func settings(texts *messages) []contracts.Setting {
	return []contracts.Setting{
		{Key: settingAPIKey, Env: typesafeEnv, Help: texts.english(helpAccess, nil), Required: true},
		{Key: settingModel, Env: "JEV_MODEL", Help: texts.english(helpModel, nil), Default: defaultModel},
		{Key: settingEndpoint, Env: "JEV_ENDPOINT", Help: texts.english(helpEndpoint, nil), Default: defaultEndpoint},
		{Key: settingKeepThreshold, Env: "JEV_KEEP_THRESHOLD", Help: texts.english(helpKeepThreshold, nil), Default: strconv.FormatFloat(defaultKeepThreshold, 'g', -1, 64)},
		{Key: settingKeepCallThreshold, Env: "JEV_KEEP_CALL_THRESHOLD", Help: texts.english(helpKeepCallThreshold, nil)},
		{Key: settingKeepResultThreshold, Env: "JEV_KEEP_RESULT_THRESHOLD", Help: texts.english(helpKeepResultThreshold, nil)},
		{Key: settingPreserveRecent, Env: "JEV_PRESERVE_RECENT", Help: texts.english(helpPreserveRecent, nil), Default: strconv.Itoa(defaultPreserveRecent)},
		{Key: settingTruncateHead, Env: "JEV_TRUNCATE_HEAD", Help: texts.english(helpTruncateHead, nil), Default: strconv.Itoa(defaultTruncateHead)},
		{Key: settingTruncateTail, Env: "JEV_TRUNCATE_TAIL", Help: texts.english(helpTruncateTail, nil), Default: strconv.Itoa(defaultTruncateTail)},
		{Key: settingMinReduction, Env: "JEV_MIN_REDUCTION", Help: texts.english(helpMinReduction, nil), Default: strconv.FormatFloat(defaultMinReduction, 'g', -1, 64)},
		{Key: settingMaxStateTokens, Env: "JEV_MAX_STATE_TOKENS", Help: texts.english(helpStateBudget, nil), Default: strconv.Itoa(defaultMaxStateTokens)},
		{Key: settingMaxRequestTokens, Env: "JEV_MAX_REQUEST_TOKENS", Help: texts.english(helpRequestBudget, nil), Default: strconv.Itoa(defaultMaxRequestTokens)},
		{Key: settingTimeout, Env: "JEV_TIMEOUT", Help: texts.english(helpTimeout, nil), Default: defaultTimeout.String()},
		{Key: settingMaxParallel, Env: "JEV_MAX_PARALLEL", Help: texts.english(helpMaxParallel, nil), Default: strconv.Itoa(defaultMaxParallel)},
		{Key: settingTriggerPercent, Env: "JEV_TRIGGER_PERCENT", Help: texts.english(helpTriggerPercent, nil), Default: "0"},
		{Key: settingOnShort, Env: "JEV_ON_SHORT", Help: texts.english(helpOnShort, nil), Default: keepName},
		{Key: settingReductionUnit, Env: "JEV_REDUCTION_UNIT", Help: texts.english(helpReductionUnit, nil), Default: unitNameCharacters},
		{Key: settingDryRun, Env: "JEV_DRY_RUN", Help: texts.english(helpDryRun, nil), Default: "false"},
		{Key: settingRules, Env: "JEV_RULES", Help: texts.english(helpRules, nil)},
		{Key: settingPinInputs, Env: "JEV_PIN_INPUTS", Help: texts.english(helpPinInputs, nil)},
		{Key: settingKeepErrors, Env: "JEV_KEEP_ERRORS", Help: texts.english(helpKeepErrors, nil), Default: strconv.FormatBool(defaultKeepErrors)},
		{Key: settingIncludeEmpty, Env: "JEV_INCLUDE_EMPTY_RESULTS", Help: texts.english(helpIncludeEmpty, nil), Default: strconv.FormatBool(defaultIncludeEmpty)},
		{Key: settingIncremental, Env: "JEV_INCREMENTAL", Help: texts.english(helpIncremental, nil), Default: strconv.FormatBool(defaultIncremental)},
		{Key: settingMinResultChars, Env: "JEV_MIN_RESULT_CHARS", Help: texts.english(helpMinResultChars, nil), Default: strconv.Itoa(defaultMinResultChars)},
		{Key: settingMaxPruned, Env: "JEV_MAX_PRUNED", Help: texts.english(helpMaxPruned, nil), Default: strconv.Itoa(defaultMaxPruned)},
		{Key: settingMaxRemovedShare, Env: "JEV_MAX_REMOVED_SHARE", Help: texts.english(helpMaxRemovedShare, nil), Default: strconv.FormatFloat(fullShare, 'g', -1, 64)},
		{Key: settingStateInputRunes, Env: "JEV_STATE_INPUT_RUNES", Help: texts.english(helpStateInputRunes, nil), Default: defaultInputLadder().String()},
		{Key: settingStateTextHead, Env: "JEV_STATE_TEXT_HEAD", Help: texts.english(helpStateTextHead, nil), Default: strconv.Itoa(defaultTextHeadRunes)},
		{Key: settingStateTextTail, Env: "JEV_STATE_TEXT_TAIL", Help: texts.english(helpStateTextTail, nil), Default: strconv.Itoa(defaultTextTailRunes)},
		{Key: settingStateContext, Env: "JEV_STATE_CONTEXT", Help: texts.english(helpStateContext, nil)},
		{Key: settingStateStages, Env: "JEV_STATE_STAGES", Help: texts.english(helpStateStages, nil), Default: defaultStageOrder().String()},
		{Key: settingGoalSource, Env: "JEV_GOAL_SOURCE", Help: texts.english(helpGoalSource, nil), Default: "auto"},
		{Key: settingGoalText, Env: "JEV_GOAL_TEXT", Help: texts.english(helpGoalText, nil)},
		{Key: settingGoalPrompts, Env: "JEV_GOAL_PROMPTS", Help: texts.english(helpGoalPrompts, nil), Default: strconv.Itoa(defaultGoalPrompts)},
		{Key: settingGoalPromptRunes, Env: "JEV_GOAL_PROMPT_RUNES", Help: texts.english(helpGoalPromptRunes, nil), Default: strconv.Itoa(defaultGoalPromptRunes)},
		{Key: settingPreserveFirst, Env: "JEV_PRESERVE_FIRST", Help: texts.english(helpPreserveFirst, nil), Default: strconv.FormatBool(defaultPreserveFirst)},
		{Key: settingPreserveRecentBudget, Env: "JEV_PRESERVE_RECENT_TOKENS", Help: texts.english(helpPreserveRecentBudget, nil), Default: "0"},
		{Key: settingPreserveTurns, Env: "JEV_PRESERVE_TURNS", Help: texts.english(helpPreserveTurns, nil), Default: "0"},
		{Key: settingRetries, Env: "JEV_RETRIES", Help: texts.english(helpRetries, nil), Default: strconv.Itoa(defaultRetries)},
		{Key: settingRetryBackoff, Env: "JEV_RETRY_BACKOFF", Help: texts.english(helpRetryBackoff, nil), Default: defaultRetryBackoff.String()},
		{Key: settingMaxRequests, Env: "JEV_MAX_REQUESTS", Help: texts.english(helpMaxRequests, nil), Default: "0"},
		{Key: settingMaxSent, Env: "JEV_MAX_SENT_TOKENS", Help: texts.english(helpMaxSent, nil), Default: "0"},
		{Key: settingDecisionLog, Env: "JEV_DECISION_LOG", Help: texts.english(helpDecisionLog, nil)},
		{Key: settingReplayDir, Env: "JEV_REPLAY_DIR", Help: texts.english(helpReplayDir, nil)},
	}
}

func refuseIncoherent(read *settingReader, opts options) {
	if opts.maxStateTokens >= opts.maxRequestTokens {
		read.refuseStateAboveRequest(opts.maxStateTokens, opts.maxRequestTokens)
	}
	if opts.maxRemovedShare > 0 && opts.minReduction > opts.maxRemovedShare {
		read.refuse(settingMinReduction, strconv.FormatFloat(opts.minReduction, 'f', -1, 64))
	}
}

func parseOptions(cfg contracts.PluginConfig) (options, error) {
	if cfg.Get(settingAPIKey) == "" {
		return options{}, &missingKeyError{}
	}
	return readOptions(cfg)
}

func readOptions(cfg contracts.PluginConfig) (options, error) {
	read := settingReader{cfg: cfg, texts: newMessages()}
	opts := options{
		apiKey:               cfg.Get(settingAPIKey),
		model:                read.text(settingModel, defaultModel),
		endpoint:             read.endpoint(),
		keepThreshold:        read.ratio(settingKeepThreshold, defaultKeepThreshold),
		keepCall:             read.optionalRatio(settingKeepCallThreshold),
		keepResult:           read.optionalRatio(settingKeepResultThreshold),
		preserveRecent:       read.count(settingPreserveRecent, defaultPreserveRecent, 0),
		truncateHead:         read.count(settingTruncateHead, defaultTruncateHead, 0),
		truncateTail:         read.count(settingTruncateTail, defaultTruncateTail, 0),
		minReduction:         read.ratio(settingMinReduction, defaultMinReduction),
		maxStateTokens:       read.count(settingMaxStateTokens, defaultMaxStateTokens, 1),
		maxRequestTokens:     read.count(settingMaxRequestTokens, defaultMaxRequestTokens, 1),
		timeout:              read.duration(settingTimeout, defaultTimeout),
		maxParallel:          read.count(settingMaxParallel, 0, 1),
		triggerPercent:       read.triggerPercent(),
		onShort:              read.onShort(),
		reductionUnit:        read.reductionUnit(),
		dryRun:               read.flag(settingDryRun),
		policy:               read.policy(),
		keepErrors:           read.flag(settingKeepErrors),
		includeEmpty:         read.flag(settingIncludeEmpty),
		incremental:          read.flag(settingIncremental),
		minResultChars:       read.count(settingMinResultChars, defaultMinResultChars, 0),
		maxPruned:            read.count(settingMaxPruned, defaultMaxPruned, 0),
		maxRemovedShare:      read.share(settingMaxRemovedShare),
		stateInputRunes:      read.inputLadder(),
		stateTextHead:        read.count(settingStateTextHead, defaultTextHeadRunes, 0),
		stateTextTail:        read.count(settingStateTextTail, defaultTextTailRunes, 0),
		stateContext:         read.text(settingStateContext, ""),
		stateStages:          read.stageOrder(),
		goalSource:           read.goalSource(),
		goalText:             strings.TrimSpace(read.text(settingGoalText, "")),
		goalPrompts:          read.count(settingGoalPrompts, 0, 1),
		goalPromptRunes:      read.count(settingGoalPromptRunes, 0, 1),
		preserveFirst:        read.optionalFlag(settingPreserveFirst),
		preserveRecentTokens: read.count(settingPreserveRecentBudget, 0, 0),
		preserveTurns:        read.count(settingPreserveTurns, 0, 0),
		retries:              read.count(settingRetries, defaultRetries, 0),
		retryBackoff:         read.duration(settingRetryBackoff, defaultRetryBackoff),
		maxRequests:          read.count(settingMaxRequests, 0, 0),
		maxSentTokens:        read.count(settingMaxSent, 0, 0),
		decisionLog:          read.text(settingDecisionLog, ""),
		replayDir:            read.replayDir(),
	}
	if opts.goalSource == goalSourceFixed && opts.goalText == "" {
		read.refuse(settingGoalText, "")
	}
	if opts.retries > maxRetries {
		read.refuse(settingRetries, strconv.Itoa(opts.retries))
	}
	refuseIncoherent(&read, opts)
	if read.err != nil {
		return options{}, read.err
	}
	return opts, nil
}

type settingReader struct {
	cfg   contracts.PluginConfig
	err   error
	texts *messages
}

func (r *settingReader) refuseStateAboveRequest(state, request int) {
	if r.err != nil {
		return
	}
	r.err = &incoherentError{
		message: r.texts.english(errorStateAboveRequest, map[string]any{"State": state, "Request": request}),
		setting: settingMaxStateTokens + ", " + settingMaxRequestTokens,
		value:   strconv.Itoa(state) + ", " + strconv.Itoa(request),
	}
}

func (r *settingReader) replayDir() string {
	dir := r.cfg.Get(settingReplayDir)
	if info, err := os.Stat(dir); err == nil && !info.IsDir() {
		r.refuse(settingReplayDir, dir)
	}
	return dir
}

func (r *settingReader) raw(key string) (string, bool) {
	value := r.cfg.Get(key)
	return value, value != ""
}

func (r *settingReader) refuse(key, value string) {
	if r.err == nil {
		r.err = &settingError{key: key, value: value}
	}
}

func (r *settingReader) text(key, fallback string) string {
	if value, ok := r.raw(key); ok {
		return value
	}
	return fallback
}

func (r *settingReader) endpoint() string {
	value, ok := r.raw(settingEndpoint)
	if !ok {
		return defaultEndpoint
	}
	parsed, err := url.Parse(value)
	if err != nil || parsed.Scheme != "https" || parsed.Host == "" || parsed.User != nil {
		r.refuse(settingEndpoint, redactedValue)
		return ""
	}
	return parsed.String()
}

const redactedValue = "<redacted>"

func parseRatio(text string) (float64, bool) {
	parsed, err := strconv.ParseFloat(text, 64)
	if err != nil || math.IsNaN(parsed) || parsed < 0 || parsed > 1 {
		return 0, false
	}
	return parsed, true
}

func (r *settingReader) ratio(key string, fallback float64) float64 {
	value, ok := r.raw(key)
	if !ok {
		return fallback
	}
	parsed, valid := parseRatio(value)
	if !valid {
		r.refuse(key, value)
		return 0
	}
	return parsed
}

func (r *settingReader) optionalRatio(key string) optionalRatio {
	if _, ok := r.raw(key); !ok {
		return optionalRatio{}
	}
	return optionalRatio{value: r.ratio(key, 0), set: true}
}

func (r *settingReader) share(key string) float64 {
	value, ok := r.raw(key)
	if !ok {
		return 0
	}
	parsed, valid := parseRatio(value)
	if !valid || parsed == 0 {
		r.refuse(key, value)
		return 0
	}
	return parsed
}

func (r *settingReader) count(key string, fallback, least int) int {
	value, ok := r.raw(key)
	if !ok {
		return fallback
	}
	parsed, err := strconv.Atoi(value)
	if err != nil || parsed < least {
		r.refuse(key, value)
		return 0
	}
	return parsed
}

func (r *settingReader) triggerPercent() int {
	value, ok := r.raw(settingTriggerPercent)
	if !ok {
		return 0
	}
	parsed, err := strconv.Atoi(value)
	if err != nil || contracts.ValidateStageTriggerPercent(parsed) != nil {
		r.refuse(settingTriggerPercent, value)
		return 0
	}
	return parsed
}

func (r *settingReader) onShort() onShort {
	value, ok := r.raw(settingOnShort)
	if !ok {
		return 0
	}
	parsed, valid := parseOnShort(value)
	if !valid {
		r.refuse(settingOnShort, value)
		return 0
	}
	return parsed
}

func (r *settingReader) reductionUnit() reductionUnit {
	value, ok := r.raw(settingReductionUnit)
	if !ok {
		return 0
	}
	parsed, valid := parseReductionUnit(value)
	if !valid {
		r.refuse(settingReductionUnit, value)
		return 0
	}
	return parsed
}

func (r *settingReader) inputLadder() inputLadder {
	value, ok := r.raw(settingStateInputRunes)
	if !ok {
		return inputLadder{}
	}
	parsed, valid := parseInputLadder(value)
	if !valid {
		r.refuse(settingStateInputRunes, value)
		return inputLadder{}
	}
	return parsed
}

func (r *settingReader) stageOrder() stageOrder {
	value, ok := r.raw(settingStateStages)
	if !ok {
		return stageOrder{}
	}
	parsed, valid := parseStageOrder(value)
	if !valid {
		r.refuse(settingStateStages, value)
		return stageOrder{}
	}
	return parsed
}

func (r *settingReader) goalSource() goalSource {
	value, ok := r.raw(settingGoalSource)
	if !ok {
		return 0
	}
	parsed, valid := parseGoalSource(value)
	if !valid {
		r.refuse(settingGoalSource, value)
		return 0
	}
	return parsed
}

func (r *settingReader) policy() *callPolicy {
	rulesText, hasRules := r.raw(settingRules)
	pinsText, hasPins := r.raw(settingPinInputs)
	if !hasRules && !hasPins {
		return nil
	}
	policy := &callPolicy{}
	if hasRules {
		parsed, valid := parseRules(rulesText)
		if !valid {
			r.refuse(settingRules, rulesText)
		}
		policy.rules = parsed
	}
	if hasPins {
		pins, valid := parseList(pinsText, nonEmpty)
		if !valid {
			r.refuse(settingPinInputs, pinsText)
		}
		policy.pins = pins
	}
	return policy
}

func (r *settingReader) optionalFlag(key string) optionalFlag {
	if _, ok := r.raw(key); !ok {
		return optionalFlag{}
	}
	return optionalFlag{value: r.flag(key), set: true}
}

func (r *settingReader) flag(key string) bool {
	value, ok := r.raw(key)
	if !ok {
		return false
	}
	parsed, err := strconv.ParseBool(value)
	if err != nil {
		r.refuse(key, value)
		return false
	}
	return parsed
}

func (r *settingReader) duration(key string, fallback time.Duration) time.Duration {
	value, ok := r.raw(key)
	if !ok {
		return fallback
	}
	parsed, err := time.ParseDuration(value)
	if err != nil || parsed <= 0 {
		r.refuse(key, value)
		return 0
	}
	return parsed
}
