package command

import "context"

type exit struct{}

func newExitCmd() Command {
	return &exit{}
}

func (cmd *exit) Name() string {
	return "exit"
}

func (cmd *exit) Mapping() []string {
	return []string{":q", "/exit", "/quit"}
}

func (cmd *exit) Description() string {
	return "exit harness"
}

func (cmd *exit) Execute(_ context.Context, args []string) (Result, error) {
	return Result{Action: ActionExit}, nil
}
