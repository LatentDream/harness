package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"

	"latentdream/harness/internal/config"
	"latentdream/harness/internal/environment/clipboard"
	"latentdream/harness/internal/herdr"
	"latentdream/harness/internal/input"
	"latentdream/harness/internal/logging"
	"latentdream/harness/internal/provider"
	"latentdream/harness/internal/runtime"
	"latentdream/harness/internal/runtime/command"
	"latentdream/harness/internal/session"
	sessiontitle "latentdream/harness/internal/session/title"
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

	continueSession, err := parseStartupArgs(os.Args[1:])
	if err != nil {
		_, _ = fmt.Fprintf(os.Stderr, "%v\n", err)
		return 2
	}
	cfg, err := config.Load()
	if err != nil {
		_, _ = fmt.Fprintf(os.Stderr, "failed to load config: %v\n", err)
		return 1
	}

	workingDirectory, err := os.Getwd()
	if err != nil {
		_, _ = fmt.Fprintf(os.Stderr, "failed to resolve working directory: %v\n", err)
		return 1
	}
	workingDirectory, err = tracing.CanonicalWorkingDirectory(workingDirectory)
	if err != nil {
		_, _ = fmt.Fprintf(os.Stderr, "failed to resolve working directory: %v\n", err)
		return 1
	}
	storeRoot, err := tracing.StoreRootFromSnapshotPath(cfg.Tracing.FilePath)
	if err != nil {
		_, _ = fmt.Fprintf(os.Stderr, "failed to resolve session storage: %v\n", err)
		return 1
	}
	store, err := tracing.NewFileStore(storeRoot)
	if err != nil {
		_, _ = fmt.Fprintf(os.Stderr, "failed to initialize session storage: %v\n", err)
		return 1
	}

	var record tracing.SessionRecord
	var initial session.Session
	if continueSession {
		record, initial, err = store.Continue(ctx, workingDirectory)
		if err != nil {
			_, _ = fmt.Fprintf(os.Stderr, "failed to continue session: %v\n", err)
			return 1
		}
	} else {
		record, err = store.Create(ctx, workingDirectory, "")
		if err != nil {
			_, _ = fmt.Fprintf(os.Stderr, "failed to create session: %v\n", err)
			return 1
		}
	}

	loggingConfig := loggingConfigForSession(cfg.Logging, record.ID)
	logging.ConfigureOrExitForSession(loggingConfig, record.ID)

	aiProvider, err := provider.New(cfg.Providers)
	if err != nil {
		_, _ = fmt.Fprintf(os.Stderr, "failed to initialize provider: %v\n", err)
		logging.Log(ctx).Error("provider initialization failure", zap.Error(err))
		return 1
	}

	selection := aiProvider.Current()
	commands := command.DefaultRegistry()
	sessionResolver := workspaceSessionResolver{store: store, workspace: workingDirectory}
	commands.Register(command.NewSwitchCmd(sessionResolver))
	steering := input.NewSteeringInbox()
	runtimeOptions := runtime.Options{Commands: commands, Clipboard: clipboard.NewSystem(), InitialSession: &initial, SessionID: record.ID, SessionTitle: record.Title, TitleGenerator: sessiontitle.NewGenerator(aiProvider), Steering: steering}

	if tui.IsInteractive(os.Stdin, os.Stdout) {
		frontend, frontendErr := tui.New(os.Stdin, os.Stdout, os.Stderr, tui.Options{
			Provider: selection.Provider, Model: selection.Model, WorkingDirectory: workingDirectory,
			Commands: commands, Models: aiProvider.Available(), Sessions: sessionResolver, Steering: steering,
		})
		if frontendErr != nil {
			_, _ = fmt.Fprintf(os.Stderr, "failed to initialize terminal UI: %v\n", frontendErr)
			return 1
		}
		reporter := herdr.Wrap(frontend)
		defer reporter.Close()
		err = frontend.Run(ctx, func(runCtx context.Context) error {
			return runPersistentSessions(runCtx, cfg, store, workingDirectory, record, initial, aiProvider, frontend, reporter, runtimeOptions)
		})
	} else {
		frontend := input.NewTerminal(os.Stdin, os.Stdout, os.Stderr)
		reporter := herdr.Wrap(frontend)
		defer reporter.Close()
		_ = frontend.Writef("Harness (%s/%s)", selection.Provider, selection.Model)
		err = runPersistentSessions(ctx, cfg, store, workingDirectory, record, initial, aiProvider, frontend, reporter, runtimeOptions)
	}

	if err != nil && !errors.Is(err, context.Canceled) {
		_, _ = fmt.Fprintf(os.Stderr, "runtime failed: %v\n", err)
		logging.Log(ctx).Error("runtime failure", zap.Error(err))
		return 1
	}
	return 0
}

