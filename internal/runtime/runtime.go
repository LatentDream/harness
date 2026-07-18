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

const maxToolRounds = 8

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
	ctx, runErr = tracing.StartRun(ctx, tracing.RunMeta{WorkingDirectory: workingDirectory})
	if runErr != nil {
		return fmt.Errorf("start trace: %w", runErr)
	}

	endReason := tracing.EndReasonError
	defer func() {
		finalCtx := context.WithoutCancel(ctx)
		runErr = errors.Join(runErr, tracing.Snapshot(finalCtx, tracing.SessionState{
			Conversation: r.Session.Conversation,
		}))

		status := tracing.StatusSuccess
		traceErr := traceError(runErr)
		if errors.Is(runErr, context.Canceled) || errors.Is(runErr, context.DeadlineExceeded) {
			status = tracing.StatusCancelled
			endReason = tracing.EndReasonCancelled
		} else if runErr != nil {
			status = tracing.StatusFailure
			endReason = tracing.EndReasonError
		}
		runErr = errors.Join(runErr, tracing.CloseRun(finalCtx, tracing.RunOutcome{
			Status: status,
			Reason: endReason,
			Error:  traceErr,
		}))
	}()
	if err := r.validateInput(); err != nil {
		return err
	}

	if r.commands == nil {
		r.commands = command.DefaultRegistry()
	}
	if r.Tools == nil {
		r.Tools = tool.NewDefault()
	}

	r.Session.Init()
	if err := tracing.Snapshot(ctx, tracing.SessionState{Conversation: r.Session.Conversation}); err != nil {
		return fmt.Errorf("record initial session: %w", err)
	}

	for {
		select {
		case <-ctx.Done():
			endReason = tracing.EndReasonCancelled
			return ctx.Err()
		default:
		}

		// User Input ~~~~~~~~~~~~~~~~~~~~
		text, err := r.input.Receive()
		if errors.Is(err, io.EOF) {
			endReason = tracing.EndReasonEOF
			return nil
		}
		if err != nil {
			operationErr := fmt.Errorf("receive input: %w", err)
			return errors.Join(operationErr, r.writeOutput(ctx, "stderr", operationErr.Error()))
		}
		commandText := strings.TrimSpace(text)
		if commandText == "" {
			if err := recordUserInput(ctx, "", text); err != nil {
				return err
			}
			continue
		}

		// Command (:q, :help, ...) ~~~~~~
		commandCtx := ctx
		isCommand := strings.HasPrefix(commandText, "/") || strings.HasPrefix(commandText, ":") || r.commands.IsCommand(commandText) != nil
		if isCommand {
			commandCtx, err = tracing.StartSpan(ctx, tracing.SpanStart{
				Kind: tracing.SpanCommand,
				Payload: struct {
					Input string `json:"input"`
				}{Input: commandText},
			})
			if err != nil {
				return fmt.Errorf("start command trace: %w", err)
			}
			if err := recordUserInput(commandCtx, "", text); err != nil {
				return err
			}
		}
		commandResult, handled, err := r.commands.Execute(commandText)
		if handled {
			if err != nil {
				operationErr := fmt.Errorf("execute command: %w", err)
				traceErr := tracing.EndSpan(commandCtx, tracing.SpanEnd{
					Status: tracing.StatusFailure,
					Error:  traceError(operationErr),
				})
				return errors.Join(operationErr, traceErr, r.writeOutput(commandCtx, "stderr", operationErr.Error()))
			}
			if commandResult.Output != "" {
				if writeErr := r.writeOutput(commandCtx, "stdout", commandResult.Output); writeErr != nil {
					operationErr := fmt.Errorf("write command output: %w", writeErr)
					return errors.Join(operationErr, tracing.EndSpan(commandCtx, tracing.SpanEnd{
						Status: tracing.StatusFailure,
						Error:  traceError(operationErr),
					}))
				}
			}
			if traceErr := tracing.EndSpan(commandCtx, tracing.SpanEnd{
				Status:  tracing.StatusSuccess,
				Payload: commandResult,
			}); traceErr != nil {
				return fmt.Errorf("end command trace: %w", traceErr)
			}
			if commandResult.Action == command.ActionExit {
				endReason = tracing.EndReasonExit
				return nil
			}
			continue
		}

		// Build context ~~~~~~~~~~~~~~~~~~
		turnID := uuid.NewString()
		turnCtx, err := tracing.StartSpan(ctx, tracing.SpanStart{Kind: tracing.SpanTurn, TurnID: turnID})
		if err != nil {
			return fmt.Errorf("start turn trace: %w", err)
		}
		if err := recordUserInput(turnCtx, turnID, text); err != nil {
			return err
		}
		rollbackIndex := len(r.Session.Conversation)
		r.Session.Conversation = append(r.Session.Conversation, llm.Message{Role: llm.RoleUser, Content: text})

		// inference + tool execution ~~~~~
		var response string
		response, err = r.inference(turnCtx, turnID)
		if err != nil {
			r.Session.Conversation = r.Session.Conversation[:rollbackIndex]
			operationErr := err
			traceErr := tracing.Record(turnCtx, tracing.Event{
				Kind:   tracing.KindSessionRollback,
				TurnID: turnID,
				Payload: struct {
					ConversationLength int `json:"conversationLength"`
				}{ConversationLength: rollbackIndex},
			})
			traceErr = errors.Join(traceErr, tracing.Snapshot(turnCtx, tracing.SessionState{
				Conversation: r.Session.Conversation,
			}))
			traceErr = errors.Join(traceErr, r.writeOutput(turnCtx, "stdout", "error: "+operationErr.Error()))
			traceErr = errors.Join(traceErr, tracing.EndSpan(turnCtx, tracing.SpanEnd{
				Status: tracing.StatusFailure,
				Error:  traceError(operationErr),
				Payload: tracing.TurnOutcome{
					Status: tracing.StatusFailure,
					Error:  traceError(operationErr),
				},
			}))
			if traceErr != nil {
				return errors.Join(operationErr, traceErr)
			}
			continue
		}

		turnErr := r.writeOutput(turnCtx, "stdout", response)
		turnErr = errors.Join(turnErr, tracing.Snapshot(turnCtx, tracing.SessionState{
			Conversation: r.Session.Conversation,
		}))
		status := tracing.StatusSuccess
		if turnErr != nil {
			status = tracing.StatusFailure
		}
		turnErr = errors.Join(turnErr, tracing.EndSpan(turnCtx, tracing.SpanEnd{
			Status: status,
			Error:  traceError(turnErr),
			Payload: tracing.TurnOutcome{
				Status:      status,
				Error:       traceError(turnErr),
				FinalAnswer: response,
			},
		}))
		if turnErr != nil {
			return fmt.Errorf("complete turn: %w", turnErr)
		}
	}
}

