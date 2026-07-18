package runtime

import (
	"context"
	"errors"
	"io"
	"strings"

	"github.com/google/uuid"
	"latentdream/harness/internal/environment"
	"latentdream/harness/internal/input"
	"latentdream/harness/internal/orchestrator"
	"latentdream/harness/internal/provider"
	"latentdream/harness/internal/runtime/command"
	"latentdream/harness/internal/session"
	"latentdream/harness/internal/tool"
)

type Runtime struct {
	id uuid.UUID

	Provider    provider.Provider
	Tools       []tool.Tool
	Controller  orchestrator.Orchestrator
	Environment environment.Environment
	State       session.Session

	input    input.IO
	commands *command.Registry
}

func New(aiProvider provider.Provider, userInput input.IO) *Runtime {
	return &Runtime{
		id:       uuid.New(),
		Provider: aiProvider,
		input:    userInput,
		commands: command.DefaultRegistry(),
	}
}

func (r *Runtime) Run(ctx context.Context) error {
	if err := r.validateInput(ctx); err != nil {
		return err
	}

	if r.commands == nil {
		r.commands = command.DefaultRegistry()
	}

	history := make([]provider.Message, 0)
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		text, err := r.input.Receive()
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			return r.input.WriteErrf("receive input: %w", err)
		}

		commandText := strings.TrimSpace(text)
		if commandText == "" {
			continue
		}

		commandResult, handled, err := r.commands.Execute(commandText)
		if handled {
			if err != nil {
				return r.input.WriteErrf("execute command: %w", err)
			}
			if commandResult.Output != "" {
				if writeErr := r.input.Write(commandResult.Output); writeErr != nil {
					return r.input.WriteErrf("write command output: %w", writeErr)
				}
			}
			if commandResult.Action == command.ActionExit {
				return nil
			}
			continue
		}

		history = append(history, provider.Message{Role: "user", Content: text})
		response, err := r.Provider.Send(ctx, provider.Query{Messages: history})
		if err != nil {
			history = history[:len(history)-1]
			if writeErr := r.input.Write("error: " + err.Error()); writeErr != nil {
				return r.input.WriteErrf("write error response: %w", writeErr)
			}
			continue
		}

		history = append(history, response.Message)
		if err := r.input.Write(response.Message.Content); err != nil {
			return r.input.WriteErrf("write response: %w", err)
		}
	}
}

func (r *Runtime) validateInput(ctx context.Context) error {
	if ctx == nil {
		ctx = context.Background()
	}
	if r.Provider == nil {
		return errors.New("runtime: provider is required")
	}
	if r.input == nil {
		return errors.New("runtime: input is required")
	}
	return nil
}
