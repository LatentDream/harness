package tool

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestWriteToolCreatesFileAndParents(t *testing.T) {
	path := filepath.Join(t.TempDir(), "nested", "sample.txt")
	output, err := NewWriteTool().Execute(context.Background(), writeArgs(t, path, "hello\nworld\n"))
	if err != nil {
		t.Fatalf("expected write to succeed, got %v", err)
	}

	if output != "Wrote file successfully." {
		t.Fatalf("unexpected output %q", output)
	}
	assertFileContents(t, path, "hello\nworld\n")
}

func TestWriteToolOverwritesExistingFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "sample.txt")
	if err := os.WriteFile(path, []byte("old contents"), 0o600); err != nil {
		t.Fatalf("write existing file: %v", err)
	}

	if _, err := NewWriteTool().Execute(context.Background(), writeArgs(t, path, "new contents")); err != nil {
		t.Fatalf("expected write to succeed, got %v", err)
	}

	assertFileContents(t, path, "new contents")
}

func TestWriteToolPreservesEmptyContent(t *testing.T) {
	path := filepath.Join(t.TempDir(), "empty.txt")
	if _, err := NewWriteTool().Execute(context.Background(), writeArgs(t, path, "")); err != nil {
		t.Fatalf("expected write to succeed, got %v", err)
	}

	assertFileContents(t, path, "")
}

func TestWriteToolRejectsNullContent(t *testing.T) {
	path := filepath.Join(t.TempDir(), "sample.txt")
	args := []byte(`{"filePath":` + mustJSONQuote(t, path) + `,"content":null}`)

	_, err := NewWriteTool().Execute(context.Background(), args)
	if err == nil || !strings.Contains(err.Error(), "content must be a string") {
		t.Fatalf("expected content string error, got %v", err)
	}
}

func TestWriteToolRejectsRelativePath(t *testing.T) {
	_, err := NewWriteTool().Execute(context.Background(), []byte(`{"filePath":"relative.txt","content":"hello"}`))
	if err == nil || !strings.Contains(err.Error(), "filePath must be absolute") {
		t.Fatalf("expected absolute path error, got %v", err)
	}
}

func TestWriteToolDefinition(t *testing.T) {
	definition := NewWriteTool().Definition()
	if definition.Name != "write" {
		t.Fatalf("expected write name, got %q", definition.Name)
	}
	if !strings.Contains(definition.Description, "Write a file") {
		t.Fatalf("expected description from write.txt, got %q", definition.Description)
	}
	if !reflect.DeepEqual(definition.Parameters.Required, []string{"filePath", "content"}) {
		t.Fatalf("unexpected required fields: %#v", definition.Parameters.Required)
	}
	if definition.Parameters.Properties["filePath"].Type != "string" {
		t.Fatalf("expected filePath string schema, got %#v", definition.Parameters.Properties["filePath"])
	}
	if definition.Parameters.Properties["content"].Type != "string" {
		t.Fatalf("expected content string schema, got %#v", definition.Parameters.Properties["content"])
	}
	if definition.Parameters.AdditionalProperties == nil || *definition.Parameters.AdditionalProperties {
		t.Fatalf("expected additionalProperties false, got %#v", definition.Parameters.AdditionalProperties)
	}
}

func writeArgs(t *testing.T, path string, content string) []byte {
	t.Helper()

	args, err := json.Marshal(map[string]string{"filePath": path, "content": content})
	if err != nil {
		t.Fatalf("marshal write arguments: %v", err)
	}
	return args
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
		t.Fatalf("read written file: %v", err)
	}
	if got := string(contents); got != want {
		t.Fatalf("expected file contents %q, got %q", want, got)
	}
}
