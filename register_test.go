package jev

import (
	"encoding/json"
	"errors"
	"io/fs"
	"slices"
	"testing"

	contracts "github.com/Herrscherd/herrscher-contracts"
	"github.com/Herrscherd/herrscher-contracts/i18n"
)

func TestRegisterDeclaresACompactorStage(t *testing.T) {
	var found *contracts.Plugin
	for _, plugin := range contracts.Default.Compactors() {
		if plugin.Manifest.Kind == compactorKind {
			found = &plugin
		}
	}
	if found == nil {
		t.Fatal("no compactor registered under jev")
	}
	if !found.Manifest.Capabilities.Compactor || found.Compactor == nil || found.Backend != nil {
		t.Fatalf("manifest = %+v, want a compactor factory and no backend", found.Manifest)
	}
	if _, err := found.Compactor(t.Context(), contracts.PluginConfig{}); !errors.Is(err, ErrMissingAPIKey) {
		t.Fatalf("factory without a key error = %v, want ErrMissingAPIKey", err)
	}
	if found.Probe == nil {
		t.Fatal("jev registers no probe")
	}
	if report := found.Probe(t.Context(), contracts.ProbeAccess{}); report.State != contracts.HealthDown || report.Detail.Code != contracts.CodeHealthEnvMissing {
		t.Fatalf("probe without a key = %+v, want down and the variable named", report)
	}
	stage, err := found.Compactor(t.Context(), contracts.PluginConfig{Settings: map[string]string{settingAPIKey: "key"}})
	if err != nil || stage == nil {
		t.Fatalf("factory = %v, %v", stage, err)
	}
}

func TestEveryMessageRendersInEveryLanguage(t *testing.T) {
	sources, err := fs.Sub(localeFiles, "locales")
	if err != nil {
		t.Fatal(err)
	}
	catalog, err := i18n.NewCatalog(nil, sources)
	if err != nil {
		t.Fatalf("NewCatalog: %v", err)
	}
	params := map[string]any{"Count": 2, "Kept": 1, "Truncated": 0, "Pruned": 1, "Percent": 30, "Target": 25, "Reason": "x"}
	keys := []string{
		helpAccess, helpModel, helpEndpoint, helpKeepThreshold, helpPreserveRecent,
		helpTruncateHead, helpMinReduction, helpStateBudget, helpRequestBudget,
		helpTimeout, noteNoHistory, noteNothingToPrune, notePruned, notePrunedShort, noteFailed,
	}
	for _, lang := range i18n.Languages() {
		for _, key := range keys {
			text, err := catalog.Message(lang, key, params)
			if err != nil || text == key {
				t.Errorf("%s %s = %q, %v", lang, key, text, err)
			}
		}
	}
}

func TestLocalesShareTheirKeys(t *testing.T) {
	ids := func(name string) []string {
		raw, err := localeFiles.ReadFile("locales/" + name)
		if err != nil {
			t.Fatal(err)
		}
		var entries []struct {
			ID string `json:"id"`
		}
		if err := json.Unmarshal(raw, &entries); err != nil {
			t.Fatal(err)
		}
		keys := make([]string, 0, len(entries))
		for _, entry := range entries {
			keys = append(keys, entry.ID)
		}
		slices.Sort(keys)
		return keys
	}
	if english, french := ids("en.json"), ids("fr.json"); !slices.Equal(english, french) {
		t.Fatalf("en %v and fr %v differ", english, french)
	}
}
