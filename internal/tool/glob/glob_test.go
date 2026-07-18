package glob

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestGlobToolFindsAndSortsFiles(t *testing.T) {
	tool := findTool(t)
	dir := t.TempDir()
	paths := []string{
		filepath.Join(dir, "z.go"),
		filepath.Join(dir, "nested", "b.go"),
		filepath.Join(dir, "nested", "a.txt"),
	}
	if err := os.Mkdir(filepath.Join(dir, "nested"), 0o700); err != nil {
		t.Fatalf("create nested directory: %v", err)
	}
	for _, filePath := range paths {
		if err := os.WriteFile(filePath, []byte("test"), 0o600); err != nil {
			t.Fatalf("write %s: %v", filePath, err)
		}
	}

	output, err := tool.Execute(context.Background(), globArgs(t, "*.go", dir))
	if err != nil {
		t.Fatalf("expected glob to succeed, got %v", err)
	}
	want := strings.Join([]string{paths[1], paths[0]}, "\n")
	if output != want {
		t.Fatalf("expected sorted Go files %q, got %q", want, output)
	}
}

func TestGlobToolSupportsRecursiveGlob(t *testing.T) {
	tool := findTool(t)
	dir := t.TempDir()
	direct := filepath.Join(dir, "src", "direct.go")
	nested := filepath.Join(dir, "src", "nested", "sample.go")
	if err := os.MkdirAll(filepath.Dir(nested), 0o700); err != nil {
		t.Fatalf("create directories: %v", err)
	}
	for _, filePath := range []string{direct, nested} {
		if err := os.WriteFile(filePath, nil, 0o600); err != nil {
			t.Fatalf("write %s: %v", filePath, err)
		}
	}

	output, err := tool.Execute(context.Background(), globArgs(t, "src/**/*.go", dir))
	if err != nil {
		t.Fatalf("expected glob to succeed, got %v", err)
	}
	want := direct + "\n" + nested
	if output != want {
		t.Fatalf("expected recursive matches %q, got %q", want, output)
	}
}

func TestGlobToolFindFallbackSkipsHiddenPaths(t *testing.T) {
	tool := findTool(t)
	dir := t.TempDir()
	visible := filepath.Join(dir, "visible.go")
	hidden := filepath.Join(dir, ".hidden.go")
	hiddenNested := filepath.Join(dir, ".hidden", "nested.go")
	if err := os.Mkdir(filepath.Dir(hiddenNested), 0o700); err != nil {
		t.Fatalf("create hidden directory: %v", err)
	}
	for _, filePath := range []string{visible, hidden, hiddenNested} {
		if err := os.WriteFile(filePath, nil, 0o600); err != nil {
			t.Fatalf("write %s: %v", filePath, err)
		}
	}

	output, err := tool.Execute(context.Background(), globArgs(t, "*.go", dir))
	if err != nil {
		t.Fatalf("expected glob to succeed, got %v", err)
	}
	if output != visible {
		t.Fatalf("expected only visible file %q, got %q", visible, output)
	}
}

func TestGlobToolDefaultsToCurrentDirectory(t *testing.T) {
	tool := findTool(t)
	dir := t.TempDir()
	filePath := filepath.Join(dir, "sample.go")
	if err := os.WriteFile(filePath, nil, 0o600); err != nil {
		t.Fatalf("write sample file: %v", err)
	}
	t.Chdir(dir)

	output, err := tool.Execute(context.Background(), []byte(`{"pattern":"*.go"}`))
	if err != nil {
		t.Fatalf("expected glob to succeed, got %v", err)
	}
	if output != filePath {
		t.Fatalf("expected %q, got %q", filePath, output)
	}
}

func TestGlobToolResolvesSymlinkedRoot(t *testing.T) {
	tool := findTool(t)
	dir := t.TempDir()
	target := filepath.Join(dir, "target")
	if err := os.Mkdir(target, 0o700); err != nil {
		t.Fatalf("create target directory: %v", err)
	}
	filePath := filepath.Join(target, "sample.go")
	if err := os.WriteFile(filePath, nil, 0o600); err != nil {
		t.Fatalf("write sample file: %v", err)
	}
	link := filepath.Join(dir, "link")
	if err := os.Symlink(target, link); err != nil {
		t.Skipf("create directory symlink: %v", err)
	}

	output, err := tool.Execute(context.Background(), globArgs(t, "*.go", link))
	if err != nil {
		t.Fatalf("expected glob to follow the root symlink, got %v", err)
	}
	if output != filePath {
		t.Fatalf("expected canonical path %q, got %q", filePath, output)
	}
}

