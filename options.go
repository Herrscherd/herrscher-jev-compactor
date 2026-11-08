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
	settingAPIKey           = "api_key"
	settingModel            = "model"
	settingEndpoint         = "endpoint"
	settingKeepThreshold    = "keep_threshold"
	settingPreserveRecent   = "preserve_recent"
	settingTruncateHead     = "truncate_head"
	settingMinReduction     = "min_reduction"
	settingMaxStateTokens   = "max_state_tokens"
	settingMaxRequestTokens = "max_request_tokens"
	settingTimeout          = "timeout"
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
)

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

type options struct {
	apiKey           string
	model            string
	endpoint         string
	keepThreshold    float64
	preserveRecent   int
	truncateHead     int
	minReduction     float64
	maxStateTokens   int
	maxRequestTokens int
	timeout          time.Duration
}

func settings(texts *messages) []contracts.Setting {
	return []contracts.Setting{
		{Key: settingAPIKey, Env: typesafeEnv, Help: texts.english(helpAccess, nil), Required: true},
		{Key: settingModel, Env: "JEV_MODEL", Help: texts.english(helpModel, nil), Default: defaultModel},
		{Key: settingEndpoint, Env: "JEV_ENDPOINT", Help: texts.english(helpEndpoint, nil), Default: defaultEndpoint},
		{Key: settingKeepThreshold, Env: "JEV_KEEP_THRESHOLD", Help: texts.english(helpKeepThreshold, nil), Default: strconv.FormatFloat(defaultKeepThreshold, 'g', -1, 64)},
		{Key: settingPreserveRecent, Env: "JEV_PRESERVE_RECENT", Help: texts.english(helpPreserveRecent, nil), Default: strconv.Itoa(defaultPreserveRecent)},
		{Key: settingTruncateHead, Env: "JEV_TRUNCATE_HEAD", Help: texts.english(helpTruncateHead, nil), Default: strconv.Itoa(defaultTruncateHead)},
		{Key: settingMinReduction, Env: "JEV_MIN_REDUCTION", Help: texts.english(helpMinReduction, nil), Default: strconv.FormatFloat(defaultMinReduction, 'g', -1, 64)},
		{Key: settingMaxStateTokens, Env: "JEV_MAX_STATE_TOKENS", Help: texts.english(helpStateBudget, nil), Default: strconv.Itoa(defaultMaxStateTokens)},
		{Key: settingMaxRequestTokens, Env: "JEV_MAX_REQUEST_TOKENS", Help: texts.english(helpRequestBudget, nil), Default: strconv.Itoa(defaultMaxRequestTokens)},
		{Key: settingTimeout, Env: "JEV_TIMEOUT", Help: texts.english(helpTimeout, nil), Default: defaultTimeout.String()},
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
		preserveRecent:   read.count(settingPreserveRecent, defaultPreserveRecent, 0),
		truncateHead:     read.count(settingTruncateHead, defaultTruncateHead, 0),
		minReduction:     read.ratio(settingMinReduction, defaultMinReduction),
		maxStateTokens:   read.count(settingMaxStateTokens, defaultMaxStateTokens, 1),
		maxRequestTokens: read.count(settingMaxRequestTokens, defaultMaxRequestTokens, 1),
		timeout:          read.duration(settingTimeout, defaultTimeout),
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

func (r *settingReader) ratio(key string, fallback float64) float64 {
	value, ok := r.raw(key)
	if !ok {
		return fallback
	}
	parsed, err := strconv.ParseFloat(value, 64)
	if err != nil || math.IsNaN(parsed) || parsed < 0 || parsed > 1 {
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
