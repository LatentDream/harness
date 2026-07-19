package clipboard

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"runtime"
	"strings"
)

// Clipboard copies text to the host clipboard.
type Clipboard interface {
	Copy(context.Context, string) error
}

type systemClipboard struct{}

type candidate struct {
	name string
	args []string
}

// NewSystem returns a clipboard implementation backed by common platform tools.
func NewSystem() Clipboard {
	return systemClipboard{}
}

func (systemClipboard) Copy(ctx context.Context, text string) error {
	if ctx == nil {
		ctx = context.Background()
	}
	candidates := platformCandidates()
	if len(candidates) == 0 {
		return fmt.Errorf("clipboard is not supported on %s", runtime.GOOS)
	}

	missing := make([]string, 0, len(candidates))
	failures := make([]error, 0, len(candidates))
	for _, item := range candidates {
		path, err := exec.LookPath(item.name)
		if err != nil {
			missing = append(missing, item.name)
			continue
		}
		cmd := exec.CommandContext(ctx, path, item.args...)
		cmd.Stdin = strings.NewReader(text)
		err = cmd.Run()
		if err == nil {
			return nil
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
		failures = append(failures, fmt.Errorf("%s failed: %w", item.name, err))
	}

	if len(missing) > 0 {
		failures = append(failures, fmt.Errorf("clipboard commands not found: %w", errors.New(strings.Join(missing, ", "))))
	}
	return fmt.Errorf("copy to clipboard: %w", errors.Join(failures...))
}

func platformCandidates() []candidate {
	switch runtime.GOOS {
	case "darwin":
		return []candidate{{name: "pbcopy"}}
	case "linux":
		return []candidate{
			{name: "wl-copy"},
			{name: "xclip", args: []string{"-selection", "clipboard"}},
			{name: "xsel", args: []string{"--clipboard", "--input"}},
		}
	case "windows":
		return []candidate{{name: "clip"}}
	default:
		return nil
	}
}