func (r *Runtime) inference(ctx context.Context, turnID string) (string, error) {
	definitions := tool.Definitions(r.Tools)
	toolsByName := tool.ByName(r.Tools)

	for round := 0; round < maxToolRounds; round++ {
		request := llm.Request{Messages: r.Session.Conversation, Tools: definitions}
		selection := r.Provider.Current()
		callCtx, err := tracing.StartSpan(ctx, tracing.SpanStart{
			Kind:   tracing.SpanLLMCall,
			TurnID: turnID,
			Payload: struct {
				Provider string      `json:"provider"`
				Model    string      `json:"model"`
				Round    int         `json:"round"`
				Request  llm.Request `json:"request"`
			}{Provider: selection.Provider, Model: selection.Model, Round: round + 1, Request: request},
		})
		if err != nil {
			return "", fmt.Errorf("start LLM trace: %w", err)
		}
		var response provider.Response
		err = execution.WithStatus(callCtx, r.input, "inference", func() error {
			var sendErr error
			response, sendErr = r.Provider.Send(callCtx, request)
			return sendErr
		})
		if err != nil {
			return "", errors.Join(err, tracing.EndSpan(callCtx, tracing.SpanEnd{
				Status: traceStatus(err),
				Error:  traceError(err),
			}))
		}

		message := response.Message
		for index := range message.ToolCalls {
			if strings.TrimSpace(message.ToolCalls[index].ID) == "" {
				message.ToolCalls[index].ID = fmt.Sprintf("call_%d", index+1)
			}
		}
		response.Message = message
		if err := tracing.EndSpan(callCtx, tracing.SpanEnd{
			Status: tracing.StatusSuccess,
			Payload: struct {
				Provider string      `json:"provider"`
				Model    string      `json:"model"`
				Round    int         `json:"round"`
				Message  llm.Message `json:"message"`
			}{Provider: response.Provider, Model: response.Model, Round: round + 1, Message: message},
		}); err != nil {
			return "", fmt.Errorf("end LLM trace: %w", err)
		}
		if err := tracing.Record(ctx, tracing.Event{
			Kind:   tracing.KindAssistantMessage,
			TurnID: turnID,
			Payload: struct {
				Message llm.Message `json:"message"`
			}{Message: message},
		}); err != nil {
			return "", fmt.Errorf("record assistant message: %w", err)
		}
		if len(message.ToolCalls) == 0 {
			r.Session.Conversation = append(r.Session.Conversation, message)
			return message.Content, nil
		}

		r.Session.Conversation = append(r.Session.Conversation, message)

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

func (r *Runtime) validateInput() error {
	if r.Provider == nil {
		return errors.New("runtime: provider is required")
	}
	if r.input == nil {
		return errors.New("runtime: input is required")
	}
	return nil
}

func (r *Runtime) writeOutput(ctx context.Context, stream string, text string) error {
	var err error
	if stream == "stderr" {
		err = r.input.WriteErr(text)
	} else {
		err = r.input.Write(text)
	}
	if err != nil {
		return err
	}

	return tracing.Record(ctx, tracing.Event{
		Kind: tracing.KindOutput,
		Payload: struct {
			Stream string `json:"stream"`
			Text   string `json:"text"`
		}{Stream: stream, Text: text},
	})
}

func traceStatus(err error) tracing.Status {
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return tracing.StatusCancelled
	}
	if err != nil {
		return tracing.StatusFailure
	}
	return tracing.StatusSuccess
}

func traceError(err error) *tracing.TraceError {
	if err == nil {
		return nil
	}
	return &tracing.TraceError{Message: err.Error()}
}

func recordUserInput(ctx context.Context, turnID string, text string) error {
	if err := tracing.Record(ctx, tracing.Event{
		Kind:   tracing.KindUserInput,
		TurnID: turnID,
		Payload: struct {
			Text string `json:"text"`
		}{Text: text},
	}); err != nil {
		return fmt.Errorf("record user input: %w", err)
	}
	return nil
}
