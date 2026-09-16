package input

import (
	"context"
	"errors"
	"strings"
	"sync"
)

var (
	ErrNoActiveTurn       = errors.New("no active turn accepts guidance")
	ErrEmptySteeringInput = errors.New("guidance must not be empty")
)

// SteeringSender accepts guidance for the currently executing turn.
// Accepted guidance is applied at the next safe model-request boundary.
type SteeringSender interface {
	SendMessage(context.Context, string) error
}

// SteeringInbox is a concurrency-safe, FIFO mailbox for guidance sent while a
// turn is executing. Conversation state remains owned by the runtime goroutine;
// callers only enqueue text here.
type SteeringInbox struct {
	mu      sync.Mutex
	active  bool
	pending []string
}

func NewSteeringInbox() *SteeringInbox {
	return &SteeringInbox{}
}

// BeginTurn opens the inbox for one turn.
func (i *SteeringInbox) BeginTurn() error {
	if i == nil {
		return errors.New("steering inbox is nil")
	}
	i.mu.Lock()
	defer i.mu.Unlock()
	if i.active {
		return errors.New("steering inbox already has an active turn")
	}
	i.active = true
	i.pending = nil
	return nil
}

func (i *SteeringInbox) SendMessage(ctx context.Context, text string) error {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if strings.TrimSpace(text) == "" {
		return ErrEmptySteeringInput
	}
	if i == nil {
		return ErrNoActiveTurn
	}

	i.mu.Lock()
	defer i.mu.Unlock()
	if !i.active {
		return ErrNoActiveTurn
	}
	i.pending = append(i.pending, text)
	return nil
}

// Drain returns all currently queued guidance without closing the turn.
func (i *SteeringInbox) Drain() []string {
	if i == nil {
		return nil
	}
	i.mu.Lock()
	defer i.mu.Unlock()
	result := append([]string(nil), i.pending...)
	i.pending = nil
	return result
}

// DrainOrClose atomically drains queued guidance, or closes an empty inbox.
// The boolean is true only when the turn was closed.
func (i *SteeringInbox) DrainOrClose() ([]string, bool) {
	if i == nil {
		return nil, true
	}
	i.mu.Lock()
	defer i.mu.Unlock()
	if len(i.pending) != 0 {
		result := append([]string(nil), i.pending...)
		i.pending = nil
		return result, false
	}
	i.active = false
	return nil, true
}

// EndTurn atomically closes the inbox and returns guidance not yet consumed.
// Callers can still trace these accepted messages when a turn fails.
func (i *SteeringInbox) EndTurn() []string {
	if i == nil {
		return nil
	}
	i.mu.Lock()
	defer i.mu.Unlock()
	pending := append([]string(nil), i.pending...)
	i.active = false
	i.pending = nil
	return pending
}
