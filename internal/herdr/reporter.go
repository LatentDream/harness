package herdr

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"sync"

	"latentdream/harness/internal/input"
)

const (
	reportSource = "custom:harness"
	agentName    = "harness"
)

type commandRunner func(context.Context, string, ...string) error

type reporterConfig struct {
	herdrBin        string
	herdrPaneID     string
	tmuxStateScript string
	tmuxPaneID      string
}

// Reporter forwards runtime events and mirrors turn-level lifecycle state to
// Herdr and tmux when their respective environments are available.
type Reporter struct {
	next      input.Sink
	config    reporterConfig
	run       commandRunner
	reports   chan report
	done      chan struct{}
	wait      sync.WaitGroup
	once      sync.Once
	sessionID string
	seq       uint64
	mu        sync.Mutex
}

type report struct {
	state     string
	sessionID string
}

func Wrap(next input.Sink) *Reporter {
	config := reporterConfig{}
	if os.Getenv("HERDR_ENV") == "1" {
		config.herdrBin = os.Getenv("HERDR_BIN_PATH")
		config.herdrPaneID = os.Getenv("HERDR_PANE_ID")
	}
	if os.Getenv("TMUX") != "" && os.Getenv("TMUX_PANE") != "" {
		pluginDir := os.Getenv("TMUX_AGENT_INDICATOR_DIR")
		if pluginDir == "" {
			if home, err := os.UserHomeDir(); err == nil {
				pluginDir = filepath.Join(home, ".tmux", "plugins", "tmux-agent-indicator")
			}
		}
		if pluginDir != "" {
			stateScript := filepath.Join(pluginDir, "scripts", "agent-state.sh")
			if info, err := os.Stat(stateScript); err == nil && !info.IsDir() {
				config.tmuxStateScript = stateScript
				config.tmuxPaneID = os.Getenv("TMUX_PANE")
			}
		}
	}
	return newReporter(next, config, runCommand)
}

func newReporter(next input.Sink, config reporterConfig, run commandRunner) *Reporter {
	r := &Reporter{next: next, config: config, run: run}
	if !r.herdrEnabled() && !r.tmuxEnabled() {
		return r
	}
	// A tool-free turn may settle immediately; retain both edges so consumers
	// never observe completion without first observing working.
	r.reports = make(chan report, 16)
	r.done = make(chan struct{})
	r.wait.Add(1)
	go r.runReports()
	if r.herdrEnabled() {
		r.queue(report{state: "idle"})
	}
	return r
}

func (r *Reporter) Emit(ctx context.Context, event input.Event) error {
	if err := r.next.Emit(ctx, event); err != nil {
		return err
	}

	if event.SessionID != "" {
		r.mu.Lock()
		r.sessionID = event.SessionID
		r.mu.Unlock()
	}
	switch event.Kind {
	case input.EventSessionLoaded, input.EventSessionReset:
		if r.herdrEnabled() {
			r.queue(report{state: "idle", sessionID: event.SessionID})
		}
	}
	return nil
}

func (r *Reporter) TurnStarted(sessionID string) {
	r.queue(report{state: "working", sessionID: sessionID})
}

func (r *Reporter) TurnCompleted(sessionID string) {
	r.queue(report{state: "idle", sessionID: sessionID})
}

func (r *Reporter) Close() {
	if r.reports == nil {
		return
	}
	r.once.Do(func() {
		close(r.done)
		r.wait.Wait()
		if r.herdrEnabled() {
			seq := r.nextSequence()
			_ = r.run(context.Background(), r.config.herdrBin, "pane", "release-agent", r.config.herdrPaneID,
				"--source", reportSource, "--agent", agentName, "--seq", formatSequence(seq))
		}
		if r.tmuxEnabled() {
			_ = r.run(context.Background(), r.config.tmuxStateScript, "--agent", agentName,
				"--state", "off", "--pane", r.config.tmuxPaneID)
		}
	})
}

func (r *Reporter) herdrEnabled() bool {
	return r.config.herdrBin != "" && r.config.herdrPaneID != ""
}

func (r *Reporter) tmuxEnabled() bool {
	return r.config.tmuxStateScript != "" && r.config.tmuxPaneID != ""
}

func (r *Reporter) queue(value report) {
	if r.reports == nil {
		return
	}
	if value.sessionID == "" {
		r.mu.Lock()
		value.sessionID = r.sessionID
		r.mu.Unlock()
	}
	select {
	case r.reports <- value:
	default:
		select {
		case <-r.reports:
		default:
		}
		select {
		case r.reports <- value:
		default:
		}
	}
}

func (r *Reporter) runReports() {
	defer r.wait.Done()
	for {
		select {
		case value := <-r.reports:
			r.send(value)
		case <-r.done:
			for {
				select {
				case value := <-r.reports:
					r.send(value)
				default:
					return
				}
			}
		}
	}
}

func (r *Reporter) send(value report) {
	if r.herdrEnabled() {
		seq := r.nextSequence()
		args := []string{"pane", "report-agent", r.config.herdrPaneID,
			"--source", reportSource, "--agent", agentName,
			"--state", value.state, "--seq", formatSequence(seq)}
		if value.sessionID != "" {
			args = append(args, "--agent-session-id", value.sessionID)
		}
		_ = r.run(context.Background(), r.config.herdrBin, args...)
	}
	if r.tmuxEnabled() {
		state := "done"
		if value.state == "working" {
			state = "running"
		}
		_ = r.run(context.Background(), r.config.tmuxStateScript, "--agent", agentName,
			"--state", state, "--pane", r.config.tmuxPaneID)
	}
}

func (r *Reporter) nextSequence() uint64 {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.seq++
	return r.seq
}

func runCommand(ctx context.Context, name string, args ...string) error {
	return exec.CommandContext(ctx, name, args...).Run()
}

func formatSequence(seq uint64) string {
	const digits = "0123456789"
	if seq == 0 {
		return "0"
	}
	var buf [20]byte
	i := len(buf)
	for seq > 0 {
		i--
		buf[i] = digits[seq%10]
		seq /= 10
	}
	return string(buf[i:])
}
