# Tracing

Tracing is the detailed record of what happened during one interactive harness
run. A run contains zero or more user turns; each turn may contain multiple LLM
calls and tool invocations.

```text
Run: one Runtime.Run invocation
  Turn: one user prompt and its outcome
    LLM call: one provider request
      Tool call: one tool invocation
```

## Goals

- Debug and understand executions.
- Inspect or restore conversation state at a known point.
- Evaluate and compare runs and individual turns.
- Support audit and observability use cases.
- Preserve enough ordering and metadata for future deterministic replay.

Loading, restoration, and replay are separate operations. Loading a trace must
not mutate a workspace or make provider calls. Restoring workspace files must be
an explicit operation with path validation. Replay should use recorded provider
and tool results rather than silently performing live work.

## Recorded Data

### Inputs

- User prompts.
- A redacted configuration snapshot.
- Explicitly allowlisted environment metadata.
- Repository identity, commit, and working tree state.

### Execution

- Commands and user-visible output.
- Normalized LLM requests, responses, failures, timing, and available usage.
- Tool arguments, results, execution failures, and timing.
- Explicit agent decisions or provider-supplied reasoning summaries.
- Conversation rollback and snapshot events.

Hidden chain-of-thought and arbitrary Go function calls are not recorded.
Instrumentation targets meaningful application boundaries.

### Artifacts

- Files created, modified, or deleted.
- Before and after hashes.
- Generated patches or diffs when enabled.
- Logs and test results when explicitly attached to the trace.

Large values should be stored as content-addressed blobs and referenced by
events rather than embedded repeatedly.

### Outcomes

Each turn records success, failure, or cancellation, an optional structured
error, and its final answer. The run records why the interactive session ended:
EOF, an exit command, cancellation, or a fatal error.

## API

The application constructs a recorder once and adds it to the root context.
There is no process-global recorder. Independent contexts can therefore use
different recorders in tests, concurrent runtimes, and replay.

```go
ctx = tracing.Init(ctx, recorder)
ctx, err = tracing.StartRun(ctx, meta)
if err != nil {
    return err
}

if err := tracing.Record(ctx, tracing.Event{
    Kind:    tracing.KindUserInput,
    Payload: input,
}); err != nil {
    return err
}

spanCtx, err := tracing.StartSpan(ctx, tracing.SpanStart{
    Kind:    tracing.SpanLLMCall,
    Payload: request,
})
if err != nil {
    return err
}

response, callErr := provider.Send(spanCtx, request)
traceErr := tracing.EndSpan(spanCtx, tracing.SpanEnd{
    Status:  statusFromError(callErr),
    Error:   traceError(callErr),
    Payload: response,
})
return errors.Join(callErr, traceErr)
```

`Init` binds an already constructed recorder rather than constructing one from
configuration. Recorder construction may validate configuration and open
storage, so it remains a separate operation. Passing a nil recorder selects the
no-op implementation.

Call sites use context-based package functions:

```go
func Init(context.Context, Recorder) context.Context
func RecorderFromContext(context.Context) Recorder
func StartRun(context.Context, RunMeta) (context.Context, error)
func Record(context.Context, Event) error
func StartSpan(context.Context, SpanStart) (context.Context, error)
func EndSpan(context.Context, SpanEnd) error
func Snapshot(context.Context, SessionState) error
func CloseRun(context.Context, RunOutcome) error
```

The returned context from `StartRun` contains the active run. The returned
context from `StartSpan` contains the active span and must be passed to
`EndSpan`. Provider and tool operations propagate these contexts normally.

Recorder implementations use the lower-level lifecycle interfaces:

```go
type Recorder interface {
    StartRun(context.Context, RunMeta) (context.Context, Run, error)
}

type Run interface {
    Event(context.Context, Event) error
    StartSpan(context.Context, SpanStart) (context.Context, Span, error)
    Snapshot(context.Context, SessionState) error
    Close(context.Context, RunOutcome) error
}

type Span interface {
    End(context.Context, SpanEnd) error
}

type Reader interface {
    Open(context.Context, string) (*Trace, error)
}
```

Recording methods return errors because serialization, redaction, and storage
can fail. The configured failure policy determines whether these errors abort
the harness or are retained as best-effort tracing failures.

All context operations fall back to usable no-op recorder, run, and span
implementations, so instrumentation does not require nil checks or initialization
guards.

## Event Model

Events use an append-only, versioned envelope. The recorder assigns event IDs,
trace IDs, sequence numbers, timestamps, span IDs, and parent span IDs. Callers
provide only the event kind, optional turn ID, and typed payload.

Initial event kinds are:

- `run.started` and `run.ended`
- `turn.started` and `turn.ended`
- `user.input`
- `command.started` and `command.ended`
- `llm.request`, `llm.response`, and `llm.error`
- `tool.started` and `tool.ended`
- `assistant.message`
- `session.rollback` and `session.snapshot`
- `artifact.observed`
- `io.output` and `error`

Paired start and end events preserve incomplete operations when a process exits
unexpectedly. Sequence numbers describe commit order in the trace, while span
and parent IDs describe causality.

Payloads have typed Go structures at instrumentation boundaries. The persisted
envelope uses `json.RawMessage` so unknown event kinds remain readable across
versions.

## Persistence

A filesystem implementation should use an append-only layout:

```text
<trace-directory>/<trace-id>/
  manifest.json
  events.jsonl
  snapshots/
  blobs/
  outcome.json
```

- Allocate sequence numbers when events are committed to storage.
- Serialize concurrent writes through one writer.
- Treat a truncated final JSONL line as an interrupted trace.
- Write manifests and outcomes atomically.
- Use `0700` directory and `0600` file permissions by default.
- Store large data in SHA-256-addressed blobs.
- Mark whether a trace was finalized cleanly.

Snapshots complement events; they do not replace them. Conversation mutations
and external side effects remain visible even when runtime conversation state is
rolled back.

## Security

Tracing is deny-by-default for sensitive data:

- Never record all environment variables. Use an allowlist.
- Never record authorization headers, tokens, credential files, or cookies.
- Redact structured fields before serialization.
- Apply redaction to messages, tool data, raw responses, errors, logs, config,
  environment metadata, file contents, and diffs.
- Apply payload limits and include truncation metadata.
- Record raw provider data only when explicitly enabled.
- Validate artifact paths before any restore operation.

Regular-expression redaction is an additional layer, not the primary defense.
Known sensitive field names must always be removed or replaced.

## Implementation Order

1. Record run and turn lifecycles in the runtime.
2. Record normalized provider requests and responses at the provider boundary.
3. Record tool status, output, and actual execution errors in `execution.ToolCall`.
4. Persist events, final snapshots, and outcomes to the filesystem.
5. Add repository snapshots and artifact blobs.
6. Add trace loading and session reconstruction.
7. Add deterministic replay after the persisted schema stabilizes.
