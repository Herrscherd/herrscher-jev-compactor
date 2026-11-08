package jev

import (
	"context"

	contracts "github.com/Herrscherd/herrscher-contracts"
)

const compactorKind = "jev"

func init() {
	texts := newMessages()
	declared := settings(texts)
	contracts.Register(contracts.Plugin{
		Manifest: contracts.Manifest{
			Kind:         compactorKind,
			Category:     contracts.CategoryCompactor,
			Status:       contracts.StatusLive,
			Capabilities: contracts.Capabilities{Compactor: true},
			Config:       declared,
		},
		Compactor: func(_ context.Context, cfg contracts.PluginConfig) (contracts.Compactor, error) {
			return newCompactor(cfg, texts, nil)
		},
		Probe: prober{declared: declared}.probe,
	})
}
