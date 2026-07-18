package runtime

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/google/uuid"
	"latentdream/harness/internal/environment"
	"latentdream/harness/internal/input"
	"latentdream/harness/internal/orchestrator"
	"latentdream/harness/internal/provider"
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

	input input.Input
}

func New(aiProvider provider.Provider, userInput input.Input) *Runtime {
	return &Runtime{
		id:       uuid.New(),
		Provider: aiProvider,
		input:    userInput,
	}
}

func (r *Runtime) Run(ctx context.Context) error {
	if ctx == nil {
		ctx = context.Background()
	}
	if r.Provider == nil {
		return errors.New("runtime: provider is required")
	}
	if r.input == nil {
		return errors.New("runtime: input is required")
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
			return fmt.Errorf("receive input: %w", err)
		}

		commandText := strings.TrimSpace(text)
		if commandText == "" {
			continue
		}
		if isExitCommand(commandText) {
			return nil
		}

		history = append(history, provider.Message{Role: "user", Content: text})
		response, err := r.Provider.Send(ctx, provider.Query{Messages: history})
		if err != nil {
			history = history[:len(history)-1]
			if writeErr := r.input.Write("error: " + err.Error()); writeErr != nil {
				return fmt.Errorf("write error response: %w", writeErr)
			}
			continue
		}

		history = append(history, response.Message)
		if err := r.input.Write(response.Message.Content); err != nil {
			return fmt.Errorf("write response: %w", err)
		}
	}
}

func isExitCommand(text string) bool {
	switch strings.ToLower(text) {
	case "/exit", "/quit", ":q":
		return true
	default:
		return false
	}
}
