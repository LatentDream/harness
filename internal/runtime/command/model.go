package command

import (
	"context"
	"fmt"
	"strings"

	"latentdream/harness/internal/provider"
)

const modelUsage = "usage: /model <provider>/<model>"

type ModelProvider interface {
	Current() provider.Selection
	Available() []provider.Selection
	Use(providerName string, modelName string) error
}

type modelCommand struct {
	provider ModelProvider
}

func NewModelCmd(provider ModelProvider) Command {
	return &modelCommand{provider: provider}
}

func (cmd *modelCommand) Name() string {
	return "model"
}

func (cmd *modelCommand) Mapping() []string {
	return []string{":model", "/model"}
}

func (cmd *modelCommand) Description() string {
	return "switch provider/model"
}

func (cmd *modelCommand) Execute(ctx context.Context, args []string) (Result, error) {
	return cmd.ExecuteRaw(ctx, strings.Join(args, " "))
}

func (cmd *modelCommand) ExecuteRaw(_ context.Context, args string) (Result, error) {
	if cmd.provider == nil {
		return Result{Output: "error: provider is not configured"}, nil
	}
	args = strings.TrimSpace(args)
	if args == "" {
		return Result{Output: cmd.listModels()}, nil
	}

	providerName, modelName, ok := parseModelSelection(args)
	if !ok {
		return Result{Output: modelUsage}, nil
	}
	if err := cmd.provider.Use(providerName, modelName); err != nil {
		return Result{Output: "error: " + err.Error()}, nil
	}
	return Result{
		Output:   fmt.Sprintf("switched model to %s/%s", providerName, modelName),
		Provider: providerName,
		Model:    modelName,
	}, nil
}

func (cmd *modelCommand) listModels() string {
	available := cmd.provider.Available()
	if len(available) == 0 {
		return "no models are configured"
	}
	current := cmd.provider.Current()

	var output strings.Builder
	output.WriteString("Available models:")
	for _, selection := range available {
		marker := " "
		if selection == current {
			marker = "*"
		}
		output.WriteString("\n  ")
		output.WriteString(marker)
		output.WriteString(" ")
		output.WriteString(selection.Provider)
		output.WriteString("/")
		output.WriteString(selection.Model)
	}
	output.WriteString("\nUsage: /model <provider>/<model>")
	return output.String()
}

func parseModelSelection(input string) (string, string, bool) {
	providerName, modelName, ok := strings.Cut(strings.TrimSpace(input), "/")
	if !ok {
		return "", "", false
	}
	providerName = strings.TrimSpace(providerName)
	modelName = strings.TrimSpace(modelName)
	if providerName == "" || modelName == "" || strings.Contains(modelName, "/") {
		return "", "", false
	}
	return providerName, modelName, true
}
