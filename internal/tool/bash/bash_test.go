package bash

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"latentdream/harness/internal/tool/model"
)

func TestBashToolRunsCommand(t *testing.T) {
	output, err := New().Execute(context.Background(), bashArgs(t, "printf hello", "", 0))
	if err != nil {
		t.Fatalf("expected bash to succeed, got %v", err)
	}

	assertContains(t, output, "<exitCode>0</exitCode>")
	assertContains(t, output, "<stdout>\nhello\n</stdout>")
}

func TestBashToolCapturesStderr(t *testing.T) {
	output, err := New().Execute(context.Background(), bashArgs(t, "printf err >&2", "", 0))
	if err != nil {
		t.Fatalf("expected bash to succeed, got %v", err)
	}

	assertContains(t, output, "<stderr>\nerr\n</stderr>")
}

func TestBashToolReturnsNonZeroExitCode(t *testing.T) {
	output, err := New().Execute(context.Background(), bashArgs(t, "exit 7", "", 0))
	if err != nil {
		t.Fatalf("expected non-zero command exit to be tool output, got %v", err)
	}

	assertContains(t, output, "<exitCode>7</exitCode>")
}

func TestBashToolUsesWorkingDirectory(t *testing.T) {
	dir := t.TempDir()
	output, err := New().Execute(context.Background(), bashArgs(t, "pwd", dir, 0))
	if err != nil {
		t.Fatalf("expected bash to succeed, got %v", err)
	}

	assertContains(t, output, "<workingDirectory>"+filepath.Clean(dir)+"</workingDirectory>")
	assertContains(t, output, filepath.Clean(dir))
}

func TestBashToolRejectsInvalidArguments(t *testing.T) {
	tests := []struct {
		name string
		args string
		want string
	}{
		{name: "missing command", args: `{}`, want: "command is required"},
		{name: "empty command", args: `{"command":" "}`, want: "command must not be empty"},
		{name: "unknown field", args: `{"command":"true","extra":true}`, want: `bash argument "extra" is not supported`},
		{name: "relative working directory", args: `{"command":"pwd","workingDirectory":"relative"}`, want: "workingDirectory must be absolute"},
		{name: "invalid timeout", args: `{"command":"true","timeoutSeconds":0}`, want: "timeoutSeconds must be a positive integer"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := New().Execute(context.Background(), []byte(test.args))
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("expected error containing %q, got %v", test.want, err)
			}
		})
	}
}

func TestBashToolRejectsFileWorkingDirectory(t *testing.T) {
	file := filepath.Join(t.TempDir(), "sample.txt")
	if err := os.WriteFile(file, []byte("hello"), 0o600); err != nil {
		t.Fatalf("write sample file: %v", err)
	}

	_, err := New().Execute(context.Background(), bashArgs(t, "pwd", file, 0))
	if err == nil || !strings.Contains(err.Error(), "workingDirectory must be a directory") {
		t.Fatalf("expected directory error, got %v", err)
	}
}

func TestBashToolTimesOut(t *testing.T) {
	tool := bashTool{executable: "/bin/bash", defaultTimeout: time.Second, maxTimeout: time.Second}
	output, err := tool.Execute(context.Background(), bashArgs(t, "sleep 2", "", 1))
	if err != nil {
		t.Fatalf("expected timeout to be tool output, got %v", err)
	}

	assertContains(t, output, "<exitCode>-1</exitCode>")
	assertContains(t, output, "<timedOut>true</timedOut>")
}

func TestBashToolHonorsContextCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := New().Execute(ctx, bashArgs(t, "true", "", 0))
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("expected context canceled, got %v", err)
	}
}

func TestBashToolDefinition(t *testing.T) {
	definition := New().Definition()
	if definition.Name != "bash" {
		t.Fatalf("expected bash name, got %q", definition.Name)
	}
	if !strings.Contains(definition.Description, "Execute a bash command") {
		t.Fatalf("expected description from bash.txt, got %q", definition.Description)
	}
	if !reflect.DeepEqual(definition.Parameters.Required, []string{"command"}) {
		t.Fatalf("unexpected required fields: %#v", definition.Parameters.Required)
	}
	if definition.Parameters.Properties["command"].Type != "string" {
		t.Fatalf("expected command string schema, got %#v", definition.Parameters.Properties["command"])
	}
	if definition.Parameters.Properties["timeoutSeconds"].Type != "number" {
		t.Fatalf("expected timeoutSeconds number schema, got %#v", definition.Parameters.Properties["timeoutSeconds"])
	}
	if definition.Parameters.AdditionalProperties == nil || *definition.Parameters.AdditionalProperties {
		t.Fatalf("expected additionalProperties false, got %#v", definition.Parameters.AdditionalProperties)
	}
}

func TestBashToolStatusIncludesCommand(t *testing.T) {
	got := New().Status(bashArgs(t, "echo hello", "", 0))
	want := "Running bash command: echo hello"
	if got != want {
		t.Fatalf("expected status %q, got %q", want, got)
	}
}

func TestBashActivitySummarizesAndCropsOutput(t *testing.T) {
	longOutput := strings.Repeat("line\n", maxActivityLines+3)
	result := formatOutput(bashParams{command: "test", workingDirectory: t.TempDir()}, 2, false, longOutput, "warning")
	activity := New().(model.ActivityPresenter).Present(json.RawMessage(`{"command":"test"}`), result, nil)

	if activity.Command != "test" {
		t.Fatalf("activity command = %q", activity.Command)
	}
	if !strings.Contains(activity.Output, "exit code 2") || !strings.Contains(activity.Output, "... output cropped ...") {
		t.Fatalf("unexpected activity output: %q", activity.Output)
	}
	if got := len(strings.Split(activity.Output, "\n")); got > maxActivityLines+1 {
		t.Fatalf("activity output has %d lines", got)
	}
}

func TestCropActivityPreservesValidUTF8(t *testing.T) {
	value := strings.Repeat("界", maxActivityBytes)
	if got := cropActivity(value); !utf8.ValidString(got) {
		t.Fatalf("cropped activity is invalid UTF-8: %q", got)
	}
}

func bashArgs(t *testing.T, command string, workingDirectory string, timeoutSeconds int) []byte {
	t.Helper()

	args := map[string]any{"command": command}
	if workingDirectory != "" {
		args["workingDirectory"] = workingDirectory
	}
	if timeoutSeconds > 0 {
		args["timeoutSeconds"] = timeoutSeconds
	}
	contents, err := json.Marshal(args)
	if err != nil {
		t.Fatalf("marshal bash arguments: %v", err)
	}
	return contents
}

func assertContains(t *testing.T, value string, want string) {
	t.Helper()

	if !strings.Contains(value, want) {
		t.Fatalf("expected output to contain %q, got:\n%s", want, value)
	}
}
