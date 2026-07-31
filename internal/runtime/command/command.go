package command

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"sync"
	"unicode"
)

type Action int

const (
	ActionContinue Action = iota
	ActionExit
	ActionNewSession
	ActionSwitchSession
)

type Result struct {
	Action    Action
	Output    string
	Provider  string
	Model     string
	SessionID string
}

type Command interface {
	Name() string
	Mapping() []string
	Execute(context.Context, []string) (Result, error)
}

// Entry describes a command and the mappings that invoke it.
type Entry struct {
	Name        string
	Mappings    []string
	Description string
}

type Registry struct {
	mu       sync.RWMutex
	commands map[string]Command
}

func DefaultRegistry() *Registry {
	r := NewRegistry(newExitCmd(), newNewCmd())
	r.Register(newHelpCmd(r))
	return r
}

func NewRegistry(commands ...Command) *Registry {
	r := &Registry{commands: make(map[string]Command)}
	for _, cmd := range commands {
		r.Register(cmd)
	}
	return r
}

func (r *Registry) Register(cmd Command) {
	if cmd == nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.commands == nil {
		r.commands = make(map[string]Command)
	}

	for _, name := range cmd.Mapping() {
		name = normalize(name)
		if name == "" {
			continue
		}
		r.commands[name] = cmd
	}
}

func (r *Registry) Execute(ctx context.Context, input string) (Result, bool, error) {
	name, rawArgs := parseRaw(input)
	if name == "" {
		return Result{}, false, nil
	}
	args := strings.Fields(rawArgs)

	cmd := r.IsCommand(name)
	if cmd == nil {
		if isCommandToken(name) {
			return Result{Output: fmt.Sprintf("unknown command: %s", name)}, true, nil
		}
		return Result{}, false, nil
	}

	if raw, ok := cmd.(interface {
		ExecuteRaw(context.Context, string) (Result, error)
	}); ok {
		result, err := raw.ExecuteRaw(ctx, rawArgs)
		return result, true, err
	}

	result, err := cmd.Execute(ctx, args)
	return result, true, err
}

func (r *Registry) IsCommand(input string) Command {
	name, _ := parse(input)
	if name == "" || r == nil {
		return nil
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.commands[normalize(name)]
}

// Entries returns command descriptions sorted by command name.
func (r *Registry) Entries() []Entry {
	if r == nil {
		return nil
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	if len(r.commands) == 0 {
		return nil
	}

	commands := make(map[string]Entry)
	for _, cmd := range r.commands {
		if cmd == nil {
			continue
		}

		name := normalize(cmd.Name())
		if name == "" {
			continue
		}

		entry := Entry{Name: name, Mappings: cleanMappings(cmd.Mapping())}
		if described, ok := cmd.(interface{ Description() string }); ok {
			entry.Description = strings.TrimSpace(described.Description())
		}
		commands[name] = entry
	}

	if len(commands) == 0 {
		return nil
	}

	names := make([]string, 0, len(commands))
	for name := range commands {
		names = append(names, name)
	}
	sort.Strings(names)

	entries := make([]Entry, 0, len(names))
	for _, name := range names {
		entry := commands[name]
		entry.Mappings = append([]string(nil), entry.Mappings...)
		entries = append(entries, entry)
	}
	return entries
}

func (r *Registry) Help() string {
	entries := r.Entries()
	if len(entries) == 0 {
		return "Available commands:\n  none"
	}

	var output strings.Builder
	output.WriteString("Available commands:")
	for _, entry := range entries {
		output.WriteString("\n  ")
		output.WriteString(strings.Join(entry.Mappings, ", "))
		if entry.Description != "" {
			output.WriteString(" - ")
			output.WriteString(entry.Description)
		}
	}
	return output.String()
}

func parse(input string) (string, []string) {
	name, rawArgs := parseRaw(input)
	if name == "" {
		return "", nil
	}
	return name, strings.Fields(rawArgs)
}

func parseRaw(input string) (string, string) {
	input = strings.TrimSpace(input)
	if input == "" {
		return "", ""
	}
	index := strings.IndexFunc(input, unicode.IsSpace)
	if index < 0 {
		return input, ""
	}
	return input[:index], strings.TrimSpace(input[index:])
}

func normalize(name string) string {
	return strings.ToLower(strings.TrimSpace(name))
}

func cleanMappings(mappings []string) []string {
	cleaned := make([]string, 0, len(mappings))
	for _, mapping := range mappings {
		mapping = strings.TrimSpace(mapping)
		if mapping != "" {
			cleaned = append(cleaned, mapping)
		}
	}
	return cleaned
}

func isCommandToken(name string) bool {
	return strings.HasPrefix(name, "/") || strings.HasPrefix(name, ":")
}
