package main

import (
	"context"
	"os"

	"latentdream/harness/internal/config"
	"latentdream/harness/internal/input"
	"latentdream/harness/internal/logging"
	"latentdream/harness/internal/provider"
	"latentdream/harness/internal/runtime"

	"go.uber.org/zap"
)

func main() {
	ctx := context.Background() // TODO: proper ctx

	ui := input.NewTerminal(os.Stdin, os.Stdout, os.Stderr)

	cfg, err := config.Load()
	if err != nil {
		ui.WriteErrf("failed to load config: %v", err)
		os.Exit(1)
	}

	logging.ConfigureOrExit(cfg.Logging)

	aiProvider, err := provider.New(cfg.Providers)
	if err != nil {
		ui.WriteErrf("failed to initialize provider: %v", err)
		logging.Log(ctx).Fatal("provider initialization failure", zap.Error(err))
	}

	selection := aiProvider.Current()
	ui.Writef("Harness (%s/%s)", selection.Provider, selection.Model)
	ui.Write("Type :q or /quit to quit.")

	harness := runtime.New(aiProvider, ui)
	if err := harness.Run(ctx); err != nil {
		ui.WriteErrf("runtime failed: %v", err)
		logging.Log(ctx).Fatal("runtime failure", zap.Error(err))
	}
}
