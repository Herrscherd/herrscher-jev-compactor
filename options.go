package jev

import (
	"errors"
	"fmt"
	"math"
	"net/url"
	"strconv"
	"time"

	contracts "github.com/Herrscherd/herrscher-contracts"
)

const (
	settingAPIKey              = "api_key"
	settingModel               = "model"
	settingEndpoint            = "endpoint"
	settingKeepThreshold       = "keep_threshold"
	settingKeepCallThreshold   = "keep_call_threshold"
	settingKeepResultThreshold = "keep_result_threshold"
	settingPreserveRecent      = "preserve_recent"
	settingTruncateHead        = "truncate_head"
	settingMinReduction        = "min_reduction"
	settingMaxStateTokens      = "max_state_tokens"
	settingMaxRequestTokens    = "max_request_tokens"
	settingTimeout             = "timeout"
	settingMaxParallel         = "max_parallel"
	settingOnShort             = "on_short"
	settingDryRun              = "dry_run"
	settingRules               = "rules"
	settingPinInputs           = "pin_inputs"
	settingKeepErrors          = "keep_errors"
	settingMinResultChars      = "min_result_chars"
	settingMaxPruned           = "max_pruned"
	settingMaxRemovedShare     = "max_removed_share"
)

const (
	typesafeEnv             = "TYPESAFE_API_KEY"
	defaultModel            = "jev-latest"
	defaultEndpoint         = "https://api.typesafe.ai/v1/systemone"
	defaultKeepThreshold    = 0.5
	defaultPreserveRecent   = 6
	defaultTruncateHead     = 300
	defaultMinReduction     = 0.25
	defaultMaxStateTokens   = 25000
	defaultMaxRequestTokens = 30000
	defaultTimeout          = 2 * time.Minute
	defaultMaxParallel      = 4
	defaultOnShort          = onShortKeep
	defaultKeepErrors       = false
	defaultMinResultChars   = 0
	defaultMaxPruned        = 0
)

type onShort uint8

const (
	onShortKeep onShort = iota + 1
	onShortDone
)

func parseOnShort(text string) (onShort, bool) {
	switch text {
	case keepName:
		return onShortKeep, true
	case "done":
		return onShortDone, true
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

type options struct {
	apiKey           string
	model            string
	endpoint         string
	keepThreshold    float64
	keepCall         optionalRatio
	keepResult       optionalRatio
	preserveRecent   int
	truncateHead     int
	minReduction     float64
	maxStateTokens   int
	maxRequestTokens int
	timeout          time.Duration
	maxParallel      int
	onShort          onShort
	dryRun           bool
	policy           *callPolicy
	keepErrors       bool
	minResultChars   int
	maxPruned        int
	maxRemovedShare  float64
}

func (o options) parallelism() int {
	return orDefault(o.maxParallel, defaultMaxParallel)
}

func (o options) shortRule() onShort {
	return orDefault(o.onShort, defaultOnShort)
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
		{Key: settingMinReduction, Env: "JEV_MIN_REDUCTION", Help: texts.english(helpMinReduction, nil), Default: strconv.FormatFloat(defaultMinReduction, 'g', -1, 64)},
		{Key: settingMaxStateTokens, Env: "JEV_MAX_STATE_TOKENS", Help: texts.english(helpStateBudget, nil), Default: strconv.Itoa(defaultMaxStateTokens)},
		{Key: settingMaxRequestTokens, Env: "JEV_MAX_REQUEST_TOKENS", Help: texts.english(helpRequestBudget, nil), Default: strconv.Itoa(defaultMaxRequestTokens)},
		{Key: settingTimeout, Env: "JEV_TIMEOUT", Help: texts.english(helpTimeout, nil), Default: defaultTimeout.String()},
		{Key: settingMaxParallel, Env: "JEV_MAX_PARALLEL", Help: texts.english(helpMaxParallel, nil), Default: strconv.Itoa(defaultMaxParallel)},
		{Key: settingOnShort, Env: "JEV_ON_SHORT", Help: texts.english(helpOnShort, nil), Default: keepName},
		{Key: settingDryRun, Env: "JEV_DRY_RUN", Help: texts.english(helpDryRun, nil), Default: "false"},
		{Key: settingRules, Env: "JEV_RULES", Help: texts.english(helpRules, nil)},
		{Key: settingPinInputs, Env: "JEV_PIN_INPUTS", Help: texts.english(helpPinInputs, nil)},
		{Key: settingKeepErrors, Env: "JEV_KEEP_ERRORS", Help: texts.english(helpKeepErrors, nil), Default: strconv.FormatBool(defaultKeepErrors)},
		{Key: settingMinResultChars, Env: "JEV_MIN_RESULT_CHARS", Help: texts.english(helpMinResultChars, nil), Default: strconv.Itoa(defaultMinResultChars)},
		{Key: settingMaxPruned, Env: "JEV_MAX_PRUNED", Help: texts.english(helpMaxPruned, nil), Default: strconv.Itoa(defaultMaxPruned)},
		{Key: settingMaxRemovedShare, Env: "JEV_MAX_REMOVED_SHARE", Help: texts.english(helpMaxRemovedShare, nil), Default: strconv.FormatFloat(fullShare, 'g', -1, 64)},
	}
}

func parseOptions(cfg contracts.PluginConfig) (options, error) {
	apiKey := cfg.Get(settingAPIKey)
	if apiKey == "" {
		return options{}, &missingKeyError{}
	}
	read := settingReader{cfg: cfg}
	opts := options{
		apiKey:           apiKey,
		model:            read.text(settingModel, defaultModel),
		endpoint:         read.endpoint(),
		keepThreshold:    read.ratio(settingKeepThreshold, defaultKeepThreshold),
		keepCall:         read.optionalRatio(settingKeepCallThreshold),
		keepResult:       read.optionalRatio(settingKeepResultThreshold),
		preserveRecent:   read.count(settingPreserveRecent, defaultPreserveRecent, 0),
		truncateHead:     read.count(settingTruncateHead, defaultTruncateHead, 0),
		minReduction:     read.ratio(settingMinReduction, defaultMinReduction),
		maxStateTokens:   read.count(settingMaxStateTokens, defaultMaxStateTokens, 1),
		maxRequestTokens: read.count(settingMaxRequestTokens, defaultMaxRequestTokens, 1),
		timeout:          read.duration(settingTimeout, defaultTimeout),
		maxParallel:      read.count(settingMaxParallel, 0, 1),
		onShort:          read.onShort(),
		dryRun:           read.flag(settingDryRun),
		policy:           read.policy(),
		keepErrors:       read.flag(settingKeepErrors),
		minResultChars:   read.count(settingMinResultChars, defaultMinResultChars, 0),
		maxPruned:        read.count(settingMaxPruned, defaultMaxPruned, 0),
		maxRemovedShare:  read.share(settingMaxRemovedShare),
	}
	if read.err != nil {
		return options{}, read.err
	}
	return opts, nil
}

type settingReader struct {
	cfg contracts.PluginConfig
	err error
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
		r.refuse(settingEndpoint, value)
		return ""
	}
	return parsed.String()
}

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
