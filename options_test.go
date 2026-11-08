package jev

import (
	"errors"
	"testing"
	"time"

	contracts "github.com/Herrscherd/herrscher-contracts"
)

func TestParseOptionsAppliesTheDefaults(t *testing.T) {
	opts, err := parseOptions(contracts.PluginConfig{Settings: map[string]string{settingAPIKey: "key"}})
	if err != nil {
		t.Fatalf("parseOptions: %v", err)
	}
	want := options{
		apiKey: "key", model: defaultModel, endpoint: defaultEndpoint,
		keepThreshold: defaultKeepThreshold, preserveRecent: defaultPreserveRecent,
		truncateHead: defaultTruncateHead, minReduction: defaultMinReduction,
		maxStateTokens: defaultMaxStateTokens, maxRequestTokens: defaultMaxRequestTokens,
		timeout: defaultTimeout,
	}
	if opts != want {
		t.Fatalf("parseOptions = %+v, want %+v", opts, want)
	}
}

func TestParseOptionsReadsTheDeclaredDefaults(t *testing.T) {
	cfg, err := contracts.Resolve(settings(newMessages()), func(name string) string {
		if name == typesafeEnv {
			return "key"
		}
		return ""
	})
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	opts, err := parseOptions(cfg)
	if err != nil || opts.timeout != 2*time.Minute || opts.keepThreshold != defaultKeepThreshold {
		t.Fatalf("parseOptions(declared defaults) = %+v, %v", opts, err)
	}
}

func TestParseOptionsRefusesBadValues(t *testing.T) {
	cases := map[string]string{
		settingEndpoint:         "http://api.typesafe.ai/v1/systemone",
		settingKeepThreshold:    "1.5",
		settingMinReduction:     "NaN",
		settingPreserveRecent:   "-1",
		settingMaxStateTokens:   "0",
		settingMaxRequestTokens: "lots",
		settingTimeout:          "0s",
	}
	for key, value := range cases {
		_, err := parseOptions(contracts.PluginConfig{Settings: map[string]string{settingAPIKey: "key", key: value}})
		if !errors.Is(err, ErrInvalidSetting) {
			t.Errorf("parseOptions(%s=%q) error = %v, want ErrInvalidSetting", key, value, err)
		}
		if notice := contracts.NoticeOf(err); notice.Code != contracts.CodeSettingInvalid {
			t.Errorf("notice of %s = %+v, want CodeSettingInvalid", key, notice)
		}
	}
}

func TestParseOptionsNeedsAnAPIKey(t *testing.T) {
	_, err := parseOptions(contracts.PluginConfig{})
	if !errors.Is(err, ErrMissingAPIKey) || contracts.NoticeOf(err).Code != contracts.CodeSettingMissing {
		t.Fatalf("parseOptions error = %v, want ErrMissingAPIKey with CodeSettingMissing", err)
	}
}
