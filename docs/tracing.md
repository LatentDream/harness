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

- Commands and user-visible output, including ordered assistant text deltas.
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
ctx, run, err := tracing.BeginRun(ctx, meta)
if err != nil {
    return err
}
defer run.End(&runErr, sessionState)

tracing.Record(ctx, tracing.Event{
    Kind:    tracing.KindUserInput,
    Payload: input,
})

spanCtx, span, err := tracing.BeginSpan(ctx, tracing.SpanStart{
    Kind:    tracing.SpanLLMCall,
    Payload: request,
})
if err != nil {
    return err
}

response, callErr := provider.Send(spanCtx, request)
span.End(callErr, response)
return errors.Join(callErr, tracing.Checkpoint(spanCtx))
```

`Init` binds an already constructed recorder rather than constructing one from
configuration. Recorder construction may validate configuration and open
storage, so it remains a separate operation. Passing a nil recorder selects the
no-op implementation.

Scopes own status derivation and lifecycle finalization. Low-level recording
errors accumulate on the active run instead of requiring error handling after
every event. `Checkpoint` returns and clears accumulated errors at meaningful
command, turn, or run boundaries. A recorder implementing best-effort behavior
returns nil from its low-level methods; a strict recorder returns storage errors
which surface at the next checkpoint.

Call sites primarily use:

```go
func Init(context.Context, Recorder) context.Context
func BeginRun(context.Context, RunMeta) (context.Context, *RunScope, error)
func BeginSpan(context.Context, SpanStart) (context.Context, *SpanScope, error)
func Record(context.Context, Event)
func Snapshot(context.Context, SessionState)
func Checkpoint(context.Context) error
```

The returned contexts contain the active run and span and must be propagated to
provider and tool operations. The lower-level `StartRun`, `StartSpan`,
`EndSpan`, and `CloseRun` functions remain available for recorder integration
and focused tests.

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

Low-level recorder methods return errors because serialization, redaction, and
storage can fail. High-level context helpers retain those errors until
`Checkpoint` or run finalization, so ordinary business logic does not need
repetitive error joins. Best-effort recorders report failures internally and
return nil; strict recorders return failures for the next checkpoint.

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

`io.output` payloads use the structured frontend event schema. Assistant output
is recorded as `assistant.started`, one or more `assistant.delta` events, and
`assistant.completed` or `assistant.aborted`. These events carry the turn ID and
LLM round so a terminal or server can reconstruct concurrent presentation
lifecycles. Status changes and complete stdout or stderr records use the same
schema. Events are recorded only after the frontend accepts them; the aggregate
`assistant.message` event remains the canonical conversation value.

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

## Implementation Status

Runtime and execution instrumentation currently records run and turn
lifecycles, normalized provider calls, user I/O and status changes, tool calls,
session snapshots, and rollback.

Next steps:

1. Persist events, final snapshots, and outcomes to the filesystem.
2. Add repository snapshots and artifact blobs.
3. Add trace loading and session reconstruction.
4. Add deterministic replay after the persisted schema stabilizes.
