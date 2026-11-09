package jev

import (
	"errors"
	"strings"
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
		truncateHead: defaultTruncateHead, truncateTail: defaultTruncateTail, minReduction: defaultMinReduction,
		maxStateTokens: defaultMaxStateTokens, maxRequestTokens: defaultMaxRequestTokens,
		timeout: defaultTimeout, retryBackoff: defaultRetryBackoff,
		stateTextHead: defaultTextHeadRunes, stateTextTail: defaultTextTailRunes,
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
		settingTruncateTail:     "-1",
		settingMaxStateTokens:   "0",
		settingMaxRequestTokens: "lots",
		settingTimeout:          "0s",
		settingTriggerPercent:   "101",
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

func TestParseOptionsRefusesIncoherentSettings(t *testing.T) {
	cases := map[string]map[string]string{
		"state as large as the request": {settingMaxStateTokens: "30000", settingMaxRequestTokens: "30000"},
		"state larger than the request": {settingMaxStateTokens: "40000", settingMaxRequestTokens: "30000"},
		"target above the removal cap":  {settingMinReduction: "0.5", settingMaxRemovedShare: "0.3"},
		"tokens target above the cap":   {settingReductionUnit: "tokens", settingMinReduction: "0.5", settingMaxRemovedShare: "0.3"},
	}
	for name, settings := range cases {
		settings[settingAPIKey] = "key"
		_, err := parseOptions(contracts.PluginConfig{Settings: settings})
		if !errors.Is(err, ErrInvalidSetting) {
			t.Errorf("%s: parseOptions error = %v, want ErrInvalidSetting", name, err)
		}
	}
	coherent := map[string]string{
		settingAPIKey:           "key",
		settingMinReduction:     "0.2",
		settingMaxRemovedShare:  "0.3",
		settingReductionUnit:    "tokens",
		settingMaxStateTokens:   "20000",
		settingMaxRequestTokens: "30000",
	}
	if _, err := parseOptions(contracts.PluginConfig{Settings: coherent}); err != nil {
		t.Fatalf("parseOptions(coherent) error = %v, want none", err)
	}
}

func TestParseOptionsNamesBothTokenSettingsWhenStateIsAboveRequest(t *testing.T) {
	settings := map[string]string{settingAPIKey: "key", settingMaxStateTokens: "40000", settingMaxRequestTokens: "30000"}
	_, err := parseOptions(contracts.PluginConfig{Settings: settings})
	for _, name := range []string{settingMaxStateTokens, settingMaxRequestTokens} {
		if err == nil || !strings.Contains(err.Error(), name) {
			t.Fatalf("parseOptions error = %v, want it to name %s", err, name)
		}
	}
}

func TestTriggerPercentReachesTheHostThroughStageTrigger(t *testing.T) {
	var stage contracts.Compactor = newTestCompactor(t, &fakeJev{}, map[string]string{settingTriggerPercent: "70"})
	trigger, ok := stage.(contracts.StageTrigger)
	if !ok {
		t.Fatal("the Jev stage does not implement contracts.StageTrigger")
	}
	if got := trigger.TriggerPercent(); got != 70 {
		t.Fatalf("TriggerPercent = %d, want 70 from trigger_percent", got)
	}
}

func TestTriggerPercentDefaultsToTheHostThreshold(t *testing.T) {
	if got := newTestCompactor(t, &fakeJev{}, nil).TriggerPercent(); got != 0 {
		t.Fatalf("TriggerPercent = %d, want 0 without trigger_percent", got)
	}
}

func TestTriggerPercentRefusesANegativeValue(t *testing.T) {
	_, err := parseOptions(contracts.PluginConfig{Settings: map[string]string{settingAPIKey: "key", settingTriggerPercent: "-1"}})
	if !errors.Is(err, ErrInvalidSetting) {
		t.Fatalf("parseOptions(trigger_percent=-1) error = %v, want ErrInvalidSetting", err)
	}
}
