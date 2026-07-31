package tui

import (
	"context"
	"errors"
	"fmt"
	"io"
	"math/rand/v2"
	"os"
	"os/signal"
	"os/user"
	"path/filepath"
	"strings"
	"time"

	"latentdream/harness/internal/input"
	"latentdream/harness/internal/provider"
	"latentdream/harness/internal/runtime/command"
)

type Options struct {
	Provider         string
	Model            string
	WorkingDirectory string
	Username         string
	Commands         *command.Registry
	Models           []provider.Selection
}

type UI struct {
	in       *os.File
	out      *os.File
	err      *os.File
	terminal *nativeTerminal
	fzf      string
	options  Options

	submissions chan input.Submission
	events      chan input.Event
	ready       chan struct{}
	interrupts  chan struct{}

	localShellResults chan localShellRunResult
	localShellCancel  context.CancelFunc
}

type blockKind int

const (
	blockUser blockKind = iota
	blockAssistant
	blockOutput
	blockError
	blockToolRead
	blockToolWrite
	blockToolBash
	blockToolWebfetch
	blockShell
)

type transcriptBlock struct {
	kind        blockKind
	text        string
	mode        input.Mode
	turnID      string
	round       int
	interrupted bool
	activity    input.ToolActivity
	completed   bool
}

type streamKey struct {
	turnID string
	round  int
}

type toolKey struct {
	turnID string
	callID string
}

type promptMode int

const (
	promptNormal promptMode = iota
	promptShell
)

type localShellRunResult struct {
	result localShellResult
	err    error
}

type model struct {
	width               int
	height              int
	provider            string
	modelName           string
	workingDirectory    string
	username            string
	tip                 string
	mode                input.Mode
	promptMode          promptMode
	focused             bool
	ready               bool
	isInferenceRunning  bool
	isLocalShellRunning bool
	status              string
	notice              string
	spinner             int
	scrollOffset        int
	colors              palette
	editor              editor
	blocks              []transcriptBlock
	streams             map[streamKey]int
	tools               map[toolKey]int
}

type readResult struct {
	data []byte
	err  error
}

func New(in, out, errOut *os.File, options Options) (*UI, error) {
	if in == nil || out == nil || errOut == nil {
		return nil, errors.New("tui requires stdin, stdout, and stderr")
	}
	if options.Commands == nil {
		options.Commands = command.DefaultRegistry()
	}
	if options.WorkingDirectory == "" {
		options.WorkingDirectory, _ = os.Getwd()
	}
	if options.Username == "" {
		if current, err := user.Current(); err == nil {
			options.Username = current.Username
		}
	}
	terminal := newNativeTerminal(in, out)
	ui := &UI{
		in:                in,
		out:               out,
		err:               errOut,
		terminal:          terminal,
		options:           options,
		submissions:       make(chan input.Submission, 1),
		events:            make(chan input.Event, 128),
		ready:             make(chan struct{}, 1),
		interrupts:        make(chan struct{}, 1),
		localShellResults: make(chan localShellRunResult, 1),
	}
	if terminal.IsInteractive() {
		fzf, findErr := findFZF()
		if findErr != nil {
			return nil, findErr
		}
		ui.fzf = fzf
	}
	return ui, nil
}

func IsInteractive(in, out *os.File) bool {
	return newNativeTerminal(in, out).IsInteractive()
}

func (u *UI) Receive(ctx context.Context) (input.Submission, error) {
	select {
	case u.ready <- struct{}{}:
	default:
	}
	select {
	case submission := <-u.submissions:
		return submission, nil
	case <-ctx.Done():
		return input.Submission{}, ctx.Err()
	}
}