func TestGlobToolPreservesSignificantWhitespace(t *testing.T) {
	tool := findTool(t)
	parent := t.TempDir()
	dir := filepath.Join(parent, "root ")
	if err := os.Mkdir(dir, 0o700); err != nil {
		t.Fatalf("create directory: %v", err)
	}
	filePath := filepath.Join(dir, "sample ")
	if err := os.WriteFile(filePath, nil, 0o600); err != nil {
		t.Fatalf("write sample file: %v", err)
	}

	output, err := tool.Execute(context.Background(), globArgs(t, "* ", dir))
	if err != nil {
		t.Fatalf("expected glob to preserve whitespace, got %v", err)
	}
	if output != filePath {
		t.Fatalf("expected %q, got %q", filePath, output)
	}
}

func TestGlobToolReturnsNoFiles(t *testing.T) {
	output, err := findTool(t).Execute(context.Background(), globArgs(t, "*.go", t.TempDir()))
	if err != nil {
		t.Fatalf("expected no files to be successful, got %v", err)
	}
	if output != "No files found" {
		t.Fatalf("expected no-files message, got %q", output)
	}
}

func TestGlobToolTruncatesResults(t *testing.T) {
	dir := t.TempDir()
	for index := defaultResultLimit; index >= 0; index-- {
		filePath := filepath.Join(dir, fmt.Sprintf("%03d.go", index))
		if err := os.WriteFile(filePath, nil, 0o600); err != nil {
			t.Fatalf("write %s: %v", filePath, err)
		}
	}

	output, err := findTool(t).Execute(context.Background(), globArgs(t, "*.go", dir))
	if err != nil {
		t.Fatalf("expected glob to succeed, got %v", err)
	}
	if !strings.Contains(output, filepath.Join(dir, "099.go")) {
		t.Fatalf("expected 100th sorted file in output, got:\n%s", output)
	}
	if strings.Contains(output, filepath.Join(dir, "100.go")) {
		t.Fatalf("expected 101st sorted file to be omitted, got:\n%s", output)
	}
	if !strings.Contains(output, fmt.Sprintf("Results truncated after %d files", defaultResultLimit)) {
		t.Fatalf("expected truncation marker, got:\n%s", output)
	}
}

func TestGlobToolDoesNotTruncateAtExactLimit(t *testing.T) {
	dir := t.TempDir()
	for index := 0; index < defaultResultLimit; index++ {
		filePath := filepath.Join(dir, fmt.Sprintf("%03d.go", index))
		if err := os.WriteFile(filePath, nil, 0o600); err != nil {
			t.Fatalf("write %s: %v", filePath, err)
		}
	}

	output, err := findTool(t).Execute(context.Background(), globArgs(t, "*.go", dir))
	if err != nil {
		t.Fatalf("expected glob to succeed, got %v", err)
	}
	if strings.Contains(output, "Results truncated") {
		t.Fatalf("did not expect truncation at the exact limit, got:\n%s", output)
	}
}

func TestGlobToolRejectsInvalidArguments(t *testing.T) {
	tests := []struct {
		name string
		args string
		want string
	}{
		{name: "relative path", args: `{"pattern":"*.go","path":"relative"}`, want: "path must be absolute"},
		{name: "absolute pattern", args: `{"pattern":"/tmp/*.go"}`, want: "pattern must be relative"},
		{name: "invalid pattern", args: `{"pattern":"["}`, want: "invalid glob pattern"},
		{name: "empty segment", args: `{"pattern":"src//*.go"}`, want: "empty path segments"},
		{name: "unknown field", args: `{"pattern":"*.go","extra":true}`, want: `glob argument "extra" is not supported`},
		{name: "empty pattern", args: `{"pattern":" "}`, want: "pattern must not be empty"},
		{name: "invalid path", args: `{"pattern":"*.go","path":12}`, want: "path must be a string"},
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

func TestGlobToolRequiresDirectory(t *testing.T) {
	filePath := filepath.Join(t.TempDir(), "sample.go")
	if err := os.WriteFile(filePath, nil, 0o600); err != nil {
		t.Fatalf("write sample file: %v", err)
	}
	_, err := findTool(t).Execute(context.Background(), globArgs(t, "*.go", filePath))
	if err == nil || !strings.Contains(err.Error(), "must be a directory") {
		t.Fatalf("expected directory error, got %v", err)
	}
}

func TestGlobToolHonorsCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := New().Execute(ctx, globArgs(t, "*.go", t.TempDir()))
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("expected context cancellation, got %v", err)
	}
}

