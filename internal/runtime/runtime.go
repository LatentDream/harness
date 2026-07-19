package runtime

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	"latentdream/harness/internal/environment"
	"latentdream/harness/internal/input"
	"latentdream/harness/internal/orchestrator"
	"latentdream/harness/internal/provider"
	"latentdream/harness/internal/runtime/command"
	"latentdream/harness/internal/runtime/execution"
	"latentdream/harness/internal/session"
	"latentdream/harness/internal/session/llm"
	"latentdream/harness/internal/tool"
	"latentdream/harness/internal/tool/model"
	"latentdream/harness/internal/tracing"

	"github.com/google/uuid"
)

const maxToolRounds = 20 // TODO: make configurable

type Runtime struct {
	id uuid.UUID

	Provider    provider.Provider
	Tools       []model.Tool
	Controller  orchestrator.Orchestrator
	Environment environment.Environment
	Session     session.Session

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

func (r *Runtime) Run(ctx context.Context) (runErr error) {
	if ctx == nil {
		ctx = context.Background()
	}
	workingDirectory, _ := os.Getwd()
	ctx, trace, err := tracing.BeginRun(ctx, tracing.RunMeta{WorkingDirectory: workingDirectory})
	if err != nil {
		return fmt.Errorf("start trace: %w", err)
	}
	defer trace.End(&runErr, r.sessionState)

	if err := r.initialize(); err != nil {
		return err
	}
	trace.Snapshot(r.sessionState())
	if err := trace.Checkpoint(); err != nil {
		return err
	}

	return r.runLoop(ctx, trace)
}

func (r *Runtime) runLoop(ctx context.Context, trace *tracing.RunScope) error {
	for {
		select {
		case <-ctx.Done():
			trace.SetReason(tracing.EndReasonCancelled)
			return ctx.Err()
		default:
		}

		text, err := r.input.Receive(ctx)
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			trace.SetReason(tracing.EndReasonCancelled)
			return err
		}
		if errors.Is(err, io.EOF) {
			trace.SetReason(tracing.EndReasonEOF)
			return nil
		}
		if err != nil {
			operationErr := fmt.Errorf("receive input: %w", err)
			return errors.Join(operationErr, execution.Write(ctx, r.input, "stderr", operationErr.Error()), tracing.Checkpoint(ctx))
		}

		commandText := strings.TrimSpace(text)
		if commandText == "" {
			execution.UserInput(ctx, "", text)
			if err := tracing.Checkpoint(ctx); err != nil {
				return err
			}
			continue
		}
		if r.isCommand(commandText) {
			action, err := r.handleCommand(ctx, text, commandText)
			if err != nil {
				return err
			}
			if action == command.ActionExit {
				trace.SetReason(tracing.EndReasonExit)
				return nil
			}
			continue
		}

		if err := r.handleTurn(ctx, text); err != nil {
			return err
		}
	}
}

func (r *Runtime) handleCommand(ctx context.Context, text string, commandText string) (command.Action, error) {
	ctx, span, err := tracing.BeginCommand(ctx, commandText, text)
	if err != nil {
		return command.ActionContinue, fmt.Errorf("start command trace: %w", err)
	}

	result, handled, commandErr := r.commands.Execute(commandText)
	if !handled {
		span.End(nil, nil)
		return command.ActionContinue, span.Checkpoint()
	}
	if commandErr != nil {
		operationErr := fmt.Errorf("execute command: %w", commandErr)
		writeErr := execution.Write(ctx, r.input, "stderr", operationErr.Error())
		span.End(operationErr, nil)
		return command.ActionContinue, errors.Join(operationErr, writeErr, span.Checkpoint())
	}

	var writeErr error
	if result.Output != "" {
		writeErr = execution.Write(ctx, r.input, "stdout", result.Output)
	}
	span.End(writeErr, result)
	return result.Action, errors.Join(writeErr, span.Checkpoint())
}

func (r *Runtime) handleTurn(ctx context.Context, text string) error {
	turnID := uuid.NewString()
	ctx, turn, err := tracing.BeginTurn(ctx, turnID, text)
	if err != nil {
		return fmt.Errorf("start turn trace: %w", err)
	}

	rollbackIndex := len(r.Session.Conversation)
	r.Session.Conversation = append(r.Session.Conversation, llm.Message{Role: llm.RoleUser, Content: text})

	response, inferenceErr := r.inference(ctx, turnID)
	if inferenceErr != nil {
		r.Session.Conversation = r.Session.Conversation[:rollbackIndex]

		turn.Rollback(rollbackIndex, r.sessionState())
		var writeErr error
		if !errors.Is(inferenceErr, context.Canceled) && !errors.Is(inferenceErr, context.DeadlineExceeded) {
			writeErr = execution.Write(ctx, r.input, "stdout", "error: "+inferenceErr.Error())
		}
		turn.End(inferenceErr, "", r.sessionState())
		traceErr := turn.Checkpoint()
		if errors.Is(inferenceErr, context.Canceled) || errors.Is(inferenceErr, context.DeadlineExceeded) {
			return errors.Join(inferenceErr, traceErr)
		}
		if tracing.IsRecordingError(inferenceErr) || writeErr != nil || traceErr != nil {
			return errors.Join(inferenceErr, writeErr, traceErr)
		}
		return nil
	}

	writeErr := execution.Write(ctx, r.input, "stdout", response)
	turn.End(writeErr, response, r.sessionState())
	return errors.Join(writeErr, turn.Checkpoint())
}

func (r *Runtime) inference(ctx context.Context, turnID string) (string, error) {
	definitions := tool.Definitions(r.Tools)
	toolsByName := tool.ByName(r.Tools)

	for round := 0; round < maxToolRounds; round++ {
		response, err := execution.LLMCall(ctx, r.input, r.Provider, llm.Request{
			Messages: r.Session.Conversation,
			Tools:    definitions,
		}, round+1, turnID)
		if err != nil {
			return "", err
		}

		message := response.Message
		r.Session.Conversation = append(r.Session.Conversation, message)
		if len(message.ToolCalls) == 0 {
			return message.Content, nil
		}
		for _, call := range message.ToolCalls {
			toolMessage, err := execution.ToolCall(ctx, r.input, toolsByName, call, turnID)
			if err != nil {
				return "", err
			}
			r.Session.Conversation = append(r.Session.Conversation, toolMessage)
		}
	}

	return "", errors.New("tool call limit exceeded")
}

func (r *Runtime) initialize() error {
	if r.Provider == nil {
		return errors.New("runtime: provider is required")
	}
	if r.input == nil {
		return errors.New("runtime: input is required")
	}
	if r.commands == nil {
		r.commands = command.DefaultRegistry()
	}
	if r.Tools == nil {
		r.Tools = tool.NewDefault()
	}
	r.Session.Init()
	return nil
}

func (r *Runtime) isCommand(text string) bool {
	return strings.HasPrefix(text, "/") || strings.HasPrefix(text, ":") || r.commands.IsCommand(text) != nil
}

func (r *Runtime) sessionState() tracing.SessionState {
	return tracing.SessionState{Conversation: r.Session.Conversation}
}