func (u *UI) Emit(ctx context.Context, event input.Event) error {
	select {
	case u.events <- event:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (u *UI) Interrupts() <-chan struct{} {
	return u.interrupts
}

func (u *UI) Run(ctx context.Context, runRuntime func(context.Context) error) (runErr error) {
	if runRuntime == nil {
		return errors.New("tui runtime function is required")
	}
	if !u.terminal.IsInteractive() {
		return errTerminalNotInteractive
	}
	if u.fzf == "" {
		return errors.New("fzf is required for interactive mode")
	}
	if err := u.terminal.Enter(); err != nil {
		return err
	}
	defer func() { runErr = errors.Join(runErr, u.terminal.Restore()) }()

	runCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	runtimeDone := make(chan error, 1)
	runtimeStopped := make(chan struct{})
	go func() {
		defer close(runtimeStopped)
		runtimeDone <- runRuntime(runCtx)
	}()

	width, height, err := u.terminal.Size()
	if err != nil {
		return err
	}
	state := model{
		width:            width,
		height:           height,
		provider:         u.options.Provider,
		modelName:        u.options.Model,
		workingDirectory: u.options.WorkingDirectory,
		username:         u.options.Username,
		tip:              randomTip(),
		mode:             input.ModeBuild,
		focused:          true,
		colors:           palette{enabled: os.Getenv("NO_COLOR") == "" && os.Getenv("TERM") != "dumb"},
		streams:          make(map[streamKey]int),
		tools:            make(map[toolKey]int),
	}
	state.editor.historyIndex = 0
	if err := u.draw(&state); err != nil {
		return err
	}

	readRequests := make(chan struct{}, 1)
	readResults := make(chan readResult, 1)
	pumpCtx, stopPump := context.WithCancel(context.Background())
	pumpDone := make(chan struct{})
	go inputPump(pumpCtx, u.in, readRequests, readResults, pumpDone)
	defer func() {
		cancel()
		stopPump()
		<-pumpDone
		select {
		case <-runtimeStopped:
		case <-time.After(time.Second):
		}
	}()
	requestRead(readRequests)
	decoder := keyDecoder{}

	resize := make(chan os.Signal, 1)
	if resizeSignal := platformResizeSignal(); resizeSignal != nil {
		signal.Notify(resize, resizeSignal)
		defer signal.Stop(resize)
	}
	ticker := time.NewTicker(120 * time.Millisecond)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case err := <-runtimeDone:
			if errors.Is(err, context.Canceled) && ctx.Err() == nil {
				return nil
			}
			return err
		case <-u.ready:
			if !state.isLocalShellRunning {
				state.ready = true
			}
			state.isInferenceRunning = false
			if !state.isLocalShellRunning {
				state.status = ""
			}
			state.tip = randomTip()
			if err := u.draw(&state); err != nil {
				return err
			}
		case result := <-u.localShellResults:
			state.applyLocalShellResult(result)
			u.localShellCancel = nil
			if err := u.draw(&state); err != nil {
				return err
			}
		case event := <-u.events:
			state.apply(event)
			for drained := 0; drained < 64; drained++ {
				select {
				case queued := <-u.events:
					state.apply(queued)
				default:
					drained = 64
				}
			}
			if err := u.draw(&state); err != nil {
				return err
			}
		case result := <-readResults:
			if result.err != nil {
				if errors.Is(result.err, io.EOF) {
					cancel()
					return nil
				}
				return fmt.Errorf("read terminal input: %w", result.err)
			}
			pressedKeys := decoder.feed(result.data)
			for index := 0; index < len(pressedKeys); index++ {
				pressed := pressedKeys[index]
				if state.ready && pressed.kind == keyText && (pressed.text == "@" || ((pressed.text == "/" || pressed.text == ":") && state.editor.empty())) {
					query := ""
					for index+1 < len(pressedKeys) && pressedKeys[index+1].kind == keyText {
						index++
						query += pressedKeys[index].text
					}
					if pressed.text == "@" {
						err = u.pickFile(runCtx, &state, query)
					} else {
						err = u.pickCommand(runCtx, &state, pressed.text, query)
					}
					if err != nil {
						state.notice = err.Error()
					}
					index = len(pressedKeys)
					continue
				}
				quit, err := u.handleKey(runCtx, &state, pressed)
				if err != nil {
					state.notice = err.Error()
				}
				if quit {
					cancel()
					return context.Canceled
				}
			}
			if err := u.draw(&state); err != nil {
				return err
			}
			requestRead(readRequests)
		case <-resize:
			width, height, sizeErr := u.terminal.Size()
			if sizeErr == nil {
				state.width, state.height = width, height
				if err := u.draw(&state); err != nil {
					return err
				}
			}
		case <-ticker.C:
			for _, pressed := range decoder.flushPending(time.Now()) {
				quit, keyErr := u.handleKey(runCtx, &state, pressed)
				if keyErr != nil {
					state.notice = keyErr.Error()
				}
				if quit {
					cancel()
					return context.Canceled
				}
			}
			if state.isInferenceRunning {
				state.spinner++
				if err := u.draw(&state); err != nil {
					return err
				}
			}
		}
	}
}

