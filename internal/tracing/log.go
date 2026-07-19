package tracing

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"

	"latentdream/harness/internal/logging"

	"go.uber.org/zap"
)

type (
	logRecorder struct {
		filePath  string
		sessionID string
	}
	logRun struct {
		filePath        string
		currentSnapshot int
		mu              sync.Mutex
	}
	logSpan struct{}
)

const (
	sessionIDPlaceholder  = "{sessionId}"
	snapshotIDPlaceholder = "{snapshot_id}"
)

// LogRecorder returns a recorder that logs trace operations and persists snapshots.
func LogRecorder(filePath, sessionID string) Recorder {
	return logRecorder{filePath: filePath, sessionID: sessionID}
}

func (r logRecorder) StartRun(ctx context.Context, metadata RunMeta) (context.Context, Run, error) {
	ctx = normalizedContext(ctx)
	logging.Log(ctx).Debug("StartRun", zap.Any("metadata", metadata))
	return ctx, &logRun{filePath: strings.ReplaceAll(r.filePath, sessionIDPlaceholder, r.sessionID)}, nil
}

func (logRun) Event(ctx context.Context, event Event) error {
	logging.Log(ctx).Debug("Event", zap.Any("event", event))
	return nil
}

func (logRun) StartSpan(ctx context.Context, span SpanStart) (context.Context, Span, error) {
	ctx = normalizedContext(ctx)
	logging.Log(ctx).Debug("span", zap.Any("span", span))
	return ctx, logSpan{}, nil
}

func (l *logRun) Snapshot(ctx context.Context, state SessionState) error {
	ctx = normalizedContext(ctx)
	logging.Log(ctx).Debug("Snapshot", zap.Any("state", state))

	l.mu.Lock()
	defer l.mu.Unlock()
	l.currentSnapshot++
	return saveSnapshot(ctx, l.filePath, l.currentSnapshot, state)
}

func saveSnapshot(ctx context.Context, pathTemplate string, snapshotID int, state SessionState) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if strings.TrimSpace(pathTemplate) == "" {
		return fmt.Errorf("save snapshot %d: filePath is empty", snapshotID)
	}

	path := strings.ReplaceAll(pathTemplate, snapshotIDPlaceholder, strconv.Itoa(snapshotID))
	path, err := expandSnapshotPath(path)
	if err != nil {
		return fmt.Errorf("save snapshot %d: %w", snapshotID, err)
	}
	contents, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal snapshot %d: %w", snapshotID, err)
	}
	contents = append(contents, '\n')

	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return fmt.Errorf("create snapshot directory %q: %w", filepath.Dir(path), err)
	}
	if err := os.WriteFile(path, contents, 0o600); err != nil {
		return fmt.Errorf("write snapshot %q: %w", path, err)
	}
	return nil
}

func expandSnapshotPath(path string) (string, error) {
	if path != "~" && !strings.HasPrefix(path, "~/") {
		return path, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("resolve home directory: %w", err)
	}
	if path == "~" {
		return home, nil
	}
	return filepath.Join(home, path[2:]), nil
}

func (logRun) Close(ctx context.Context, outcome RunOutcome) error {
	ctx = normalizedContext(ctx)
	logging.Log(ctx).Debug("Close", zap.Any("outcome", outcome))
	return nil
}

func (logSpan) End(ctx context.Context, end SpanEnd) error {
	ctx = normalizedContext(ctx)
	logging.Log(ctx).Debug("End", zap.Any("end", end))
	return nil
}
