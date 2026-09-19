package runtime

import (
	"context"
	"encoding/json"
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
	sessiontitle "latentdream/harness/internal/session/title"
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
	ChatTools   []model.Tool

	receiver         input.Receiver
	output           input.Sink
	commands         *command.Registry
	clipboard        command.Clipboard
	resetUI          bool
	sessionID        string
	sessionTitle     string
	loadedSession    bool
	pendingSessionID string
	titleGenerator   TitleGenerator
	titleUpdater     TitleUpdater
	steering         *input.SteeringInbox
}

type TitleGenerator interface {
	Generate(context.Context, string) (string, error)
}

type TitleUpdater interface {
	SetTitle(context.Context, string) (string, error)
}

type promptContextProvider interface {
	PromptContext() string
}

type conversationStateRestorer interface {
	Restore([]llm.Message)
}

type Options struct {
	Commands       *command.Registry
	Clipboard      command.Clipboard
	ResetFrontend  bool
	ChatTools      []model.Tool
	InitialSession *session.Session
	SessionID      string
	SessionTitle   string
	TitleGenerator TitleGenerator
	TitleUpdater   TitleUpdater
	Steering       *input.SteeringInbox
}

func New(aiProvider provider.Provider, receiver input.Receiver, output input.Sink, options Options) *Runtime {
	if options.Commands == nil {
		options.Commands = command.DefaultRegistry()
	}
	if options.Clipboard == nil {
		options.Clipboard = clipboard.NewSystem()
	}
	if options.Steering == nil {
		options.Steering = input.NewSteeringInbox()
	}
	r := &Runtime{
		id:             uuid.New(),
		Provider:       aiProvider,
		Tools:          tool.NewDefault(),
		ChatTools:      options.ChatTools,
		receiver:       receiver,
		output:         output,
		commands:       options.Commands,
		clipboard:      options.Clipboard,
		resetUI:        options.ResetFrontend,
		sessionID:      options.SessionID,
		sessionTitle:   options.SessionTitle,
		titleGenerator: options.TitleGenerator,
		titleUpdater:   options.TitleUpdater,
		steering:       options.Steering,
	}
	if options.InitialSession != nil {
		r.Session = cloneSession(*options.InitialSession)
		r.loadedSession = options.SessionID != "" || len(options.InitialSession.Conversation) > 0 || strings.TrimSpace(options.SessionTitle) != ""
	}
	r.commands.Register(command.NewCopyCmd(r.latestCopyableMessage, r.clipboard))
	r.commands.Register(command.NewModelCmd(r.Provider))
	return r
}

func (r *Runtime) SwitchSessionID() string { return r.pendingSessionID }

// SendMessage queues guidance for the currently executing turn. The model sees
// it at the next safe request boundary; an in-flight provider or tool call is
// never mutated or cancelled.
func (r *Runtime) SendMessage(ctx context.Context, text string) error {
	if r == nil || r.steering == nil {
		return input.ErrNoActiveTurn
	}
	if ctx == nil {
		ctx = context.Background()
	}
	return r.steering.SendMessage(ctx, text)
}

func (r *Runtime) Run(ctx context.Context) (runErr error) {
	_, runErr = r.RunSession(ctx)
	return runErr
}

// RunSession runs one persisted session and reports the command that ended it.
func (r *Runtime) RunSession(ctx context.Context) (action command.Action, runErr error) {
	if ctx == nil {
		ctx = context.Background()
	}
	workingDirectory, _ := os.Getwd()
	ctx, trace, err := tracing.BeginRun(ctx, tracing.RunMeta{SessionID: r.sessionID, RunID: r.id.String(), WorkingDirectory: workingDirectory})
	if err != nil {
		return command.ActionContinue, fmt.Errorf("start trace: %w", err)
	}
	defer trace.End(&runErr, r.sessionState)

	if err := r.initialize(); err != nil {
		return command.ActionContinue, err
	}
	trace.Snapshot(r.sessionState())
	if err := trace.Checkpoint(); err != nil {
		return command.ActionContinue, err
	}
	if r.resetUI {
		event := input.Event{Kind: input.EventSessionReset}
		if r.loadedSession {
			event = r.sessionLoadedEvent()
		}
		if err := execution.Emit(ctx, r.output, event); err != nil {
			return command.ActionContinue, fmt.Errorf("reset session frontend: %w", err)
		}
		if err := trace.Checkpoint(); err != nil {
			return command.ActionContinue, err
		}
	}

	return r.runLoop(ctx, trace)
}

