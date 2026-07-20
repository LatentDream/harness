<p align="center">
   <h1 align="center"><b>Coding Harness</b></h1>
   <p align="center">
      <br />
      <strong>By LatentDream</strong>
   </p>
</p>

# Harness

Harness is a small, modular coding-agent runtime for working with LLMs inside a developer environment.

Harness is a work in progress. See [todo.md](todo.md) for the roadmap.

[![asciicast](https://asciinema.org/a/yZRbvX6IC7FpVp3k.svg)](https://asciinema.org/a/yZRbvX6IC7FpVp3k)

## Features

- Support multiple LLM providers, including Codex, OpenAI-compatible APIs, Anthropic, and LiteLLM;
- Interactive terminal UI when running in a TTY.
- Line-oriented mode for redirected input/output.
- Build and Plan modes.
- Tool calling support for reading, writing, searching, globbing, and shell commands.
- Provider/model switching with `:model` or `/model`.
- New session support with `:new` or `/new`.
- Copy latest assistant response with `:copy` or `/copy`.
- Structured tracing and session logging.

## Requirements

- `fzf`
- `rg`
- `fd`

## Quick Start

Using [just](https://github.com/casey/just):

```bash
just build
just run
just test
```

## Configuration

Harness loads configuration from the default user config path:

```text
~/.harness.json
```

You can override the config path with:

```bash
HARNESS_CONFIG_PATH=/path/to/config.json
```

Provider authentication is configured through provider-specific fields such as auth files or environment variables.

## Terminal UI

When stdin and stdout are interactive TTYs, Harness starts a full-screen terminal interface.

Useful keys:

| Key | Action |
| --- | --- |
| `Enter` | Submit prompt |
| `Ctrl+N` | Insert newline |
| `Tab` | Switch Build/Plan mode |
| `@` | Pick a repository file |
| `/` or `:` | Open command picker |
| `Ctrl+C` | Cancel and exit |

Plan mode exposes only read-only tools. Build mode allows the full configured tool set.
