package jev

import (
	"embed"
	"fmt"
	"io/fs"
	"log/slog"
	"sync"

	"github.com/Herrscherd/herrscher-contracts/i18n"
)

//go:embed locales
var localeFiles embed.FS

const (
	helpAccess         = "jev.setting.api_key"
	helpModel          = "jev.setting.model"
	helpEndpoint       = "jev.setting.endpoint"
	helpKeepThreshold  = "jev.setting.keep_threshold"
	helpPreserveRecent = "jev.setting.preserve_recent"
	helpTruncateHead   = "jev.setting.truncate_head"
	helpMinReduction   = "jev.setting.min_reduction"
	helpStateBudget    = "jev.setting.max_state_tokens"
	helpRequestBudget  = "jev.setting.max_request_tokens"
	helpTimeout        = "jev.setting.timeout"
	helpMaxParallel    = "jev.setting.max_parallel"
	helpOnShort        = "jev.setting.on_short"
	helpDryRun         = "jev.setting.dry_run"
	noteFailed         = "jev.note.failed"
	noteNoHistory      = "jev.note.no_history"
	noteNothingToPrune = "jev.note.nothing_to_prune"
	notePruned         = "jev.note.pruned"
	notePrunedShort    = "jev.note.pruned_short"
	notePrunedStopped  = "jev.note.pruned_stopped"
	noteDryRun         = "jev.note.dry_run"
)

type messages struct {
	load func() (*i18n.Catalog, error)
}

func newMessages() *messages {
	return &messages{load: sync.OnceValues(loadCatalog)}
}

func loadCatalog() (*i18n.Catalog, error) {
	sources, err := fs.Sub(localeFiles, "locales")
	if err != nil {
		return nil, fmt.Errorf("jev locales: %w", err)
	}
	return i18n.NewCatalog(nil, sources)
}

func (m *messages) english(key string, params map[string]any) string {
	catalog, err := m.load()
	if err != nil {
		slog.Warn("jev: load messages", "key", key, "err", err)
		return key
	}
	text, err := catalog.Message(i18n.English, key, params)
	if err != nil {
		slog.Warn("jev: render message", "key", key, "err", err)
		return key
	}
	return text
}