func (r *Runtime) runLoop(ctx context.Context, trace *tracing.RunScope) (command.Action, error) {
	for {
		select {
		case <-ctx.Done():
			trace.SetReason(tracing.EndReasonCancelled)
			return command.ActionContinue, ctx.Err()
		default:
		}

		submission, err := r.receiver.Receive(ctx)
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			trace.SetReason(tracing.EndReasonCancelled)
			return command.ActionContinue, err
		}
		if errors.Is(err, io.EOF) {
			trace.SetReason(tracing.EndReasonEOF)
			return command.ActionContinue, nil
		}
		if err != nil {
			operationErr := fmt.Errorf("receive input: %w", err)
			return command.ActionContinue, errors.Join(operationErr, execution.Write(ctx, r.output, input.StreamStderr, operationErr.Error()), tracing.Checkpoint(ctx))
		}

		submission.Mode, err = normalizeMode(submission.Mode)
		if err != nil {
			operationErr := fmt.Errorf("receive input: %w", err)
			return command.ActionContinue, errors.Join(operationErr, execution.Write(ctx, r.output, input.StreamStderr, operationErr.Error()), tracing.Checkpoint(ctx))
		}
		text := submission.Text
		commandText := strings.TrimSpace(text)
		if commandText == "" {
			execution.UserInput(ctx, "", text, submission.Mode)
			if err := tracing.Checkpoint(ctx); err != nil {
				return command.ActionContinue, err
			}
			continue
		}
		if r.isCommand(commandText) {
			action, err := r.handleCommand(ctx, text, commandText, submission.Mode)
			if err != nil {
				return command.ActionContinue, err
			}
			if action == command.ActionExit {
				trace.SetReason(tracing.EndReasonExit)
				return action, nil
			}
			if action == command.ActionNewSession {
				trace.SetReason(tracing.EndReasonNewSession)
				return action, nil
			}
			if action == command.ActionSwitchSession {
				trace.SetReason(tracing.EndReasonSessionSwitch)
				return action, nil
			}
			continue
		}

		if err := r.handleInterruptibleTurn(ctx, submission); err != nil {
			return command.ActionContinue, err
		}
	}
}

func (r *Runtime) handleInterruptibleTurn(ctx context.Context, submission input.Submission) error {
	if lifecycle, ok := r.output.(input.TurnLifecycle); ok {
		lifecycle.TurnStarted(r.sessionID)
		defer lifecycle.TurnCompleted(r.sessionID)
	}

	interrupter, ok := r.receiver.(input.Interrupter)
	if !ok || interrupter == nil {
		return r.handleTurn(ctx, submission)
	}

	interrupts := interrupter.Interrupts()
	if interrupts == nil {
		return r.handleTurn(ctx, submission)
	}
	drainInterrupts(interrupts)

	turnCtx, cancelTurn := context.WithCancel(ctx)
	done := make(chan struct{})
	go func() {
		select {
		case <-interrupts:
			cancelTurn()
		case <-done:
		case <-ctx.Done():
		}
	}()

	err := r.handleTurn(turnCtx, submission)
	close(done)
	cancelTurn()
	drainInterrupts(interrupts)

	if err != nil && errors.Is(err, context.Canceled) && turnCtx.Err() != nil && ctx.Err() == nil {
		return nil
	}
	return err
}

