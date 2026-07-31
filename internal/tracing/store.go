package tracing

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"latentdream/harness/internal/session"
	"latentdream/harness/internal/session/llm"

	"github.com/google/uuid"
)

var (
	ErrNoSession        = errors.New("no session exists for the current working directory")
	ErrSessionNotFound  = errors.New("session not found in the current working directory")
	ErrAmbiguousSession = errors.New("session selector is ambiguous")
)

// SessionRecord identifies a persistent conversation. IDs are immutable; titles
// are display metadata and need not be unique.
type SessionRecord struct {
	ID        string    `json:"id"`
	Title     string    `json:"title,omitempty"`
	CreatedAt time.Time `json:"createdAt"`
	UpdatedAt time.Time `json:"updatedAt"`
}

type workspaceManifest struct {
	Version         int             `json:"version"`
	CanonicalPath   string          `json:"canonicalPath"`
	ActiveSessionID string          `json:"activeSessionId,omitempty"`
	Sessions        []SessionRecord `json:"sessions"`
}

type sessionManifest struct {
	Version        int       `json:"version"`
	ID             string    `json:"id"`
	WorkspacePath  string    `json:"workspacePath"`
	Title          string    `json:"title,omitempty"`
	CreatedAt      time.Time `json:"createdAt"`
	UpdatedAt      time.Time `json:"updatedAt"`
	LatestSnapshot uint64    `json:"latestSnapshot"`
}

type persistedSnapshot struct {
	Version      int           `json:"version"`
	Sequence     uint64        `json:"sequence"`
	Timestamp    time.Time     `json:"timestamp"`
	Conversation []llm.Message `json:"conversation"`
}

// FileStore persists multiple sessions per canonical working directory.
type FileStore struct {
	root string
	mu   sync.Mutex
}

func NewFileStore(root string) (*FileStore, error) {
	root, err := expandSnapshotPath(strings.TrimSpace(root))
	if err != nil {
		return nil, err
	}
	if root == "" {
		return nil, errors.New("session store root is empty")
	}
	root, err = filepath.Abs(root)
	if err != nil {
		return nil, fmt.Errorf("resolve session store root: %w", err)
	}
	return &FileStore{root: filepath.Clean(root)}, nil
}

// StoreRootFromSnapshotPath derives the storage root from the existing tracing
// path setting, preserving configuration compatibility.
func StoreRootFromSnapshotPath(path string) (string, error) {
	path, err := expandSnapshotPath(path)
	if err != nil {
		return "", err
	}
	if index := strings.Index(path, sessionIDPlaceholder); index >= 0 {
		return strings.TrimRight(path[:index], string(filepath.Separator)), nil
	}
	return filepath.Dir(path), nil
}

func CanonicalWorkingDirectory(path string) (string, error) {
	absolute, err := filepath.Abs(path)
	if err != nil {
		return "", fmt.Errorf("resolve working directory: %w", err)
	}
	canonical, err := filepath.EvalSymlinks(absolute)
	if err != nil {
		return "", fmt.Errorf("canonicalize working directory %q: %w", absolute, err)
	}
	return filepath.Clean(canonical), nil
}

func (s *FileStore) Create(ctx context.Context, workspace, title string) (SessionRecord, error) {
	if err := ctxErr(ctx); err != nil {
		return SessionRecord{}, err
	}
	workspace, err := CanonicalWorkingDirectory(workspace)
	if err != nil {
		return SessionRecord{}, err
	}
	now := time.Now().UTC()
	record := SessionRecord{ID: uuid.NewString(), Title: strings.TrimSpace(title), CreatedAt: now, UpdatedAt: now}
	manifest := sessionManifest{Version: CurrentVersion, ID: record.ID, WorkspacePath: workspace, Title: record.Title, CreatedAt: now, UpdatedAt: now}

	s.mu.Lock()
	defer s.mu.Unlock()
	if err := writeJSONAtomic(s.sessionManifestPath(record.ID), manifest); err != nil {
		return SessionRecord{}, fmt.Errorf("create session: %w", err)
	}
	workspaceState, err := s.readWorkspace(workspace)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return SessionRecord{}, err
	}
	if errors.Is(err, os.ErrNotExist) {
		workspaceState = workspaceManifest{Version: CurrentVersion, CanonicalPath: workspace}
	}
	workspaceState.ActiveSessionID = record.ID
	workspaceState.Sessions = append(workspaceState.Sessions, record)
	if err := writeJSONAtomic(s.workspaceManifestPath(workspace), workspaceState); err != nil {
		return SessionRecord{}, fmt.Errorf("index session: %w", err)
	}
	return record, nil
}

