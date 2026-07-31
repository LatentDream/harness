package tracing

import (
	"context"
	"sync"

	"latentdream/harness/internal/logging"

	"go.uber.org/zap"
)

// SessionRecorder persists snapshots for one stable session. A new recorder may
// be created for every run; snapshot sequencing is owned by the store.
func SessionRecorder(store *FileStore, sessionID string) Recorder {
	return &sessionRecorder{store: store, sessionID: sessionID}
}

type sessionRecorder struct {
	store     *FileStore
	sessionID string
}

type sessionRun struct {
	store     *FileStore
	sessionID string
	mu        sync.Mutex
}

func (r *sessionRecorder) StartRun(ctx context.Context, metadata RunMeta) (context.Context, Run, error) {
	ctx = normalizedContext(ctx)
	logging.Log(ctx).Debug("StartRun", zap.Any("metadata", metadata), zap.String("sessionId", r.sessionID))
	return ctx, &sessionRun{store: r.store, sessionID: r.sessionID}, nil
}

func (r *sessionRun) Event(ctx context.Context, event Event) error {
	logging.Log(ctx).Debug("Event", zap.Any("event", event))
	return nil
}

func (r *sessionRun) StartSpan(ctx context.Context, span SpanStart) (context.Context, Span, error) {
	ctx = normalizedContext(ctx)
	logging.Log(ctx).Debug("span", zap.Any("span", span))
	return ctx, logSpan{}, nil
}

func (r *sessionRun) Snapshot(ctx context.Context, state SessionState) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	logging.Log(ctx).Debug("Snapshot", zap.Any("state", state))
	return r.store.save(ctx, r.sessionID, state)
}

func (r *sessionRun) Close(ctx context.Context, outcome RunOutcome) error {
	logging.Log(ctx).Debug("Close", zap.Any("outcome", outcome))
	return nil
}
