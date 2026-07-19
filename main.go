package main

import (
	"context"
	"errors"
	"os"
	"os/signal"
	"syscall"

	"latentdream/harness/internal/config"
	"latentdream/harness/internal/input"
	"latentdream/harness/internal/logging"
	"latentdream/harness/internal/provider"
	"latentdream/harness/internal/runtime"
	"latentdream/harness/internal/tracing"

	"github.com/google/uuid"
	"go.uber.org/zap"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	ui := input.NewTerminal(os.Stdin, os.Stdout, os.Stderr)

	cfg, err := config.Load()
	if err != nil {
		ui.WriteErrf("failed to load config: %v", err)
		os.Exit(1)
	}

	sessionID := uuid.NewString()
	if cfg.Logging.InitialFields == nil {
		cfg.Logging.InitialFields = map[string]any{}
	}
	cfg.Logging.InitialFields["sessionId"] = sessionID
	logging.ConfigureOrExitForSession(cfg.Logging, sessionID)
	defer func() { _ = logging.Sync() }()

	ctx = tracing.Init(ctx, tracing.LogRecorder(cfg.Tracing.FilePath, sessionID))

	aiProvider, err := provider.New(cfg.Providers)
	if err != nil {
		ui.WriteErrf("failed to initialize provider: %v", err)
		logging.Log(ctx).Fatal("provider initialization failure", zap.Error(err))
	}

	selection := aiProvider.Current()
	ui.Writef("Harness (%s/%s)", selection.Provider, selection.Model)

	harness := runtime.New(aiProvider, ui, ui)
	if err := harness.Run(ctx); err != nil {
		if errors.Is(err, context.Canceled) {
			return
		}
		ui.WriteErrf("runtime failed: %v", err)
		logging.Log(ctx).Fatal("runtime failure", zap.Error(err))
	}
}
