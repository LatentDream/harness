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
| `PageUp` / `PageDown`, `Ctrl+U` / `Ctrl+D` | Scroll the transcript |
| Arrow keys | Move through the editor |
| `Alt+B` / `Alt+F` | Move by one word |
| `Ctrl+W` | Delete the previous word |
| `Esc` | Clear the editor |
| `Ctrl+C` | Cancel and exit Harness |

Bracketed paste is enabled, so pasted multiline content is inserted without
being submitted.

## Modes

- **Build** exposes all configured tools and permits workspace changes.
- **Plan** adds planning instructions to the current request and exposes only
  read-only tools. The selected mode is recorded with each turn in the trace.

Mode instructions are request-local and are not added to conversation history.

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
