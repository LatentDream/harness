package runtime

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	"latentdream/harness/internal/environment"
	"latentdream/harness/internal/environment/clipboard"
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

const maxToolRounds = 64 // TODO: make configurable

type Runtime struct {
	id uuid.UUID

	Provider    provider.Provider
	Tools       []model.Tool
	Controller  orchestrator.Orchestrator
	Environment environment.Environment
	Session     session.Session

	receiver  input.Receiver
	output    input.Sink
	commands  *command.Registry
	clipboard command.Clipboard
}

type Options struct {
	Commands  *command.Registry
	Clipboard command.Clipboard
}

func New(aiProvider provider.Provider, receiver input.Receiver, output input.Sink, options Options) *Runtime {
	if options.Commands == nil {
		options.Commands = command.DefaultRegistry()
	}
	if options.Clipboard == nil {
		options.Clipboard = clipboard.NewSystem()
	}
	r := &Runtime{
		id:        uuid.New(),
		Provider:  aiProvider,
		Tools:     tool.NewDefault(),
		receiver:  receiver,
		output:    output,
		commands:  options.Commands,
		clipboard: options.Clipboard,
	}
	r.commands.Register(command.NewCopyCmd(r.latestCopyableMessage, r.clipboard))
	r.commands.Register(command.NewModelCmd(r.Provider))
	return r
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

		submission, err := r.receiver.Receive(ctx)
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
			return errors.Join(operationErr, execution.Write(ctx, r.output, input.StreamStderr, operationErr.Error()), tracing.Checkpoint(ctx))
		}

		submission.Mode, err = normalizeMode(submission.Mode)
		if err != nil {
			operationErr := fmt.Errorf("receive input: %w", err)
			return errors.Join(operationErr, execution.Write(ctx, r.output, input.StreamStderr, operationErr.Error()), tracing.Checkpoint(ctx))
		}
		text := submission.Text
		commandText := strings.TrimSpace(text)
		if commandText == "" {
			execution.UserInput(ctx, "", text, submission.Mode)
			if err := tracing.Checkpoint(ctx); err != nil {
				return err
			}
			continue
		}
		if r.isCommand(commandText) {
			action, err := r.handleCommand(ctx, text, commandText, submission.Mode)
			if err != nil {
				return err
			}
			if action == command.ActionExit {
				trace.SetReason(tracing.EndReasonExit)
				return nil
			}
			continue
		}

		if err := r.handleTurn(ctx, submission); err != nil {
			return err
		}
	}
}

func (r *Runtime) handleCommand(ctx context.Context, text string, commandText string, mode input.Mode) (command.Action, error) {
	ctx, span, err := tracing.BeginCommand(ctx, commandText, text, mode)
	if err != nil {
		return command.ActionContinue, fmt.Errorf("start command trace: %w", err)
	}

	result, handled, commandErr := r.commands.Execute(ctx, commandText)
	if !handled {
		span.End(nil, nil)
		return command.ActionContinue, span.Checkpoint()
	}
	if commandErr != nil {
		operationErr := fmt.Errorf("execute command: %w", commandErr)
		writeErr := execution.Write(ctx, r.output, input.StreamStderr, operationErr.Error())
		span.End(operationErr, nil)
		return command.ActionContinue, errors.Join(operationErr, writeErr, span.Checkpoint())
	}

	var writeErr error
	if result.Output != "" {
		writeErr = execution.Write(ctx, r.output, input.StreamStdout, result.Output)
	}
	if result.Provider != "" || result.Model != "" {
		writeErr = errors.Join(writeErr, execution.Emit(ctx, r.output, input.Event{
			Kind:     input.EventProviderSelection,
			Provider: result.Provider,
			Model:    result.Model,
		}))
	}
	span.End(writeErr, result)
	return result.Action, errors.Join(writeErr, span.Checkpoint())
}

