package grep

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestGrepToolSearchesRecursively(t *testing.T) {
	requireRipgrep(t)
	dir := t.TempDir()
	subdir := filepath.Join(dir, "subdir")
	if err := os.Mkdir(subdir, 0o700); err != nil {
		t.Fatalf("create subdirectory: %v", err)
	}
	path := filepath.Join(subdir, "sample.go")
	if err := os.WriteFile(path, []byte("first\nneedle here\n"), 0o600); err != nil {
		t.Fatalf("write sample file: %v", err)
	}

	output, err := New().Execute(context.Background(), grepArgs(t, "needle", dir, ""))
	if err != nil {
		t.Fatalf("expected grep to succeed, got %v", err)
	}
	assertContains(t, output, path+":2:needle here")
}

func TestGrepToolFiltersFilesWithInclude(t *testing.T) {
	requireRipgrep(t)
	dir := t.TempDir()
	goPath := filepath.Join(dir, "sample.go")
	txtPath := filepath.Join(dir, "sample.txt")
	for _, path := range []string{goPath, txtPath} {
		if err := os.WriteFile(path, []byte("needle\n"), 0o600); err != nil {
			t.Fatalf("write %s: %v", path, err)
		}
	}

	output, err := New().Execute(context.Background(), grepArgs(t, "needle", dir, "*.go"))
	if err != nil {
		t.Fatalf("expected grep to succeed, got %v", err)
	}
	assertContains(t, output, goPath+":1:needle")
	if strings.Contains(output, txtPath) {
		t.Fatalf("expected include to exclude text file, got:\n%s", output)
	}
}

func TestGrepToolDefaultsToCurrentDirectory(t *testing.T) {
	requireRipgrep(t)
	dir := t.TempDir()
	path := filepath.Join(dir, "sample.txt")
	if err := os.WriteFile(path, []byte("needle\n"), 0o600); err != nil {
		t.Fatalf("write sample file: %v", err)
	}
	t.Chdir(dir)

	output, err := New().Execute(context.Background(), []byte(`{"pattern":"needle"}`))
	if err != nil {
		t.Fatalf("expected grep to succeed, got %v", err)
	}
	assertContains(t, output, path+":1:needle")
}

func TestGrepToolReturnsNoMatches(t *testing.T) {
	requireRipgrep(t)
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "sample.txt"), []byte("haystack\n"), 0o600); err != nil {
		t.Fatalf("write sample file: %v", err)
	}

	output, err := New().Execute(context.Background(), grepArgs(t, "needle", dir, ""))
	if err != nil {
		t.Fatalf("expected no matches to be successful, got %v", err)
	}
	if output != "No matches found" {
		t.Fatalf("expected no-match message, got %q", output)
	}
}

func TestGrepToolReportsInvalidRegex(t *testing.T) {
	requireRipgrep(t)
	_, err := New().Execute(context.Background(), grepArgs(t, "[", t.TempDir(), ""))
	if err == nil || !strings.Contains(err.Error(), "regex parse error") {
		t.Fatalf("expected regex parse error, got %v", err)
	}
}

func TestGrepToolReportsMissingExecutable(t *testing.T) {
	tool := grepTool{executable: filepath.Join(t.TempDir(), "missing-rg")}
	_, err := tool.Execute(context.Background(), grepArgs(t, "needle", t.TempDir(), ""))
	if err == nil || !strings.Contains(err.Error(), "start ripgrep") {
		t.Fatalf("expected missing executable error, got %v", err)
	}
}

func TestGrepToolRejectsInvalidArguments(t *testing.T) {
	tests := []struct {
		name string
		args string
		want string
	}{
		{name: "relative path", args: `{"pattern":"needle","path":"relative"}`, want: "path must be absolute"},
		{name: "unknown field", args: `{"pattern":"needle","extra":true}`, want: `grep argument "extra" is not supported`},
		{name: "empty pattern", args: `{"pattern":" "}`, want: "pattern must not be empty"},
		{name: "invalid include", args: `{"pattern":"needle","include":12}`, want: "include must be a string"},
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

func TestGrepToolTruncatesResults(t *testing.T) {
	requireRipgrep(t)
	dir := t.TempDir()
	path := filepath.Join(dir, "sample.txt")
	contents := strings.Repeat("needle\n", defaultResultLimit+1)
	if err := os.WriteFile(path, []byte(contents), 0o600); err != nil {
		t.Fatalf("write sample file: %v", err)
	}

	output, err := New().Execute(context.Background(), grepArgs(t, "needle", path, ""))
	if err != nil {
		t.Fatalf("expected grep to succeed, got %v", err)
	}
	assertContains(t, output, fmt.Sprintf(":%d:needle", defaultResultLimit))
	assertContains(t, output, fmt.Sprintf("Results truncated after %d matching lines", defaultResultLimit))
	if strings.Contains(output, fmt.Sprintf(":%d:needle", defaultResultLimit+1)) {
		t.Fatalf("expected final match to be omitted, got:\n%s", output)
	}
}

func TestGrepToolHonorsCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := New().Execute(ctx, grepArgs(t, "needle", t.TempDir(), ""))
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("expected context cancellation, got %v", err)
	}
}

func TestGrepToolDefinitionAndStatus(t *testing.T) {
	tool := New()
	definition := tool.Definition()
	if definition.Name != "grep" {
		t.Fatalf("expected grep definition, got %#v", definition)
	}
	if got := tool.Status(grepArgs(t, "needle", t.TempDir(), "")); got != "Searching for needle" {
		t.Fatalf("unexpected status %q", got)
	}
}

func grepArgs(t *testing.T, pattern string, path string, include string) []byte {
	t.Helper()
	values := map[string]string{"pattern": pattern}
	if path != "" {
		values["path"] = path
	}
	if include != "" {
		values["include"] = include
	}
	args, err := json.Marshal(values)
	if err != nil {
		t.Fatalf("marshal grep arguments: %v", err)
	}
	return args
}

func requireRipgrep(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("rg"); err != nil {
		t.Skip("ripgrep is not installed")
	}
}

func assertContains(t *testing.T, haystack string, needle string) {
	t.Helper()
	if !strings.Contains(haystack, needle) {
		t.Fatalf("expected output to contain %q, got:\n%s", needle, haystack)
	}
}
