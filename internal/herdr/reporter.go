package herdr

import (
	"context"
	"os"
	"os/exec"
	"sync"

	"latentdream/harness/internal/input"
)

const (
	reportSource = "custom:harness"
	agentName    = "harness"
)

type commandRunner func(context.Context, string, ...string) error

// Reporter forwards runtime events to the frontend and reports lifecycle state
// when Harness is running inside a Herdr pane.
type Reporter struct {
	next    input.Sink
	bin     string
	paneID  string
	run     commandRunner
	reports chan report
	done    chan struct{}
	once    sync.Once
}

type report struct {
	state     string
	sessionID string
}

func Wrap(next input.Sink) *Reporter {
	bin, paneID := "", ""
	if os.Getenv("HERDR_ENV") == "1" {
		bin = os.Getenv("HERDR_BIN_PATH")
		paneID = os.Getenv("HERDR_PANE_ID")
	}
	return newReporter(next, bin, paneID, runCommand)
}

func newReporter(next input.Sink, bin, paneID string, run commandRunner) *Reporter {
	r := &Reporter{next: next, bin: bin, paneID: paneID, run: run}
	if bin == "" || paneID == "" {
		return r
	}
	r.reports = make(chan report, 1)
	r.done = make(chan struct{})
	go r.runReports()
	r.queue(report{state: "idle"})
	return r
}

func (r *Reporter) Emit(ctx context.Context, event input.Event) error {
	if err := r.next.Emit(ctx, event); err != nil {
		return err
	}

	switch event.Kind {
	case input.EventInferenceStarted, input.EventToolStarted:
		r.queue(report{state: "working", sessionID: event.SessionID})
	case input.EventAssistantCompleted, input.EventAssistantAborted:
		r.queue(report{state: "idle", sessionID: event.SessionID})
	case input.EventSessionLoaded, input.EventSessionReset:
		r.queue(report{state: "idle", sessionID: event.SessionID})
	}
	return nil
}

func (r *Reporter) Close() {
	if r.reports == nil {
		return
	}
	r.once.Do(func() {
		close(r.done)
		_ = r.run(context.Background(), r.bin, "pane", "release-agent", r.paneID,
			"--source", reportSource, "--agent", agentName)
	})
}

func (r *Reporter) queue(value report) {
	if r.reports == nil {
		return
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
	var seq uint64
	for {
		select {
		case value := <-r.reports:
			seq++
			args := []string{"pane", "report-agent", r.paneID,
				"--source", reportSource, "--agent", agentName,
				"--state", value.state, "--seq", formatSequence(seq)}
			if value.sessionID != "" {
				args = append(args, "--agent-session-id", value.sessionID)
			}
			_ = r.run(context.Background(), r.bin, args...)
		case <-r.done:
			return
		}
	}
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
