package edit

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"latentdream/harness/internal/tool/model"
)

func TestEditToolReplacesSingleMatch(t *testing.T) {
	path := writeExistingFile(t, "hello old world\n", 0o600)

	output, err := New().Execute(context.Background(), editArgs(t, path, "old", "new", nil))
	if err != nil {
		t.Fatalf("expected edit to succeed, got %v", err)
	}
	if output != "Edited file successfully: 1 replacement(s)." {
		t.Fatalf("unexpected output %q", output)
	}
	assertFileContents(t, path, "hello new world\n")
	assertFileMode(t, path, 0o600)
}

func TestEditToolAllowsEmptyReplacement(t *testing.T) {
	path := writeExistingFile(t, "hello old world\n", 0o644)

	if _, err := New().Execute(context.Background(), editArgs(t, path, "old ", "", nil)); err != nil {
		t.Fatalf("expected edit to succeed, got %v", err)
	}
	assertFileContents(t, path, "hello world\n")
}

func TestEditToolReplacesExpectedMultipleMatches(t *testing.T) {
	path := writeExistingFile(t, "one fish, one fish\n", 0o644)
	expected := 2

	if _, err := New().Execute(context.Background(), editArgs(t, path, "one", "two", &expected)); err != nil {
		t.Fatalf("expected edit to succeed, got %v", err)
	}
	assertFileContents(t, path, "two fish, two fish\n")
}

func TestEditToolRejectsUnexpectedMatchCount(t *testing.T) {
	path := writeExistingFile(t, "old old\n", 0o644)

	_, err := New().Execute(context.Background(), editArgs(t, path, "old", "new", nil))
	if err == nil || !strings.Contains(err.Error(), "oldString matched 2 time(s), expected 1") {
		t.Fatalf("expected match count error, got %v", err)
	}
	assertFileContents(t, path, "old old\n")
}

func TestEditToolRejectsNoMatch(t *testing.T) {
	path := writeExistingFile(t, "contents\n", 0o644)

	_, err := New().Execute(context.Background(), editArgs(t, path, "missing", "new", nil))
	if err == nil || !strings.Contains(err.Error(), "oldString matched 0 time(s), expected 1") {
		t.Fatalf("expected no match error, got %v", err)
	}
	assertFileContents(t, path, "contents\n")
}

func TestEditToolRejectsRelativePath(t *testing.T) {
	_, err := New().Execute(context.Background(), []byte(`{"filePath":"relative.txt","oldString":"old","newString":"new"}`))
	if err == nil || !strings.Contains(err.Error(), "filePath must be absolute") {
		t.Fatalf("expected absolute path error, got %v", err)
	}
}

func TestEditToolRejectsMissingFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "missing.txt")

	_, err := New().Execute(context.Background(), editArgs(t, path, "old", "new", nil))
	if err == nil || !strings.Contains(err.Error(), "stat file") {
		t.Fatalf("expected stat error, got %v", err)
	}
}

func TestEditToolRejectsDirectory(t *testing.T) {
	path := t.TempDir()

	_, err := New().Execute(context.Background(), editArgs(t, path, "old", "new", nil))
	if err == nil || !strings.Contains(err.Error(), "filePath must be a regular file") {
		t.Fatalf("expected regular file error, got %v", err)
	}
}

func TestEditToolRejectsEmptyOldString(t *testing.T) {
	path := writeExistingFile(t, "contents\n", 0o644)

	_, err := New().Execute(context.Background(), editArgs(t, path, "", "new", nil))
	if err == nil || !strings.Contains(err.Error(), "oldString must not be empty") {
		t.Fatalf("expected oldString empty error, got %v", err)
	}
}

func TestEditToolRejectsInvalidExpectedReplacements(t *testing.T) {
	path := writeExistingFile(t, "contents\n", 0o644)
	args := []byte(`{"filePath":` + mustJSONQuote(t, path) + `,"oldString":"old","newString":"new","expectedReplacements":0}`)

	_, err := New().Execute(context.Background(), args)
	if err == nil || !strings.Contains(err.Error(), "expectedReplacements must be a positive integer") {
		t.Fatalf("expected positive integer error, got %v", err)
	}
}

