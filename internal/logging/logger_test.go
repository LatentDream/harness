package logging

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"go.uber.org/zap/zaptest/observer"
)

func TestLogInjectsFieldsAddedToContext(t *testing.T) {
	logs := useObservedLogger(t)

	ctx := With(context.Background(), "request_id", "req-123")
	ctx = With(ctx, "account_id", 42)

	Log(ctx).Info("handled request", zap.String("route", "/health"))

	entries := logs.FilterMessage("handled request").All()
	if len(entries) != 1 {
		t.Fatalf("expected 1 log entry, got %d", len(entries))
	}

	fields := entries[0].ContextMap()
	if fields["request_id"] != "req-123" {
		t.Fatalf("expected request_id to be injected, got %v", fields["request_id"])
	}
	if fields["account_id"] != int64(42) {
		t.Fatalf("expected account_id to be injected, got %v", fields["account_id"])
	}
	if fields["route"] != "/health" {
		t.Fatalf("expected explicit log field to be preserved, got %v", fields["route"])
	}
}

func TestLogInjectsRegisteredContextFields(t *testing.T) {
	logs := useObservedLogger(t)

	type requestIDKey struct{}
	RegisterContextField(requestIDKey{}, "request_id")

	ctx := context.WithValue(context.Background(), requestIDKey{}, "req-123")
	Log(ctx).Info("handled request")

	entries := logs.FilterMessage("handled request").All()
	if len(entries) != 1 {
		t.Fatalf("expected 1 log entry, got %d", len(entries))
	}

	fields := entries[0].ContextMap()
	if fields["request_id"] != "req-123" {
		t.Fatalf("expected registered request_id to be injected, got %v", fields["request_id"])
	}
}

func TestConfigureReturnsInvalidLevelError(t *testing.T) {
	useObservedLogger(t)

	if err := Configure(Config{Level: "not-a-level"}); err == nil {
		t.Fatal("expected invalid level error")
	}
}

func TestConfigureAppendsToFileOutput(t *testing.T) {
	useObservedLogger(t)

	logPath := filepath.Join(t.TempDir(), "harness.log")
	if err := os.WriteFile(logPath, []byte("existing\n"), 0o600); err != nil {
		t.Fatalf("write existing log file: %v", err)
	}

	if err := Configure(Config{
		Level:             "info",
		Encoding:          "json",
		Output:            OutputFile,
		FilePath:          logPath,
		DisableCaller:     true,
		DisableStacktrace: true,
	}); err != nil {
		t.Fatalf("configure file logger: %v", err)
	}

	Log(context.Background()).Info("appended")
	if err := Sync(); err != nil {
		t.Fatalf("sync logger: %v", err)
	}

	contents, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatalf("read log file: %v", err)
	}

	logContents := string(contents)
	if !strings.HasPrefix(logContents, "existing\n") {
		t.Fatalf("expected log file to be appended, got %q", logContents)
	}
	if !strings.Contains(logContents, `"msg":"appended"`) {
		t.Fatalf("expected appended log entry, got %q", logContents)
	}
}

func TestConfigureForSessionExpandsDefaultFilePath(t *testing.T) {
	useObservedLogger(t)

	homeDir := t.TempDir()
	t.Setenv("HOME", homeDir)
	sessionID := "session-123"

	if err := ConfigureForSession(Config{
		Level:             "debug",
		Encoding:          "json",
		Output:            OutputFile,
		FilePath:          "~/.harness/logs/{sessionId}/{sessionId}.log",
		DisableCaller:     true,
		DisableStacktrace: true,
	}, sessionID); err != nil {
		t.Fatalf("configure session logger: %v", err)
	}

	Log(context.Background()).Debug("session log")
	if err := Sync(); err != nil {
		t.Fatalf("sync logger: %v", err)
	}

	logPath := filepath.Join(homeDir, ".harness", "logs", sessionID, sessionID+".log")
	contents, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatalf("read session log file: %v", err)
	}
	if !strings.Contains(string(contents), `"msg":"session log"`) {
		t.Fatalf("expected session log entry, got %q", string(contents))
	}
}

func TestConfigureForSessionRequiresSessionForPlaceholder(t *testing.T) {
	useObservedLogger(t)

	err := ConfigureForSession(Config{Output: OutputFile, FilePath: "logs/{sessionId}.log"}, "")
	if err == nil {
		t.Fatal("expected missing session ID error")
	}
	if !strings.Contains(err.Error(), `contains "{sessionId}"`) {
		t.Fatalf("expected placeholder error, got %v", err)
	}
}

func TestConfigureRejectsFileOutputWithoutPath(t *testing.T) {
	useObservedLogger(t)

	if err := Configure(Config{Output: OutputFile}); err == nil {
		t.Fatal("expected missing filePath error")
	}
}

func TestConfigureRejectsUnsupportedOutput(t *testing.T) {
	useObservedLogger(t)

	if err := Configure(Config{Output: "database"}); err == nil {
		t.Fatal("expected unsupported output error")
	}
}

func useObservedLogger(t *testing.T) *observer.ObservedLogs {
	t.Helper()

	core, logs := observer.New(zapcore.DebugLevel)
	observedLogger := zap.New(core)

	mu.Lock()
	previousLogger := logger
	previousRegisteredFields := append([]registeredContextField(nil), registeredContextFields...)
	logger = observedLogger
	registeredContextFields = nil
	mu.Unlock()

	t.Cleanup(func() {
		mu.Lock()
		logger = previousLogger
		registeredContextFields = previousRegisteredFields
		mu.Unlock()
	})

	return logs
}
