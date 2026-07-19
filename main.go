package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"latentdream/harness/internal/config"
	"latentdream/harness/internal/environment/clipboard"
	"latentdream/harness/internal/input"
	"latentdream/harness/internal/logging"
	"latentdream/harness/internal/provider"
	"latentdream/harness/internal/runtime"
	"latentdream/harness/internal/runtime/command"
	"latentdream/harness/internal/tracing"
	"latentdream/harness/internal/tui"

	"github.com/google/uuid"
	"go.uber.org/zap"
)

func main() {
	exitCode := run()
	_ = logging.Sync()
	if exitCode != 0 {
		os.Exit(exitCode)
	}
}

func run() int {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	cfg, err := config.Load()
	if err != nil {
		_, _ = fmt.Fprintf(os.Stderr, "failed to load config: %v\n", err)
		return 1
	}

	sessionID := uuid.NewString()
	if cfg.Logging.InitialFields == nil {
		cfg.Logging.InitialFields = map[string]any{}
	}
	cfg.Logging.InitialFields["sessionId"] = sessionID
	logging.ConfigureOrExitForSession(cfg.Logging, sessionID)

	ctx = tracing.Init(ctx, tracing.LogRecorder(cfg.Tracing.FilePath, sessionID))

	aiProvider, err := provider.New(cfg.Providers)
	if err != nil {
		_, _ = fmt.Fprintf(os.Stderr, "failed to initialize provider: %v\n", err)
		logging.Log(ctx).Error("provider initialization failure", zap.Error(err))
		return 1
	}

	selection := aiProvider.Current()
	commands := command.DefaultRegistry()
	runtimeOptions := runtime.Options{Commands: commands, Clipboard: clipboard.NewSystem()}

	if tui.IsInteractive(os.Stdin, os.Stdout) {
		workingDirectory, _ := os.Getwd()

		frontend, err := tui.New(os.Stdin, os.Stdout, os.Stderr, tui.Options{
			Provider:         selection.Provider,
			Model:            selection.Model,
			WorkingDirectory: workingDirectory,
			Commands:         commands,
			Models:           aiProvider.Available(),
		})
		if err != nil {
			_, _ = fmt.Fprintf(os.Stderr, "failed to initialize terminal UI: %v\n", err)
			return 1
		}

		harness := runtime.New(aiProvider, frontend, frontend, runtimeOptions)
		err = frontend.Run(ctx, harness.Run)

	} else {
		frontend := input.NewTerminal(os.Stdin, os.Stdout, os.Stderr)
		_ = frontend.Writef("Harness (%s/%s)", selection.Provider, selection.Model)
		harness := runtime.New(aiProvider, frontend, frontend, runtimeOptions)
		err = harness.Run(ctx)
	}

	if err != nil && !errors.Is(err, context.Canceled) {
		_, _ = fmt.Fprintf(os.Stderr, "runtime failed: %v\n", err)
		logging.Log(ctx).Error("runtime failure", zap.Error(err))
		return 1
	}
	return 0
}
