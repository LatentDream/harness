package command

import "context"

type newCommand struct{}

func newNewCmd() Command {
	return &newCommand{}
}

func (cmd *newCommand) Name() string {
	return "new"
}

func (cmd *newCommand) Mapping() []string {
	return []string{":new", "/new"}
}

func (cmd *newCommand) Description() string {
	return "start a new session"
}

func (cmd *newCommand) Execute(_ context.Context, args []string) (Result, error) {
	if len(args) > 0 {
		return Result{Output: "usage: /new"}, nil
	}
	return Result{Action: ActionNewSession}, nil
}
