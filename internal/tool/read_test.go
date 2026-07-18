package tool

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestReadToolReadsFileWithOffsetAndLimit(t *testing.T) {
	path := filepath.Join(t.TempDir(), "sample.txt")
	if err := os.WriteFile(path, []byte("first\nsecond\nthird\n"), 0o600); err != nil {
		t.Fatalf("write sample file: %v", err)
	}

	output, err := NewReadTool().Execute(context.Background(), []byte(fmt.Sprintf(`{"filePath":%q,"offset":2,"limit":1}`, path)))
	if err != nil {
		t.Fatalf("expected read to succeed, got %v", err)
	}

	assertContains(t, output, "<type>file</type>")
	assertContains(t, output, "2: second")
	assertContains(t, output, "(Showing lines 2-2 of 3 total lines. Use offset 3 to read more.)")
	if strings.Contains(output, "1: first") || strings.Contains(output, "3: third") {
		t.Fatalf("expected limited output, got:\n%s", output)
	}
}

func TestReadToolReadsDirectory(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "file.txt"), []byte("contents"), 0o600); err != nil {
		t.Fatalf("write directory file: %v", err)
	}
	if err := os.Mkdir(filepath.Join(dir, "subdir"), 0o700); err != nil {
		t.Fatalf("create subdir: %v", err)
	}

	output, err := NewReadTool().Execute(context.Background(), []byte(fmt.Sprintf(`{"filePath":%q}`, dir)))
	if err != nil {
		t.Fatalf("expected read to succeed, got %v", err)
	}

	assertContains(t, output, "<type>directory</type>")
	assertContains(t, output, "file.txt\n")
	assertContains(t, output, "subdir/\n")
}

func TestReadToolRejectsRelativePath(t *testing.T) {
	_, err := NewReadTool().Execute(context.Background(), []byte(`{"filePath":"relative.txt"}`))
	if err == nil || !strings.Contains(err.Error(), "filePath must be absolute") {
		t.Fatalf("expected absolute path error, got %v", err)
	}
}

func assertContains(t *testing.T, haystack string, needle string) {
	t.Helper()
	if !strings.Contains(haystack, needle) {
		t.Fatalf("expected output to contain %q, got:\n%s", needle, haystack)
	}
}
