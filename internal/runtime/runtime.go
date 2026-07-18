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
	"latentdream/harness/internal/session"
	"latentdream/harness/internal/tool"

	"github.com/google/uuid"
)

const maxToolRounds = 8

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

		rollbackIndex := len(history)
		history = append(history, llm.Message{Role: llm.RoleUser, Content: text})

		var response string
		history, response, err = r.complete(ctx, history)
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

func (r *Runtime) complete(ctx context.Context, history []llm.Message) ([]llm.Message, string, error) {
	definitions := tool.Definitions(r.Tools)
	toolsByName := tool.ByName(r.Tools)

	for round := 0; round < maxToolRounds; round++ {
		response, err := r.Provider.Send(ctx, llm.Request{Messages: history, Tools: definitions})
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
			history = append(history, executeToolCall(ctx, toolsByName, call))
		}
	}

	return history, "", errors.New("tool call limit exceeded")
}

func executeToolCall(ctx context.Context, toolsByName map[string]tool.Tool, call llm.ToolCall) llm.Message {
	result := ""
	item := toolsByName[call.Name]
	if item == nil {
		result = fmt.Sprintf("error: tool %q is not available", call.Name)
	} else {
		output, err := item.Execute(ctx, call.Arguments)
		if err != nil {
			result = "error: " + err.Error()
		} else {
			result = output
		}
	}

	return llm.Message{Role: llm.RoleTool, ToolCallID: call.ID, Content: result}
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