func TestGlobToolDoesNotFallbackAfterFDFailure(t *testing.T) {
	tool := globTool{lookPath: func(name string) (string, error) {
		switch name {
		case "fd":
			return requireExecutable(t, "false"), nil
		case "find":
			t.Fatal("find must not be resolved after fd was found")
		}
		return "", exec.ErrNotFound
	}}

	_, err := tool.Execute(context.Background(), globArgs(t, "*.go", t.TempDir()))
	if err == nil || !strings.Contains(err.Error(), "run fd") {
		t.Fatalf("expected fd execution failure, got %v", err)
	}
}

func TestGlobToolCommandSelection(t *testing.T) {
	t.Run("prefers fd", func(t *testing.T) {
		tool := globTool{lookPath: func(name string) (string, error) {
			if name == "fd" {
				return "/tools/fd", nil
			}
			t.Fatalf("unexpected lookup for %q", name)
			return "", nil
		}}
		executable, backend, arguments, err := tool.command("/workspace")
		if err != nil {
			t.Fatalf("select command: %v", err)
		}
		if executable != "/tools/fd" || backend != "fd" || !containsPair(arguments, "--search-path", "/workspace") {
			t.Fatalf("unexpected fd command: %q %q %#v", executable, backend, arguments)
		}
	})

	t.Run("falls back to find", func(t *testing.T) {
		tool := globTool{lookPath: func(name string) (string, error) {
			if name == "fd" {
				return "", exec.ErrNotFound
			}
			return "/tools/find", nil
		}}
		executable, backend, arguments, err := tool.command("/workspace")
		if err != nil {
			t.Fatalf("select command: %v", err)
		}
		if executable != "/tools/find" || backend != "find" || len(arguments) == 0 || arguments[0] != "/workspace" {
			t.Fatalf("unexpected find command: %q %q %#v", executable, backend, arguments)
		}
	})
}

func TestGlobToolDefinitionAndStatus(t *testing.T) {
	tool := New()
	definition := tool.Definition()
	if definition.Name != "glob" || !reflect.DeepEqual(definition.Parameters.Required, []string{"pattern"}) {
		t.Fatalf("unexpected glob definition: %#v", definition)
	}
	if got := tool.Status(globArgs(t, "**/*.go", t.TempDir())); got != "Finding files matching **/*.go" {
		t.Fatalf("unexpected status %q", got)
	}
}

func TestGlobToolUsesFDWhenInstalled(t *testing.T) {
	if _, err := exec.LookPath("fd"); err != nil {
		t.Skip("fd is not installed")
	}
	dir := t.TempDir()
	filePath := filepath.Join(dir, "sample.go")
	if err := os.WriteFile(filePath, nil, 0o600); err != nil {
		t.Fatalf("write sample file: %v", err)
	}

	output, err := New().Execute(context.Background(), globArgs(t, "*.go", dir))
	if err != nil {
		t.Fatalf("expected fd glob to succeed, got %v", err)
	}
	if output != filePath {
		t.Fatalf("expected %q, got %q", filePath, output)
	}
}

func findTool(t *testing.T) globTool {
	t.Helper()
	find := requireExecutable(t, "find")
	return globTool{lookPath: func(name string) (string, error) {
		if name == "fd" {
			return "", exec.ErrNotFound
		}
		if name == "find" {
			return find, nil
		}
		return "", exec.ErrNotFound
	}}
}

func requireExecutable(t *testing.T, name string) string {
	t.Helper()
	executable, err := exec.LookPath(name)
	if err != nil {
		t.Skipf("%s is not installed", name)
	}
	return executable
}

func globArgs(t *testing.T, pattern string, root string) []byte {
	t.Helper()
	values := map[string]string{"pattern": pattern}
	if root != "" {
		values["path"] = root
	}
	args, err := json.Marshal(values)
	if err != nil {
		t.Fatalf("marshal glob arguments: %v", err)
	}
	return args
}

func containsPair(values []string, first string, second string) bool {
	for index := 0; index+1 < len(values); index++ {
		if values[index] == first && values[index+1] == second {
			return true
		}
	}
	return false
}