func parseStartupArgs(args []string) (bool, error) {
	set := flag.NewFlagSet("harness", flag.ContinueOnError)
	set.SetOutput(os.Stderr)
	resume := set.Bool("continue", false, "continue the active session for this working directory")
	set.BoolVar(resume, "c", false, "continue the active session for this working directory")
	if err := set.Parse(args); err != nil {
		return false, err
	}
	if set.NArg() != 0 {
		return false, fmt.Errorf("unexpected arguments: %s", strings.Join(set.Args(), " "))
	}
	return *resume, nil
}

func runPersistentSessions(
	ctx context.Context,
	cfg config.Config,
	store *tracing.FileStore,
	workingDirectory string,
	record tracing.SessionRecord,
	initial session.Session,
	aiProvider provider.Provider,
	receiver input.Receiver,
	output input.Sink,
	options runtime.Options,
) error {
	for {
		options.InitialSession = &initial
		options.SessionID = record.ID
		options.SessionTitle = record.Title
		options.TitleUpdater = workspaceTitleUpdater{store: store, workspace: workingDirectory, sessionID: record.ID}
		options.ResetFrontend = true
		sessionCtx := tracing.Init(ctx, tracing.SessionRecorder(store, record.ID))
		harness := runtime.New(aiProvider, receiver, output, options)
		action, err := harness.RunSession(sessionCtx)
		if err != nil {
			return err
		}

		switch action {
		case command.ActionNewSession:
			record, err = store.Create(ctx, workingDirectory, "")
			initial = session.Session{}
		case command.ActionSwitchSession:
			targetID := harness.SwitchSessionID()
			if targetID == "" {
				return errors.New("switch session command returned an empty session ID")
			}
			record, initial, err = store.Load(ctx, workingDirectory, targetID)
			if err == nil {
				err = store.Activate(ctx, workingDirectory, targetID)
			}
		default:
			return nil
		}
		if err != nil {
			return fmt.Errorf("transition session: %w", err)
		}

		_ = logging.Sync()
		loggingConfig := loggingConfigForSession(cfg.Logging, record.ID)
		if err := logging.ConfigureForSession(loggingConfig, record.ID); err != nil {
			return fmt.Errorf("configure session logging: %w", err)
		}
	}
}

type workspaceTitleUpdater struct {
	store     *tracing.FileStore
	workspace string
	sessionID string
}

func (u workspaceTitleUpdater) SetTitle(ctx context.Context, title string) (string, error) {
	record, err := u.store.SetTitle(ctx, u.workspace, u.sessionID, title)
	if err != nil {
		return "", err
	}
	return record.Title, nil
}

type workspaceSessionResolver struct {
	store     *tracing.FileStore
	workspace string
}

func (r workspaceSessionResolver) ResolveSession(ctx context.Context, selector string) (command.SessionSummary, error) {
	record, err := r.store.Resolve(ctx, r.workspace, selector)
	if err != nil {
		return command.SessionSummary{}, err
	}
	return command.SessionSummary{ID: record.ID, Title: record.Title, UpdatedAt: record.UpdatedAt}, nil
}

func (r workspaceSessionResolver) ListSessions(ctx context.Context) ([]command.SessionSummary, error) {
	records, err := r.store.List(ctx, r.workspace)
	if err != nil {
		return nil, err
	}
	summaries := make([]command.SessionSummary, len(records))
	for i, record := range records {
		summaries[i] = command.SessionSummary{ID: record.ID, Title: record.Title, UpdatedAt: record.UpdatedAt}
	}
	return summaries, nil
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
