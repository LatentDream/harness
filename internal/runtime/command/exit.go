package command

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

func (cmd *exit) Execute(args []string) (Result, error) {
	return Result{Action: ActionExit}, nil
}
