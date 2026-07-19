package clipboard

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"
)

func TestSystemClipboardReturnsAfterDaemonizingHelper(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("Linux clipboard helper test")
	}

	dir := t.TempDir()
	writeExecutable(t, dir, "wl-copy", "#!/bin/sh\n(/bin/sleep 2) &\nexit 0\n")
	t.Setenv("PATH", dir)

	started := time.Now()
	if err := NewSystem().Copy(context.Background(), "message"); err != nil {
		t.Fatalf("copy failed: %v", err)
	}
	if elapsed := time.Since(started); elapsed >= time.Second {
		t.Fatalf("copy waited for daemonized helper for %v", elapsed)
	}
}

func TestSystemClipboardFallsBackAfterHelperFailure(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("Linux clipboard helper test")
	}

	dir := t.TempDir()
	output := filepath.Join(dir, "clipboard.txt")
	writeExecutable(t, dir, "wl-copy", "#!/bin/sh\nexit 1\n")
	writeExecutable(t, dir, "xclip", "#!/bin/sh\n/bin/cat > \"$CLIPBOARD_TEST_OUTPUT\"\n")
	t.Setenv("PATH", dir)
	t.Setenv("CLIPBOARD_TEST_OUTPUT", output)

	const message = "fallback message\nwith another line"
	if err := NewSystem().Copy(context.Background(), message); err != nil {
		t.Fatalf("copy failed: %v", err)
	}
	copied, err := os.ReadFile(output)
	if err != nil {
		t.Fatalf("read copied text: %v", err)
	}
	if string(copied) != message {
		t.Fatalf("copied text = %q, want %q", copied, message)
	}
}

func writeExecutable(t *testing.T, dir string, name string, contents string) {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte(contents), 0o755); err != nil {
		t.Fatalf("write %s: %v", name, err)
	}
}
