# Terminal UI

Harness uses a full-screen terminal interface when stdin and stdout are TTYs on
Linux or macOS. Redirected input or output continues to use the line-oriented
interface.

The interactive interface requires `fzf` on `PATH`. Harness checks this before
changing terminal mode and exits with an actionable error when it is missing.

## Keys

| Key | Action |
| --- | --- |
| `Enter` | Submit the current prompt |
| `Ctrl+N` | Insert a newline |
| `Tab` | Switch between Build and Plan mode |
| `@` | Open the repository file picker and insert `@relative/path` |
| `/` or `:` | Open the command picker at the start of an empty prompt |
| `!` | Enter local shell mode at the start of an empty prompt; `Enter` runs the command and inserts truncated output into the next message |
| `PageUp` / `PageDown`, `Ctrl+U` / `Ctrl+D` | Scroll the transcript |
| Arrow keys | Move through the editor |
| `Alt+B` / `Alt+F` | Move by one word |
| `Ctrl+W` | Delete the previous word |
| `Esc` | Clear the editor, cancel shell mode, or cancel a running local command |
| `Ctrl+C` | Cancel and exit Harness |

Bracketed paste is enabled, so pasted multiline content is inserted without
being submitted.

## Modes

- **Build** exposes all configured tools and permits workspace changes.
- **Plan** adds planning instructions to the current request and exposes only
  read-only tools. The selected mode is recorded with each turn in the trace.

Mode instructions are request-local and are not added to conversation history.

## Local shell mode

Type `!` at the start of an empty prompt to switch to `[Shell] $`. The text you enter runs locally in the configured working directory using `/bin/bash -lc`. Harness captures stdout, stderr, exit code, and timeout state, adds a shell block to the transcript, and pre-fills the next normal prompt with the captured output. Long command output is truncated before insertion so it can be sent to the model as context.

## Commands

- `:new` or `/new` closes the current persisted session and starts a fresh one.
  Conversation history and the TUI transcript are cleared, while the selected
  provider and model remain active.
- `:help` or `/help` lists all available commands.

## Pickers

The file picker uses tracked and unignored files from `git ls-files`. Outside a
Git repository it walks the working directory and skips `.git`. File names are
passed to `fzf` with NUL delimiters so whitespace and newline characters are
preserved.

Harness restores cooked mode and leaves the alternate screen before starting
`fzf`, then restores the full-screen interface and redraws the transcript after
selection or cancellation.

## Color

The interface uses ANSI colors for roles, status, errors, and the active mode.
Set `NO_COLOR` or use `TERM=dumb` to disable decorative color while retaining
the cursor and screen-control sequences required by the interface.
