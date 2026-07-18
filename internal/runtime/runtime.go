package runtime

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"

	"latentdream/harness/internal/environment"
	"latentdream/harness/internal/input"
	"latentdream/harness/internal/llm"
	"latentdream/harness/internal/orchestrator"
	"latentdream/harness/internal/provider"
	"latentdream/harness/internal/runtime/command"
	"latentdream/harness/internal/runtime/execution"
	"latentdream/harness/internal/session"
	"latentdream/harness/internal/tool"
	"latentdream/harness/internal/tool/model"

	"github.com/google/uuid"
)

const maxToolRounds = 8

type Runtime struct {
	id uuid.UUID

	Provider    provider.Provider
	Tools       []model.Tool
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
		Tools:    tool.NewDefault(),
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
	if r.Tools == nil {
		r.Tools = tool.NewDefault()
	}

	history := make([]llm.Message, 0)
	history = append(history, llm.Message{Role: llm.RoleSystem, Content: r.State.BuildSystemPrompt()})

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		// User Input ~~~~~~~~~~~~~~~~~~~~
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

		// Command (:q, :help, ...) ~~~~~~
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

		// Build context ~~~~~~~~~~~~~~~~~~
		rollbackIndex := len(history)
		history = append(history, llm.Message{Role: llm.RoleUser, Content: text})

		// inference + tool execution ~~~~~
		var response string
		history, response, err = r.inference(ctx, history)
		if err != nil {
			history = history[:rollbackIndex]
			if writeErr := r.input.Write("error: " + err.Error()); writeErr != nil {
				return r.input.WriteErrf("write error response: %w", writeErr)
			}
			continue
		}

		if err := r.input.Write(response); err != nil {
			return r.input.WriteErrf("write response: %w", err)
		}
	}
}

func (r *Runtime) inference(ctx context.Context, history []llm.Message) ([]llm.Message, string, error) {
	definitions := tool.Definitions(r.Tools)
	toolsByName := tool.ByName(r.Tools)

	for round := 0; round < maxToolRounds; round++ {
		var response provider.Response
		err := execution.WithStatus(r.input, "inference", func() error {
			var sendErr error
			response, sendErr = r.Provider.Send(ctx, llm.Request{Messages: history, Tools: definitions})
			return sendErr
		})
		if err != nil {
			return history, "", err
		}

		message := response.Message
		if len(message.ToolCalls) == 0 {
			history = append(history, message)
			return history, message.Content, nil
		}

		for index := range message.ToolCalls {
			if strings.TrimSpace(message.ToolCalls[index].ID) == "" {
				message.ToolCalls[index].ID = fmt.Sprintf("call_%d", index+1)
			}
		}
		history = append(history, message)

		for _, call := range message.ToolCalls {
			toolMessage, err := execution.ToolCall(ctx, r.input, toolsByName, call)
			if err != nil {
				return history, "", err
			}
			history = append(history, toolMessage)
		}
	}

	return history, "", errors.New("tool call limit exceeded")
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