var inputTips = []string{
	"Tip: @ searches project files",
	"Tip: / opens the command palette",
	"Tip: Tab switches [Build] and [Plan]",
	"Tip: Ctrl+N inserts a newline",
	"Tip: Esc cancels current work",
	"Tip: PgUp/PgDn, Ctrl+U/D, or the mouse wheel scroll the transcript",
}

func randomTip() string {
	return inputTips[rand.IntN(len(inputTips))]
}

func inputPump(ctx context.Context, reader *os.File, requests <-chan struct{}, results chan<- readResult, done chan<- struct{}) {
	defer close(done)
	buffer := make([]byte, 4096)
	for {
		select {
		case <-ctx.Done():
			return
		case <-requests:
		}
		for {
			if err := ctx.Err(); err != nil {
				return
			}
			ready, err := platformWaitReadable(reader.Fd(), 100*time.Millisecond)
			if err != nil {
				select {
				case results <- readResult{err: err}:
				case <-ctx.Done():
				}
				return
			}
			if ready {
				break
			}
		}
		count, err := reader.Read(buffer)
		data := append([]byte(nil), buffer[:count]...)
		select {
		case results <- readResult{data: data, err: err}:
		case <-ctx.Done():
			return
		}
		if err != nil {
			return
		}
	}
}

func requestRead(requests chan<- struct{}) {
	select {
	case requests <- struct{}{}:
	default:
	}
}

func (u *UI) draw(state *model) error {
	_, err := io.WriteString(u.out, state.render())
	return err
}

func (m *model) apply(event input.Event) {
	m.notice = ""
	switch event.Kind {
	case input.EventStatus:
		m.status = event.Text
	case input.EventInferenceStarted:
		m.isInferenceRunning = true
	case input.EventInferenceEnded:
		m.isInferenceRunning = false
	case input.EventToolStarted:
		kind, ok := displayedToolKind(event.ToolName)
		if !ok {
			break
		}
		block := transcriptBlock{
			kind: kind, turnID: event.TurnID, activity: event.ToolActivity,
		}
		m.blocks = append(m.blocks, block)
		if m.tools == nil {
			m.tools = make(map[toolKey]int)
		}
		m.tools[toolKey{turnID: event.TurnID, callID: event.ToolCallID}] = len(m.blocks) - 1
		m.scrollOffset = 0
	case input.EventToolCompleted:
		key := toolKey{turnID: event.TurnID, callID: event.ToolCallID}
		if index, ok := m.tools[key]; ok {
			m.blocks[index].activity = event.ToolActivity
			m.blocks[index].completed = true
			delete(m.tools, key)
			m.scrollOffset = 0
		}
	case input.EventOutput:
		kind := blockOutput
		if event.Stream == input.StreamStderr || strings.HasPrefix(strings.ToLower(event.Text), "error:") {
			kind = blockError
		}
		m.blocks = append(m.blocks, transcriptBlock{kind: kind, text: event.Text, turnID: event.TurnID})
		m.scrollOffset = 0
	case input.EventAssistantStarted:
		key := streamKey{turnID: event.TurnID, round: event.Round}
		m.blocks = append(m.blocks, transcriptBlock{kind: blockAssistant, turnID: event.TurnID, round: event.Round})
		m.streams[key] = len(m.blocks) - 1
		m.scrollOffset = 0
	case input.EventAssistantDelta:
		if index, ok := m.streams[streamKey{turnID: event.TurnID, round: event.Round}]; ok {
			m.blocks[index].text += event.Text
		}
		m.scrollOffset = 0
	case input.EventAssistantCompleted:
		key := streamKey{turnID: event.TurnID, round: event.Round}
		if index, ok := m.streams[key]; ok {
			m.blocks[index].text = event.Text
			m.blocks[index].interrupted = false
			delete(m.streams, key)
		}
	case input.EventAssistantAborted:
		key := streamKey{turnID: event.TurnID, round: event.Round}
		if index, ok := m.streams[key]; ok {
			if event.Text != "" {
				m.blocks[index].text = event.Text
			}
			m.blocks[index].interrupted = true
			delete(m.streams, key)
		}
	case input.EventProviderSelection:
		if event.Provider != "" {
			m.provider = event.Provider
		}
		if event.Model != "" {
			m.modelName = event.Model
		}
	case input.EventSessionReset:
		m.blocks = nil
		m.streams = make(map[streamKey]int)
		m.tools = make(map[toolKey]int)
		m.isInferenceRunning = false
		m.isLocalShellRunning = false
		m.promptMode = promptNormal
		m.status = ""
		m.notice = ""
		m.scrollOffset = 0
		m.editor.history = nil
		m.editor.historyIndex = 0
	}
}