func (r *Runtime) handleTurn(ctx context.Context, submission input.Submission) error {
	turnID := uuid.NewString()
	ctx, turn, err := tracing.BeginTurn(ctx, turnID, submission.Text, submission.Mode)
	if err != nil {
		return fmt.Errorf("start turn trace: %w", err)
	}

	rollbackIndex := len(r.Session.Conversation)
	r.Session.Conversation = append(r.Session.Conversation, llm.Message{Role: llm.RoleUser, Content: submission.Text})

	response, inferenceErr := r.inference(ctx, turnID, submission.Mode)
	if inferenceErr != nil {
		r.Session.Conversation = r.Session.Conversation[:rollbackIndex]

		turn.Rollback(rollbackIndex, r.sessionState())
		var writeErr error
		if !errors.Is(inferenceErr, context.Canceled) && !errors.Is(inferenceErr, context.DeadlineExceeded) {
			writeErr = execution.Emit(ctx, r.output, input.Event{
				Kind: input.EventOutput, TurnID: turnID, Stream: input.StreamStdout, Text: "error: " + inferenceErr.Error(),
			})
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

	turn.End(nil, response, r.sessionState())
	return turn.Checkpoint()
}

func (r *Runtime) inference(ctx context.Context, turnID string, mode input.Mode) (string, error) {
	availableTools := r.Tools
	if mode == input.ModePlan {
		availableTools = tool.WithCapability(r.Tools, model.CapabilityReadOnly)
	}
	definitions := tool.Definitions(availableTools)
	toolsByName := tool.ByName(availableTools)

	for round := 0; round < maxToolRounds; round++ {
		messages := r.Session.Conversation
		if mode == input.ModePlan {
			messages = messagesWithPlanInstruction(messages)
		}
		response, err := execution.LLMCall(ctx, r.output, r.Provider, llm.Request{
			Messages: messages,
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
			toolMessage, err := execution.ToolCall(ctx, r.output, toolsByName, call, turnID)
			if err != nil {
				return "", err
			}
			r.Session.Conversation = append(r.Session.Conversation, toolMessage)
		}
	}

	return "", errors.New("tool call limit exceeded")
}

func messagesWithPlanInstruction(messages []llm.Message) []llm.Message {
	result := append([]llm.Message(nil), messages...)
	for index := range result {
		if result[index].Role == llm.RoleSystem {
			result[index].Content += "\n\n" + session.PlanModeInstruction
			return result
		}
	}
	return append([]llm.Message{{Role: llm.RoleSystem, Content: session.PlanModeInstruction}}, result...)
}

func (r *Runtime) initialize() error {
	if r.Provider == nil {
		return errors.New("runtime: provider is required")
	}
	if r.receiver == nil {
		return errors.New("runtime: input receiver is required")
	}
	if r.output == nil {
		return errors.New("runtime: output sink is required")
	}
	if r.Tools == nil {
		r.Tools = tool.NewDefault()
	}
	r.Session.Init()
	return nil
}

func (r *Runtime) latestCopyableMessage() (string, bool) {
	for index := len(r.Session.Conversation) - 1; index >= 0; index-- {
		message := r.Session.Conversation[index]
		if message.Role != llm.RoleAssistant {
			continue
		}
		if strings.TrimSpace(message.Content) == "" {
			continue
		}
		return message.Content, true
	}
	return "", false
}

func (r *Runtime) isCommand(text string) bool {
	return strings.HasPrefix(text, "/") || strings.HasPrefix(text, ":") || r.commands.IsCommand(text) != nil
}

func (r *Runtime) sessionState() tracing.SessionState {
	return tracing.SessionState{Conversation: r.Session.Conversation}
}

func normalizeMode(mode input.Mode) (input.Mode, error) {
	switch input.Mode(strings.ToLower(strings.TrimSpace(string(mode)))) {
	case "", input.ModeBuild:
		return input.ModeBuild, nil
	case input.ModePlan:
		return input.ModePlan, nil
	default:
		return "", fmt.Errorf("unsupported submission mode %q", mode)
	}
}
