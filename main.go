package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
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
	loggingConfig := loggingConfigForSession(cfg.Logging, sessionID)
	logging.ConfigureOrExitForSession(loggingConfig, sessionID)

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

		err = frontend.Run(ctx, func(runCtx context.Context) error {
			return runSessions(runCtx, cfg, sessionID, aiProvider, frontend, frontend, runtimeOptions)
		})

	} else {
		frontend := input.NewTerminal(os.Stdin, os.Stdout, os.Stderr)
		_ = frontend.Writef("Harness (%s/%s)", selection.Provider, selection.Model)
		err = runSessions(ctx, cfg, sessionID, aiProvider, frontend, frontend, runtimeOptions)
	}

	if err != nil && !errors.Is(err, context.Canceled) {
		_, _ = fmt.Fprintf(os.Stderr, "runtime failed: %v\n", err)
		logging.Log(ctx).Error("runtime failure", zap.Error(err))
		return 1
	}
	return 0
}

func runSessions(
	ctx context.Context,
	cfg config.Config,
	sessionID string,
	aiProvider provider.Provider,
	receiver input.Receiver,
	output input.Sink,
	options runtime.Options,
) error {
	first := true
	for {
		if !first {
			_ = logging.Sync()
			sessionID = uuid.NewString()
			loggingConfig := loggingConfigForSession(cfg.Logging, sessionID)
			if loggingConfig.Output == logging.OutputFile && !strings.Contains(loggingConfig.FilePath, logging.SessionIDPlaceholder) {
				loggingConfig.FilePath = sessionScopedPath(loggingConfig.FilePath, sessionID)
			}
			if err := logging.ConfigureForSession(loggingConfig, sessionID); err != nil {
				return fmt.Errorf("configure session logging: %w", err)
			}
		}

		tracePath := cfg.Tracing.FilePath
		if !first && !strings.Contains(tracePath, logging.SessionIDPlaceholder) {
			tracePath = sessionScopedPath(tracePath, sessionID)
		}
		sessionCtx := tracing.Init(ctx, tracing.LogRecorder(tracePath, sessionID))
		options.ResetFrontend = !first
		harness := runtime.New(aiProvider, receiver, output, options)
		action, err := harness.RunSession(sessionCtx)
		if err != nil {
			return err
		}
		if action != command.ActionNewSession {
			return nil
		}
		first = false
	}
}

func loggingConfigForSession(cfg logging.Config, sessionID string) logging.Config {
	fields := make(map[string]any, len(cfg.InitialFields)+1)
	for key, value := range cfg.InitialFields {
		fields[key] = value
	}
	fields["sessionId"] = sessionID
	cfg.InitialFields = fields
	return cfg
}

func sessionScopedPath(path string, sessionID string) string {
	directory, name := filepath.Split(path)
	return filepath.Join(directory, sessionID, name)
}