func (s *FileStore) Continue(ctx context.Context, workspace string) (SessionRecord, session.Session, error) {
	if err := ctxErr(ctx); err != nil {
		return SessionRecord{}, session.Session{}, err
	}
	workspace, err := CanonicalWorkingDirectory(workspace)
	if err != nil {
		return SessionRecord{}, session.Session{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	index, err := s.readWorkspace(workspace)
	if errors.Is(err, os.ErrNotExist) || (err == nil && index.ActiveSessionID == "") {
		return SessionRecord{}, session.Session{}, ErrNoSession
	}
	if err != nil {
		return SessionRecord{}, session.Session{}, err
	}
	return s.loadLocked(workspace, index.ActiveSessionID)
}

// Resolve finds a session in one workspace by full ID, unique ID prefix, or
// exact title. It is ready for the future /switch command.
func (s *FileStore) Resolve(ctx context.Context, workspace, selector string) (SessionRecord, error) {
	if err := ctxErr(ctx); err != nil {
		return SessionRecord{}, err
	}
	workspace, err := CanonicalWorkingDirectory(workspace)
	if err != nil {
		return SessionRecord{}, err
	}
	selector = strings.TrimSpace(selector)
	s.mu.Lock()
	defer s.mu.Unlock()
	index, err := s.readWorkspace(workspace)
	if errors.Is(err, os.ErrNotExist) {
		return SessionRecord{}, ErrSessionNotFound
	}
	if err != nil {
		return SessionRecord{}, err
	}
	var matches []SessionRecord
	for _, candidate := range index.Sessions {
		if candidate.ID == selector {
			return s.validateRecord(workspace, candidate)
		}
		if strings.HasPrefix(candidate.ID, selector) || candidate.Title == selector {
			matches = append(matches, candidate)
		}
	}
	if len(matches) == 0 {
		return SessionRecord{}, ErrSessionNotFound
	}
	if len(matches) > 1 {
		return SessionRecord{}, fmt.Errorf("%w: %q", ErrAmbiguousSession, selector)
	}
	return s.validateRecord(workspace, matches[0])
}

func (s *FileStore) List(ctx context.Context, workspace string) ([]SessionRecord, error) {
	if err := ctxErr(ctx); err != nil {
		return nil, err
	}
	workspace, err := CanonicalWorkingDirectory(workspace)
	if err != nil {
		return nil, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	index, err := s.readWorkspace(workspace)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	result := append([]SessionRecord(nil), index.Sessions...)
	sort.Slice(result, func(i, j int) bool { return result[i].UpdatedAt.After(result[j].UpdatedAt) })
	return result, nil
}

func (s *FileStore) Load(ctx context.Context, workspace, id string) (SessionRecord, session.Session, error) {
	if err := ctxErr(ctx); err != nil {
		return SessionRecord{}, session.Session{}, err
	}
	workspace, err := CanonicalWorkingDirectory(workspace)
	if err != nil {
		return SessionRecord{}, session.Session{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.loadLocked(workspace, id)
}

func (s *FileStore) Activate(ctx context.Context, workspace, id string) error {
	if err := ctxErr(ctx); err != nil {
		return err
	}
	workspace, err := CanonicalWorkingDirectory(workspace)
	if err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	index, err := s.readWorkspace(workspace)
	if err != nil {
		return err
	}
	found := false
	for _, record := range index.Sessions {
		if record.ID == id {
			found = true
			break
		}
	}
	if !found {
		return ErrSessionNotFound
	}
	if _, err := s.validateRecord(workspace, SessionRecord{ID: id}); err != nil {
		return err
	}
	index.ActiveSessionID = id
	return writeJSONAtomic(s.workspaceManifestPath(workspace), index)
}

func (s *FileStore) save(ctx context.Context, sessionID string, state SessionState) error {
	if err := ctxErr(ctx); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	manifest, err := s.readSession(sessionID)
	if err != nil {
		return err
	}
	manifest.LatestSnapshot++
	manifest.UpdatedAt = time.Now().UTC()
	snapshot := persistedSnapshot{Version: CurrentVersion, Sequence: manifest.LatestSnapshot, Timestamp: manifest.UpdatedAt, Conversation: state.Conversation}
	path := filepath.Join(s.sessionDir(sessionID), "snapshots", fmt.Sprintf("%010d.json", snapshot.Sequence))
	if err := writeJSONAtomic(path, snapshot); err != nil {
		return fmt.Errorf("write session snapshot: %w", err)
	}
	if err := writeJSONAtomic(s.sessionManifestPath(sessionID), manifest); err != nil {
		return fmt.Errorf("update session manifest: %w", err)
	}
	if index, indexErr := s.readWorkspace(manifest.WorkspacePath); indexErr == nil {
		for i := range index.Sessions {
			if index.Sessions[i].ID == sessionID {
				index.Sessions[i].UpdatedAt = manifest.UpdatedAt
				index.Sessions[i].Title = manifest.Title
			}
		}
		if err := writeJSONAtomic(s.workspaceManifestPath(manifest.WorkspacePath), index); err != nil {
			return fmt.Errorf("update workspace index: %w", err)
		}
	}
	return nil
}

func (s *FileStore) loadLocked(workspace, id string) (SessionRecord, session.Session, error) {
	manifest, err := s.readSession(id)
	if errors.Is(err, os.ErrNotExist) {
		return SessionRecord{}, session.Session{}, ErrSessionNotFound
	}
	if err != nil {
		return SessionRecord{}, session.Session{}, err
	}
	if manifest.WorkspacePath != workspace {
		return SessionRecord{}, session.Session{}, ErrSessionNotFound
	}
	record := SessionRecord{ID: manifest.ID, Title: manifest.Title, CreatedAt: manifest.CreatedAt, UpdatedAt: manifest.UpdatedAt}
	if manifest.LatestSnapshot == 0 {
		return record, session.Session{}, nil
	}
	path := filepath.Join(s.sessionDir(id), "snapshots", fmt.Sprintf("%010d.json", manifest.LatestSnapshot))
	var snapshot persistedSnapshot
	if err := readJSON(path, &snapshot); err != nil {
		return SessionRecord{}, session.Session{}, fmt.Errorf("load session snapshot: %w", err)
	}
	if snapshot.Version != CurrentVersion {
		return SessionRecord{}, session.Session{}, fmt.Errorf("unsupported session snapshot version %d", snapshot.Version)
	}
	return record, cloneSession(snapshot.Conversation), nil
}

func (s *FileStore) validateRecord(workspace string, record SessionRecord) (SessionRecord, error) {
	manifest, err := s.readSession(record.ID)
	if errors.Is(err, os.ErrNotExist) || (err == nil && manifest.WorkspacePath != workspace) {
		return SessionRecord{}, ErrSessionNotFound
	}
	if err != nil {
		return SessionRecord{}, err
	}
	return SessionRecord{ID: manifest.ID, Title: manifest.Title, CreatedAt: manifest.CreatedAt, UpdatedAt: manifest.UpdatedAt}, nil
}

func (s *FileStore) readWorkspace(workspace string) (workspaceManifest, error) {
	var manifest workspaceManifest
	if err := readJSON(s.workspaceManifestPath(workspace), &manifest); err != nil {
		return workspaceManifest{}, err
	}
	if manifest.Version != CurrentVersion {
		return workspaceManifest{}, fmt.Errorf("unsupported workspace manifest version %d", manifest.Version)
	}
	if manifest.CanonicalPath != workspace {
		return workspaceManifest{}, errors.New("workspace index path does not match current working directory")
	}
	return manifest, nil
}

func (s *FileStore) readSession(id string) (sessionManifest, error) {
	if _, err := uuid.Parse(id); err != nil {
		return sessionManifest{}, ErrSessionNotFound
	}
	var manifest sessionManifest
	if err := readJSON(s.sessionManifestPath(id), &manifest); err != nil {
		return sessionManifest{}, err
	}
	if manifest.Version != CurrentVersion {
		return sessionManifest{}, fmt.Errorf("unsupported session manifest version %d", manifest.Version)
	}
	if manifest.ID != id {
		return sessionManifest{}, errors.New("session manifest ID mismatch")
	}
	return manifest, nil
}

func (s *FileStore) workspaceManifestPath(workspace string) string {
	digest := sha256.Sum256([]byte(workspace))
	return filepath.Join(s.root, "workspaces", hex.EncodeToString(digest[:]), "manifest.json")
}
func (s *FileStore) sessionDir(id string) string { return filepath.Join(s.root, "sessions", id) }
func (s *FileStore) sessionManifestPath(id string) string {
	return filepath.Join(s.sessionDir(id), "manifest.json")
}

func readJSON(path string, target any) error {
	contents, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	if err := json.Unmarshal(contents, target); err != nil {
		return fmt.Errorf("decode %q: %w", path, err)
	}
	return nil
}

func writeJSONAtomic(path string, value any) error {
	contents, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	contents = append(contents, '\n')
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	temporary, err := os.CreateTemp(filepath.Dir(path), ".tmp-*")
	if err != nil {
		return err
	}
	temporaryPath := temporary.Name()
	defer os.Remove(temporaryPath)
	if err := temporary.Chmod(0o600); err != nil {
		temporary.Close()
		return err
	}
	if _, err := temporary.Write(contents); err != nil {
		temporary.Close()
		return err
	}
	if err := temporary.Sync(); err != nil {
		temporary.Close()
		return err
	}
	if err := temporary.Close(); err != nil {
		return err
	}
	return os.Rename(temporaryPath, path)
}

func cloneSession(messages []llm.Message) session.Session {
	cloned := make([]llm.Message, len(messages))
	for i, message := range messages {
		cloned[i] = message
		cloned[i].ToolCalls = append([]llm.ToolCall(nil), message.ToolCalls...)
		for j := range cloned[i].ToolCalls {
			cloned[i].ToolCalls[j].Arguments = append(json.RawMessage(nil), message.ToolCalls[j].Arguments...)
		}
	}
	return session.Session{Conversation: cloned}
}

func ctxErr(ctx context.Context) error {
	if ctx == nil {
		return nil
	}
	return ctx.Err()
}
