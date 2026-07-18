package command

import (
	"fmt"
	"strings"
)

type Action int

const (
	ActionContinue Action = iota
	ActionExit
)

type Result struct {
	Action Action
	Output string
}

type Command interface {
	Name() string
	Mapping() []string
	Execute(args []string) (Result, error)
}

type Registry struct {
	commands map[string]Command
}

func DefaultRegistry() *Registry {
	return NewRegistry(newExitCmd())
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

func (r *Registry) Execute(input string) (Result, bool, error) {
	name, args := parse(input)
	if name == "" {
		return Result{}, false, nil
	}

	cmd := r.IsCommand(name)
	if cmd == nil {
		if isCommandToken(name) {
			return Result{Output: fmt.Sprintf("unknown command: %s", name)}, true, nil
		}
		return Result{}, false, nil
	}

	result, err := cmd.Execute(args)
	return result, true, err
}

func (r *Registry) IsCommand(input string) Command {
	name, _ := parse(input)
	if name == "" || r == nil {
		return nil
	}
	return r.commands[normalize(name)]
}

func parse(input string) (string, []string) {
	parts := strings.Fields(strings.TrimSpace(input))
	if len(parts) == 0 {
		return "", nil
	}
	return parts[0], parts[1:]
}

func normalize(name string) string {
	return strings.ToLower(strings.TrimSpace(name))
}

func isCommandToken(name string) bool {
	return strings.HasPrefix(name, "/") || strings.HasPrefix(name, ":")
}
