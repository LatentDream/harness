package command

import (
	"context"
	"fmt"
	"strings"
	"time"
)

const switchUsage = "usage: /switch <session-title-or-id>"

// SessionSummary is the presentation-safe session metadata used by /switch.
type SessionSummary struct {
	ID        string
	Title     string
	UpdatedAt time.Time
}

// SessionResolver restricts session discovery to the workspace selected by its
// implementation. The command never loads session contents itself.
type SessionResolver interface {
	ResolveSession(context.Context, string) (SessionSummary, error)
	ListSessions(context.Context) ([]SessionSummary, error)
}

type switchCommand struct {
	resolver SessionResolver
}

func NewSwitchCmd(resolver SessionResolver) Command {
	return &switchCommand{resolver: resolver}
}

func (cmd *switchCommand) Name() string { return "switch" }

func (cmd *switchCommand) Mapping() []string { return []string{":switch", "/switch"} }

func (cmd *switchCommand) Description() string { return "switch session by title or ID" }

func (cmd *switchCommand) Execute(ctx context.Context, args []string) (Result, error) {
	return cmd.ExecuteRaw(ctx, strings.Join(args, " "))
}

func (cmd *switchCommand) ExecuteRaw(ctx context.Context, selector string) (Result, error) {
	if cmd.resolver == nil {
		return Result{Output: "error: session switching is not configured"}, nil
	}
	selector = strings.TrimSpace(selector)
	if selector == "" {
		sessions, err := cmd.resolver.ListSessions(ctx)
		if err != nil {
			return Result{Output: "error: " + err.Error()}, nil
		}
		if len(sessions) == 0 {
			return Result{Output: "no sessions exist in the current working directory"}, nil
		}
		var output strings.Builder
		output.WriteString("Available sessions:")
		for _, candidate := range sessions {
			title := strings.TrimSpace(candidate.Title)
			if title == "" {
				title = "Untitled"
			}
			id := candidate.ID
			if len(id) > 8 {
				id = id[:8]
			}
			fmt.Fprintf(&output, "\n  %s  %s", id, title)
		}
		output.WriteString("\n" + switchUsage)
		return Result{Output: output.String()}, nil
	}

	selected, err := cmd.resolver.ResolveSession(ctx, selector)
	if err != nil {
		return Result{Output: "error: " + err.Error()}, nil
	}
	return Result{Action: ActionSwitchSession, SessionID: selected.ID}, nil
}
