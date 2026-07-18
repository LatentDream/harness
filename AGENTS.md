# AGENTS.md

## Project Overview

Harness is a small, modular coding-agent runtime built around two goals:

- Support multiple LLM providers, including Codex, OpenAI-compatible APIs,
  Anthropic, and LiteLLM.
- Make executions reproducible. Record each interaction and relevant state change
  through the tracer so a previous session can be inspected, evaluated, and
  restored.

Preserve these properties when changing the system. Provider-specific behavior
must not leak into the shared session, runtime, or tool abstractions, and new
execution paths must remain traceable.

## Tech Stack

- Go 1.26.5
- Standard library where practical
- `just` for common development commands

## Commands

```bash
just build     # go build .
just run       # go run .
just dev       # go run .
just test      # go test ./...
```

Run individual Go commands directly when a narrower check is useful, for
example `go test ./internal/provider` or `go test ./internal/provider -run TestName`.

## Architecture

- `internal/config`: typed configuration and loading.
- `internal/provider`: provider selection, authentication, and API adapters.
- `internal/session`: conversation state, prompts, and shared LLM types.
- `internal/runtime`: command processing and tool execution lifecycle.
- `internal/tool`: tool contracts and implementations.
- `internal/environment`: filesystem, Git, and other host interactions.
- `internal/tracing`: execution events, outcomes, persisted state, and replay.

Keep provider wire formats inside `internal/provider`. Convert them to and from
the shared types in `internal/session/llm` at the provider boundary.

## Go Style

- Follow idiomatic Go and format changed Go files with `gofmt`.
- Prefer dependency injection through constructors over package globals. This
  keeps components composable and makes tests deterministic.
- Keep structs small and cohesive, with a narrow and explicit responsibility.
- Define interfaces at the point of consumption when an implementation must be
  replaceable or isolated in tests. Do not add an interface for every struct.
- Accept `context.Context` as the first parameter for operations involving I/O,
  cancellation, or tracing. Propagate the caller's context.
- Return errors rather than logging and continuing. Add useful context with
  `%w` when wrapping errors.
- Avoid hidden side effects in constructors; validate configuration before
  starting network or filesystem work.
- Prefer the smallest change that preserves existing package boundaries.

## JSON And Configuration

- Use typed structs with explicit JSON tags whenever the schema is known.
- Use `json.RawMessage` only when data is intentionally deferred, provider-
  specific, or preserved as an opaque payload.
- Keep external API request and response structs separate from shared domain
  types.
- Validate configuration and external input at package boundaries. Return
  errors that identify the invalid field or provider without exposing secrets.
- Preserve existing JSON field names unless a compatibility change is
  intentional.

## Tracing And Reproducibility

- Trace provider calls, tool invocations, relevant state transitions, outputs,
  errors, outcomes, and generated artifacts.
- Preserve event ordering and enough metadata to understand and replay a run.
- Keep tracing concerns behind the tracer abstraction rather than coupling core
  logic to a storage implementation.
- Never record credentials, authentication headers, or unapproved environment
  variables. Apply configured redaction before persistence.
- Treat persisted trace formats as versioned data. Make compatibility changes
  explicit.

## Testing

- Co-locate tests with their package in `*_test.go` files.
- Prefer table-driven tests for validation, parsing, and provider variations.
- Inject dependencies or use `httptest` and temporary directories instead of
  making live provider calls or modifying the developer's environment.
- Cover success, invalid input, cancellation, and dependency failure paths when
  behavior changes.
- Run `just test` and `just build` before considering a code change complete.

## Change Discipline

- Keep patches focused; do not refactor unrelated code while implementing a
  feature or fix.
- Do not add dependencies when the standard library is sufficient.
- Do not commit credentials, generated binaries, local configuration, or trace
  data containing sensitive information.
- Update documentation and tests when changing public behavior, configuration,
  provider mappings, or persisted trace schemas.
