package jev

import (
	"context"

	contracts "github.com/Herrscherd/herrscher-contracts"
)

const compactorKind = "jev"

func init() {
	texts := newMessages()
	contracts.Register(contracts.Plugin{
		Manifest: contracts.Manifest{
			Kind:         compactorKind,
			Category:     contracts.CategoryCompactor,
			Status:       contracts.StatusLive,
			Capabilities: contracts.Capabilities{Compactor: true},
			Config:       settings(texts),
		},
		Compactor: func(_ context.Context, cfg contracts.PluginConfig) (contracts.Compactor, error) {
			return newCompactor(cfg, texts, nil)
		},
	})
}
