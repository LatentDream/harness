package tracing

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"latentdream/harness/internal/session/llm"
)

func TestFileStoreContinuesActiveSessionForWorkspace(t *testing.T) {
	workspace := t.TempDir()
	store, err := NewFileStore(t.TempDir())
	if err != nil {
		t.Fatalf("create store: %v", err)
	}
	record, err := store.Create(context.Background(), workspace, "parser work")
	if err != nil {
		t.Fatalf("create session: %v", err)
	}
	state := SessionState{Conversation: []llm.Message{
		{Role: llm.RoleSystem, Content: "system"},
		{Role: llm.RoleUser, Content: "first"},
		{Role: llm.RoleAssistant, Content: "answer"},
	}}
	if err := store.save(context.Background(), record.ID, state); err != nil {
		t.Fatalf("save session: %v", err)
	}

	continuedRecord, continued, err := store.Continue(context.Background(), workspace)
	if err != nil {
		t.Fatalf("continue session: %v", err)
	}
	if continuedRecord.ID != record.ID || continuedRecord.Title != "parser work" {
		t.Fatalf("continued record = %#v, want %#v", continuedRecord, record)
	}
	if len(continued.Conversation) != 3 || continued.Conversation[2].Content != "answer" {
		t.Fatalf("continued conversation = %#v", continued.Conversation)
	}
}

func TestFileStoreKeepsMultipleSessionsAndUsesActiveSession(t *testing.T) {
	workspace := t.TempDir()
	store, _ := NewFileStore(t.TempDir())
	first, err := store.Create(context.Background(), workspace, "first")
	if err != nil {
		t.Fatal(err)
	}
	second, err := store.Create(context.Background(), workspace, "second")
	if err != nil {
		t.Fatal(err)
	}
	listed, err := store.List(context.Background(), workspace)
	if err != nil {
		t.Fatal(err)
	}
	if len(listed) != 2 {
		t.Fatalf("sessions = %#v, want two", listed)
	}
	active, _, err := store.Continue(context.Background(), workspace)
	if err != nil {
		t.Fatal(err)
	}
	if active.ID != second.ID {
		t.Fatalf("active session = %s, want %s", active.ID, second.ID)
	}
	if err := store.Activate(context.Background(), workspace, first.ID); err != nil {
		t.Fatal(err)
	}
	active, _, err = store.Continue(context.Background(), workspace)
	if err != nil || active.ID != first.ID {
		t.Fatalf("activated session = %#v, err %v", active, err)
	}
}

func TestFileStoreNeverLoadsSessionFromAnotherWorkspace(t *testing.T) {
	firstWorkspace := t.TempDir()
	secondWorkspace := t.TempDir()
	store, _ := NewFileStore(t.TempDir())
	record, err := store.Create(context.Background(), firstWorkspace, "private")
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := store.Load(context.Background(), secondWorkspace, record.ID); !errors.Is(err, ErrSessionNotFound) {
		t.Fatalf("load from another workspace returned %v", err)
	}
	if _, err := store.Resolve(context.Background(), secondWorkspace, record.ID); !errors.Is(err, ErrSessionNotFound) {
		t.Fatalf("resolve from another workspace returned %v", err)
	}
	if _, _, err := store.Continue(context.Background(), secondWorkspace); !errors.Is(err, ErrNoSession) {
		t.Fatalf("continue another workspace returned %v", err)
	}
}

func TestFileStoreResolvesTitlesAndUniqueIDPrefixes(t *testing.T) {
	workspace := t.TempDir()
	store, _ := NewFileStore(t.TempDir())
	record, err := store.Create(context.Background(), workspace, "implement parser")
	if err != nil {
		t.Fatal(err)
	}
	for _, selector := range []string{"implement parser", record.ID[:8], record.ID} {
		resolved, err := store.Resolve(context.Background(), workspace, selector)
		if err != nil || resolved.ID != record.ID {
			t.Fatalf("resolve %q = %#v, %v", selector, resolved, err)
		}
	}
	if _, err := store.Create(context.Background(), workspace, "implement parser"); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Resolve(context.Background(), workspace, "implement parser"); !errors.Is(err, ErrAmbiguousSession) {
		t.Fatalf("duplicate title returned %v", err)
	}
}

func TestFileStoreSnapshotSequenceContinuesAcrossRecorders(t *testing.T) {
	workspace := t.TempDir()
	root := t.TempDir()
	store, _ := NewFileStore(root)
	record, err := store.Create(context.Background(), workspace, "")
	if err != nil {
		t.Fatal(err)
	}
	if err := store.save(context.Background(), record.ID, SessionState{}); err != nil {
		t.Fatal(err)
	}
	if err := store.save(context.Background(), record.ID, SessionState{}); err != nil {
		t.Fatal(err)
	}
	for _, sequence := range []string{"0000000001.json", "0000000002.json"} {
		path := filepath.Join(root, "sessions", record.ID, "snapshots", sequence)
		if _, err := os.Stat(path); err != nil {
			t.Fatalf("snapshot %s: %v", sequence, err)
		}
	}
}

func TestCanonicalWorkingDirectoryResolvesSymlinks(t *testing.T) {
	real := t.TempDir()
	link := filepath.Join(t.TempDir(), "workspace")
	if err := os.Symlink(real, link); err != nil {
		t.Fatal(err)
	}
	canonical, err := CanonicalWorkingDirectory(link)
	if err != nil {
		t.Fatal(err)
	}
	expected, _ := filepath.EvalSymlinks(real)
	if canonical != expected {
		t.Fatalf("canonical path = %q, want %q", canonical, expected)
	}
}

func TestFileStoreSetTitleUpdatesSessionAndWorkspaceIndex(t *testing.T) {
	workspace := t.TempDir()
	store, _ := NewFileStore(t.TempDir())
	record, err := store.Create(context.Background(), workspace, "")
	if err != nil {
		t.Fatal(err)
	}
	updated, err := store.SetTitle(context.Background(), workspace, record.ID, "Fix Parser Commas")
	if err != nil {
		t.Fatal(err)
	}
	if updated.Title != "Fix Parser Commas" || !updated.UpdatedAt.Equal(record.UpdatedAt) {
		t.Fatalf("updated record = %#v", updated)
	}
	loaded, _, err := store.Load(context.Background(), workspace, record.ID)
	if err != nil || loaded.Title != "Fix Parser Commas" {
		t.Fatalf("loaded record = %#v, %v", loaded, err)
	}
	resolved, err := store.Resolve(context.Background(), workspace, "Fix Parser Commas")
	if err != nil || resolved.ID != record.ID {
		t.Fatalf("resolved record = %#v, %v", resolved, err)
	}
}

func TestFileStoreSetTitleRejectsAnotherWorkspace(t *testing.T) {
	store, _ := NewFileStore(t.TempDir())
	record, err := store.Create(context.Background(), t.TempDir(), "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.SetTitle(context.Background(), t.TempDir(), record.ID, "Wrong Workspace"); !errors.Is(err, ErrSessionNotFound) {
		t.Fatalf("SetTitle returned %v", err)
	}
}
