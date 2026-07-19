package command

import (
	"context"
	"fmt"
	"strings"
)

type Clipboard interface {
	Copy(context.Context, string) error
}

type copyCommand struct {
	latest    func() (string, bool)
	clipboard Clipboard
}

func NewCopyCmd(latest func() (string, bool), clipboard Clipboard) Command {
	return &copyCommand{latest: latest, clipboard: clipboard}
}

func (cmd *copyCommand) Name() string {
	return "copy"
}

func (cmd *copyCommand) Mapping() []string {
	return []string{":copy", "/copy"}
}

func (cmd *copyCommand) Description() string {
	return "copy latest message to clipboard"
}

func (cmd *copyCommand) Execute(ctx context.Context, args []string) (Result, error) {
	if len(args) > 0 {
		return Result{Output: "usage: /copy"}, nil
	}
	if cmd.latest == nil {
		return Result{Output: "no message to copy"}, nil
	}
	text, ok := cmd.latest()
	if !ok || strings.TrimSpace(text) == "" {
		return Result{Output: "no message to copy"}, nil
	}
	if cmd.clipboard == nil {
		return Result{Output: "error: copy failed: clipboard is not configured"}, nil
	}
	if err := cmd.clipboard.Copy(ctx, text); err != nil {
		return Result{Output: fmt.Sprintf("error: copy failed: %v", err)}, nil
	}
	return Result{Output: "copied latest message to clipboard"}, nil
}