func drainInterrupts(interrupts <-chan struct{}) {
	for {
		select {
		case <-interrupts:
		default:
			return
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
	if result.Action == command.ActionSwitchSession {
		r.pendingSessionID = result.SessionID
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
	shouldCreateTitle := r.sessionTitle == "" && countUserMessages(r.Session.Conversation) == 0
	if err := r.steering.BeginTurn(); err != nil {
		operationErr := fmt.Errorf("start turn guidance: %w", err)
		turn.End(operationErr, "", r.sessionState())
		return errors.Join(operationErr, turn.Checkpoint())
	}
	defer r.steering.EndTurn()

	r.Session.Conversation = append(r.Session.Conversation, llm.Message{Role: llm.RoleUser, Content: submission.Text})

	response, inferenceErr := r.inference(ctx, turnID, submission.Mode)
	for _, text := range r.steering.EndTurn() {
		// Guidance accepted just as an operation failed still belongs in the
		// trace, but the rollback below intentionally keeps it out of history.
		tracing.SteeringInput(ctx, turnID, text, submission.Mode)
	}
	if inferenceErr != nil {
		r.Session.Conversation = r.Session.Conversation[:rollbackIndex]
		restoreToolState(r.Tools, r.Session.Conversation)

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
	if err := turn.Checkpoint(); err != nil {
		return err
	}
	if shouldCreateTitle {
		r.createTitle(ctx, submission.Text)
	}
	return nil
}

func (r *Runtime) inference(ctx context.Context, turnID string, mode input.Mode) (string, error) {
	availableTools := r.toolsForMode(mode)
	definitions := tool.Definitions(availableTools)
	toolsByName := tool.ByName(availableTools)

	for round := 0; round < maxToolRounds; round++ {
		messages := r.messagesForRequest(mode, availableTools)
		response, err := execution.LLMCall(ctx, r.output, r.Provider, llm.Request{
			Messages: messages,
			Tools:    definitions,
		}, round+1, turnID)
		if err != nil {
			return "", err
		}

		message := response.Message
		r.Session.Conversation = append(r.Session.Conversation, message)
		if len(message.ToolCalls) != 0 {
			for _, call := range message.ToolCalls {
				toolMessage, err := execution.ToolCall(ctx, r.output, toolsByName, call, turnID, round+1)
				if err != nil {
					return "", err
				}
				r.Session.Conversation = append(r.Session.Conversation, toolMessage)
			}
			if err := r.appendSteering(ctx, turnID, mode, r.steering.Drain()); err != nil {
				return "", err
			}
			continue
		}

		steering, closed := r.steering.DrainOrClose()
		if err := r.appendSteering(ctx, turnID, mode, steering); err != nil {
			return "", err
		}
		if closed {
			return message.Content, nil
		}
	}

	return "", errors.New("tool call limit exceeded")
}

func (r *Runtime) appendSteering(ctx context.Context, turnID string, mode input.Mode, messages []string) error {
	for _, text := range messages {
		r.Session.Conversation = append(r.Session.Conversation, llm.Message{Role: llm.RoleUser, Content: text})
		tracing.SteeringInput(ctx, turnID, text, mode)
	}
	return tracing.Checkpoint(ctx)
}

func (r *Runtime) toolsForMode(mode input.Mode) []model.Tool {
	switch mode {
	case input.ModePlan:
		available := tool.WithCapability(r.Tools, model.CapabilityReadOnly)
		return append(available, tool.WithCapability(r.Tools, model.CapabilityAgentState)...)
	case input.ModeChat:
		return r.ChatTools
	default:
		return r.Tools
	}
}

func (r *Runtime) messagesForRequest(mode input.Mode, availableTools []model.Tool) []llm.Message {
	messages := messagesForMode(r.Session.Conversation, mode)
	if mode == input.ModeChat {
		return messages
	}
	for _, item := range availableTools {
		provider, ok := item.(promptContextProvider)
		if ok {
			messages = messagesWithInstruction(messages, provider.PromptContext())
		}
	}
	return messages
}

func restoreToolState(tools []model.Tool, messages []llm.Message) {
	for _, item := range tools {
		if restorer, ok := item.(conversationStateRestorer); ok {
			restorer.Restore(messages)
		}
	}
}

func messagesForMode(messages []llm.Message, mode input.Mode) []llm.Message {
	switch mode {
	case input.ModePlan:
		return messagesWithInstruction(messages, session.PlanModeInstruction)
	case input.ModeChat:
		return messagesWithInstruction(messages, session.ChatModeInstruction)
	default:
		return messages
	}
}

func messagesWithInstruction(messages []llm.Message, instruction string) []llm.Message {
	result := append([]llm.Message(nil), messages...)
	for index := range result {
		if result[index].Role == llm.RoleSystem {
			result[index].Content += "\n\n" + instruction
			return result
		}
	}
	return append([]llm.Message{{Role: llm.RoleSystem, Content: instruction}}, result...)
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
	restoreToolState(r.Tools, r.Session.Conversation)
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

func (r *Runtime) createTitle(ctx context.Context, prompt string) {
	if r.titleGenerator == nil || r.titleUpdater == nil || strings.TrimSpace(prompt) == "" {
		return
	}
	title, err := r.titleGenerator.Generate(ctx, prompt)
	if err != nil {
		if ctx.Err() != nil {
			return
		}
		title = sessiontitle.Fallback(prompt)
	}
	persisted, err := r.titleUpdater.SetTitle(ctx, title)
	if err != nil {
		return
	}
	r.sessionTitle = persisted
	tracing.Record(ctx, tracing.Event{Kind: tracing.KindSessionTitleUpdated, Payload: struct {
		SessionID string `json:"sessionId"`
		Title     string `json:"title"`
	}{SessionID: r.sessionID, Title: persisted}})
	_ = execution.Emit(ctx, r.output, input.Event{
		Kind: input.EventSessionTitleChanged, SessionID: r.sessionID, SessionTitle: persisted,
	})
	_ = tracing.Checkpoint(ctx)
}

func countUserMessages(messages []llm.Message) int {
	count := 0
	for _, message := range messages {
		if message.Role == llm.RoleUser {
			count++
		}
	}
	return count
}

func (r *Runtime) sessionLoadedEvent() input.Event {
	messages := make([]input.PresentationMessage, 0, len(r.Session.Conversation))
	for _, message := range r.Session.Conversation {
		if (message.Role == llm.RoleUser || message.Role == llm.RoleAssistant) && strings.TrimSpace(message.Content) != "" {
			messages = append(messages, input.PresentationMessage{Role: message.Role, Content: message.Content})
		}
	}
	return input.Event{Kind: input.EventSessionLoaded, SessionID: r.sessionID, SessionTitle: r.sessionTitle, Messages: messages}
}

func cloneSession(source session.Session) session.Session {
	conversation := make([]llm.Message, len(source.Conversation))
	for i, message := range source.Conversation {
		conversation[i] = message
		conversation[i].ToolCalls = append([]llm.ToolCall(nil), message.ToolCalls...)
		for j := range conversation[i].ToolCalls {
			conversation[i].ToolCalls[j].Arguments = append(json.RawMessage(nil), message.ToolCalls[j].Arguments...)
		}
	}
	return session.Session{Conversation: conversation}
}

func normalizeMode(mode input.Mode) (input.Mode, error) {
	switch input.Mode(strings.ToLower(strings.TrimSpace(string(mode)))) {
	case "", input.ModeBuild:
		return input.ModeBuild, nil
	case input.ModePlan:
		return input.ModePlan, nil
	case input.ModeChat:
		return input.ModeChat, nil
	default:
		return "", fmt.Errorf("unsupported submission mode %q", mode)
	}
}