func displayedToolKind(name string) (blockKind, bool) {
	switch name {
	case "read":
		return blockToolRead, true
	case "write":
		return blockToolWrite, true
	case "bash":
		return blockToolBash, true
	case "webfetch":
		return blockToolWebfetch, true
	default:
		return 0, false
	}
}

func (u *UI) handleKey(ctx context.Context, state *model, pressed key) (bool, error) {
	state.notice = ""
	switch pressed.kind {
	case keyCtrlC:
		return true, nil
	case keyEscape:
		if state.isLocalShellRunning {
			if u.localShellCancel != nil {
				u.localShellCancel()
			}
			state.notice = "cancelling command..."
			return false, nil
		}
		if state.ready {
			state.editor.reset()
			state.promptMode = promptNormal
			return false, nil
		}
		select {
		case u.interrupts <- struct{}{}:
		default:
		}
		state.notice = "cancelling current work..."
		return false, nil
	case keyFocusIn:
		state.focused = true
		return false, nil
	case keyFocusOut:
		state.focused = false
		return false, nil
	case keyPageUp:
		state.scrollOffset += max(1, state.height/2)
		return false, nil
	case keyPageDown:
		state.scrollOffset = max(0, state.scrollOffset-max(1, state.height/2))
		return false, nil
	case keyScrollUp:
		state.scrollOffset += 3
		return false, nil
	case keyScrollDown:
		state.scrollOffset = max(0, state.scrollOffset-3)
		return false, nil
	case keyTab:
		if state.promptMode == promptShell {
			state.editor.insert("\t")
			return false, nil
		}
		if state.mode == input.ModeBuild {
			state.mode = input.ModePlan
		} else {
			state.mode = input.ModeBuild
		}
		return false, nil
	}
	if !state.ready {
		return false, nil
	}
	switch pressed.kind {
	case keyEnter:
		text := state.editor.value()
		if strings.TrimSpace(text) == "" {
			return false, nil
		}
		if state.promptMode == promptShell {
			state.editor.remember("!" + strings.TrimSpace(text))
			return false, u.startLocalShell(ctx, state, text)
		}
		state.editor.remember(text)
		state.blocks = append(state.blocks, transcriptBlock{kind: blockUser, text: text, mode: state.mode})
		state.scrollOffset = 0
		state.ready = false
		state.status = "starting..."
		u.submissions <- input.Submission{Text: text, Mode: state.mode}
		state.editor.reset()
	case keyNewline:
		state.editor.insert("\n")
	case keyBackspace:
		state.editor.backspace()
	case keyDelete:
		state.editor.delete()
	case keyCtrlW:
		state.editor.deleteWord()
	case keyLeft:
		state.editor.moveLeft()
	case keyRight:
		state.editor.moveRight()
	case keyWordLeft:
		state.editor.moveWord(-1)
	case keyWordRight:
		state.editor.moveWord(1)
	case keyUp:
		state.editor.vertical(-1)
	case keyDown:
		state.editor.vertical(1)
	case keyHome:
		state.editor.home()
	case keyEnd:
		state.editor.end()
	case keyText:
		if pressed.text == "!" && state.promptMode == promptNormal && state.editor.empty() {
			state.promptMode = promptShell
			return false, nil
		}
		if state.promptMode == promptShell {
			state.editor.insert(pressed.text)
			return false, nil
		}
		if pressed.text == "@" {
			return false, u.pickFile(ctx, state, "")
		}
		if (pressed.text == "/" || pressed.text == ":") && state.editor.empty() {
			return false, u.pickCommand(ctx, state, pressed.text, "")
		}
		state.editor.insert(pressed.text)
	}
	return false, nil
}

