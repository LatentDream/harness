package command

type help struct {
	registry *Registry
}

func newHelpCmd(registry *Registry) Command {
	return &help{registry: registry}
}

func (cmd *help) Name() string {
	return "help"
}

func (cmd *help) Mapping() []string {
	return []string{":help", "/help"}
}

func (cmd *help) Description() string {
	return "show available commands"
}

func (cmd *help) Execute(args []string) (Result, error) {
	return Result{Output: cmd.registry.Help()}, nil
}
