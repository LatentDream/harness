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

<p align="center">
  <a href="https://asciinema.org/a/yZRbvX6IC7FpVp3k">
    <img src="https://asciinema.org/a/yZRbvX6IC7FpVp3k.svg" alt="Harness asciicast" />
  </a>
</p>

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

If no user config file exists, Harness uses its embedded defaults. The config file is JSON and may contain any fields from the default config; values you provide override the defaults.

### Example config

> [!WARNING]
> Auth and multi-provider setup are still works in progress.
> Codex is the guaranteed working provider for now, but you must run the OpenCode auth flow first to generate the auth file that Harness reads.
> Built-in auth and provider setup helpers are coming soon.

```json
{
  "providers": [
    {
      "name": "codex",
      "type": "codex",
      "auth_file": "~/.local/share/opencode/auth.json",
      "auth_provider": "openai",
      "models": [
        { "name": "gpt-5.5" },
        { "name": "gpt-5.6-sol" },
        { "name": "gpt-5.6-luna" }
      ],
      "enabled": true
    },
    {
      "name": "openai",
      "type": "openai",
      "auth_token_env_var": "OPENAI_API_KEY",
      "models": [
        { "name": "gpt-4.1" },
        { "name": "gpt-4.1-mini" }
      ],
      "enabled": true
    },
    {
      "name": "anthropic",
      "type": "anthropic",
      "auth_token_env_var": "ANTHROPIC_API_KEY",
      "models": [
        { "name": "claude-sonnet-4-5" }
      ],
      "enabled": true
    },
    {
      "name": "litellm",
      "type": "litellm",
      "base_url": "http://localhost:4000/v1",
      "auth_token_env_var": "LITELLM_API_KEY",
      "models": [
        { "name": "gpt-4.1" }
      ],
      "enabled": false
    }
  ]
}
```

### Provider setup and authentication

Harness supports these provider types:

| Type | Description | Auth |
| --- | --- | --- |
| `codex` | ChatGPT/Codex backend provider | OAuth auth file or token env var |
| `openai` / `openai-compatible` | OpenAI Chat Completions-compatible APIs | API key env var |
| `anthropic` / `claude` | Anthropic Messages API | API key env var |
| `litellm` / `custom-litellm` | LiteLLM or custom OpenAI-compatible endpoint | API key env var plus `base_url` |

For API-key based providers, set `auth_token_env_var` in the config and export the matching environment variable:

```bash
export OPENAI_API_KEY="your-openai-api-key"
export ANTHROPIC_API_KEY="your-anthropic-api-key"
export LITELLM_API_KEY="your-litellm-api-key"
```

OpenAI-compatible providers send the token as a bearer token. Anthropic providers send the token as `x-api-key`.

For Codex, Harness currently defaults to using the OpenCode auth file:

```json
{
  "name": "codex",
  "type": "codex",
  "auth_file": "~/.local/share/opencode/auth.json",
  "auth_provider": "openai",
  "enabled": true
}
```

The Codex auth file is expected to contain an OAuth record keyed by `auth_provider`. Harness can refresh expired Codex OAuth tokens when a refresh token is present.

You can also configure Codex with a token environment variable instead:

```json
{
  "name": "codex",
  "type": "codex",
  "auth_token_env_var": "CODEX_ACCESS_TOKEN",
  "models": [{ "name": "gpt-5.3-codex" }],
  "enabled": true
}
```

```bash
export CODEX_ACCESS_TOKEN="your-token"
```

First-run config generation and built-in auth setup are still works in progress. See [todo.md](todo.md) for the roadmap.

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
| `!` | Run a local shell command and insert truncated output into the next prompt |
| `Ctrl+C` | Cancel and exit |

Plan mode exposes only read-only tools. Build mode allows the full configured tool set.