func (u *UI) pickFile(ctx context.Context, state *model, query string) error {
	candidates, err := fileCandidates(ctx, state.workingDirectory)
	if err != nil {
		state.editor.insert("@")
		return err
	}
	selected, ok, err := u.runPicker(ctx, candidates, "Files > ", query, true)
	if err != nil {
		state.editor.insert("@")
		return err
	}
	if !ok {
		state.editor.insert("@")
		return nil
	}
	state.editor.insert("@" + filepath.ToSlash(selected) + " ")
	return nil
}

func (u *UI) pickCommand(ctx context.Context, state *model, prefix string, query string) error {
	selected, ok, err := u.runPicker(ctx, commandCandidates(u.options.Commands, prefix), "Commands > ", query, false)
	if err != nil {
		state.editor.insert(prefix)
		return err
	}
	if !ok {
		state.editor.insert(prefix)
		return nil
	}
	if selected == prefix+"model" {
		return u.pickModel(ctx, state, selected)
	}
	state.editor.set(selected + " ")
	return nil
}

func (u *UI) pickModel(ctx context.Context, state *model, commandText string) error {
	selected, ok, err := u.runPicker(ctx, modelCandidates(u.options.Models), "Models > ", "", false)
	if err != nil {
		state.editor.set(commandText + " ")
		return err
	}
	if !ok {
		state.editor.set(commandText + " ")
		return nil
	}
	state.editor.set(commandText + " " + selected)
	return nil
}

func (u *UI) runPicker(ctx context.Context, candidates []string, prompt string, query string, zeroDelimited bool) (selection string, selected bool, runErr error) {
	if err := u.terminal.Suspend(); err != nil {
		return "", false, err
	}
	defer func() { runErr = errors.Join(runErr, u.terminal.Resume()) }()
	return runFZF(ctx, u.fzf, candidates, prompt, query, zeroDelimited, u.err)
}

func (u *UI) startLocalShell(ctx context.Context, state *model, command string) error {
	command = strings.TrimSpace(command)
	if command == "" {
		return errors.New("command must not be empty")
	}
	if u.localShellResults == nil {
		u.localShellResults = make(chan localShellRunResult, 1)
	}
	commandCtx, cancel := context.WithCancel(ctx)
	u.localShellCancel = cancel
	state.ready = false
	state.isLocalShellRunning = true
	state.status = "running command: " + command
	state.editor.reset()
	go func() {
		result, err := runLocalShellCommand(commandCtx, command, state.workingDirectory)
		select {
		case u.localShellResults <- localShellRunResult{result: result, err: err}:
		case <-ctx.Done():
		}
	}()
	return nil
}

func (m *model) applyLocalShellResult(run localShellRunResult) {
	m.ready = true
	m.isLocalShellRunning = false
	m.status = ""
	m.promptMode = promptNormal
	m.scrollOffset = 0
	if run.err != nil {
		m.notice = run.err.Error()
		return
	}
	blockText := formatLocalShellForBlock(run.result)
	m.blocks = append(m.blocks, transcriptBlock{kind: blockShell, text: blockText})
	m.editor.set(formatLocalShellForMessage(run.result))
}