func TestEditToolRejectsUnknownArgument(t *testing.T) {
	path := writeExistingFile(t, "contents\n", 0o644)
	args := []byte(`{"filePath":` + mustJSONQuote(t, path) + `,"oldString":"old","newString":"new","extra":"nope"}`)

	_, err := New().Execute(context.Background(), args)
	if err == nil || !strings.Contains(err.Error(), `edit argument "extra" is not supported`) {
		t.Fatalf("expected unknown argument error, got %v", err)
	}
}

func TestEditToolHonorsCancellationBeforeWrite(t *testing.T) {
	path := writeExistingFile(t, "old\n", 0o644)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := New().Execute(ctx, editArgs(t, path, "old", "new", nil))
	if err == nil || !strings.Contains(err.Error(), "context canceled") {
		t.Fatalf("expected cancellation error, got %v", err)
	}
	assertFileContents(t, path, "old\n")
}

func TestEditToolDefinition(t *testing.T) {
	definition := New().Definition()
	if definition.Name != "edit" {
		t.Fatalf("expected edit name, got %q", definition.Name)
	}
	if !strings.Contains(definition.Description, "Edit an existing file") {
		t.Fatalf("expected description from edit.txt, got %q", definition.Description)
	}
	if !reflect.DeepEqual(definition.Parameters.Required, []string{"filePath", "oldString", "newString"}) {
		t.Fatalf("unexpected required fields: %#v", definition.Parameters.Required)
	}
	for _, field := range []string{"filePath", "oldString", "newString"} {
		if definition.Parameters.Properties[field].Type != "string" {
			t.Fatalf("expected %s string schema, got %#v", field, definition.Parameters.Properties[field])
		}
	}
	if definition.Parameters.Properties["expectedReplacements"].Type != "number" {
		t.Fatalf("expected expectedReplacements number schema, got %#v", definition.Parameters.Properties["expectedReplacements"])
	}
	if definition.Parameters.AdditionalProperties == nil || *definition.Parameters.AdditionalProperties {
		t.Fatalf("expected additionalProperties false, got %#v", definition.Parameters.AdditionalProperties)
	}
}

func TestEditToolStatusIncludesPath(t *testing.T) {
	path := filepath.Join(t.TempDir(), "sample.txt")

	got := New().Status(editArgs(t, path, "old", "new", nil))
	want := "Editing file " + filepath.Clean(path)
	if got != want {
		t.Fatalf("expected status %q, got %q", want, got)
	}
}

func TestEditToolPresentationExcludesContent(t *testing.T) {
	path := filepath.Join(t.TempDir(), "sample.txt")
	activity := New().(model.ActivityPresenter).Present(
		editArgs(t, path, "secret old", "secret new", nil), "Edited file successfully: 1 replacement(s).", nil,
	)

	if activity.Target != path {
		t.Fatalf("activity target = %q, want %q", activity.Target, path)
	}
	if strings.Contains(activity.Target+activity.Command+activity.Output, "secret") {
		t.Fatalf("activity leaked edited content: %#v", activity)
	}
}

func editArgs(t *testing.T, path string, oldString string, newString string, expectedReplacements *int) []byte {
	t.Helper()

	args := map[string]any{"filePath": path, "oldString": oldString, "newString": newString}
	if expectedReplacements != nil {
		args["expectedReplacements"] = *expectedReplacements
	}
	encoded, err := json.Marshal(args)
	if err != nil {
		t.Fatalf("marshal edit arguments: %v", err)
	}
	return encoded
}

func writeExistingFile(t *testing.T, contents string, mode os.FileMode) string {
	t.Helper()

	path := filepath.Join(t.TempDir(), "sample.txt")
	if err := os.WriteFile(path, []byte(contents), mode); err != nil {
		t.Fatalf("write existing file: %v", err)
	}
	return path
}

func mustJSONQuote(t *testing.T, value string) string {
	t.Helper()

	contents, err := json.Marshal(value)
	if err != nil {
		t.Fatalf("quote JSON string: %v", err)
	}
	return string(contents)
}

func assertFileContents(t *testing.T, path string, want string) {
	t.Helper()

	contents, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read file: %v", err)
	}
	if got := string(contents); got != want {
		t.Fatalf("expected file contents %q, got %q", want, got)
	}
}

func assertFileMode(t *testing.T, path string, want os.FileMode) {
	t.Helper()

	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat file: %v", err)
	}
	if got := info.Mode().Perm(); got != want {
		t.Fatalf("expected file mode %#o, got %#o", want, got)
	}
}
