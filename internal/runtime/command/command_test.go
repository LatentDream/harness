package command

import (
	"context"
	"errors"
	"reflect"
	"testing"
)

func TestRegistryExecutesMappedCommand(t *testing.T) {
	testCommand := &recordingCommand{}
	registry := NewRegistry(testCommand)

	result, handled, err := registry.Execute(context.Background(), "  /test one two  ")
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if !handled {
		t.Fatal("expected command to be handled")
	}
	if result.Output != "ok" {
		t.Fatalf("expected output ok, got %q", result.Output)
	}
	if !reflect.DeepEqual(testCommand.args, []string{"one", "two"}) {
		t.Fatalf("expected args one two, got %#v", testCommand.args)
	}
}

func TestRegistryIgnoresRegularInput(t *testing.T) {
	result, handled, err := DefaultRegistry().Execute(context.Background(), "hello there")
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if handled {
		t.Fatalf("expected input not to be handled, got %#v", result)
	}
}

func TestRegistryHandlesUnknownCommand(t *testing.T) {
	result, handled, err := DefaultRegistry().Execute(context.Background(), "/missing arg")
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if !handled {
		t.Fatal("expected unknown command to be handled")
	}
	if result.Output != "unknown command: /missing" {
		t.Fatalf("expected unknown command output, got %q", result.Output)
	}
}

func TestRegistryExecutesDefaultExitCommand(t *testing.T) {
	for _, input := range []string{":q", "/exit", "/quit"} {
		result, handled, err := DefaultRegistry().Execute(context.Background(), input)
		if err != nil {
			t.Fatalf("expected no error for %q, got %v", input, err)
		}
		if !handled {
			t.Fatalf("expected %q to be handled", input)
		}
		if result.Action != ActionExit {
			t.Fatalf("expected %q to exit, got action %d", input, result.Action)
		}
	}
}

func TestRegistryExecutesDefaultHelpCommand(t *testing.T) {
	for _, input := range []string{":help", "/help"} {
		result, handled, err := DefaultRegistry().Execute(context.Background(), input)
		if err != nil {
			t.Fatalf("expected no error for %q, got %v", input, err)
		}
		if !handled {
			t.Fatalf("expected %q to be handled", input)
		}
		expectedOutput := "Available commands:\n  :q, /exit, /quit - exit harness\n  :help, /help - show available commands"
		if result.Output != expectedOutput {
			t.Fatalf("expected help output %q, got %q", expectedOutput, result.Output)
		}
	}
}

func TestRegistryEntriesAreSortedWithAliasesAndDescriptions(t *testing.T) {
	registry := NewRegistry(
		&describedCommand{name: "zebra", mappings: []string{" /zebra ", ":z"}, description: " striped command "},
		&describedCommand{name: "alpha", mappings: []string{"/alpha", ":a"}, description: "first command"},
	)

	expected := []Entry{
		{Name: "alpha", Mappings: []string{"/alpha", ":a"}, Description: "first command"},
		{Name: "zebra", Mappings: []string{"/zebra", ":z"}, Description: "striped command"},
	}
	if entries := registry.Entries(); !reflect.DeepEqual(entries, expected) {
		t.Fatalf("expected entries %#v, got %#v", expected, entries)
	}
}

func TestRegistryEntriesReturnsCopies(t *testing.T) {
	command := &describedCommand{name: "test", mappings: []string{"/test", ":t"}, description: "test command"}
	registry := NewRegistry(command)

	entries := registry.Entries()
	entries[0].Name = "changed"
	entries[0].Mappings[0] = "/changed"
	entries[0].Description = "changed"

	expected := []Entry{{Name: "test", Mappings: []string{"/test", ":t"}, Description: "test command"}}
	if actual := registry.Entries(); !reflect.DeepEqual(actual, expected) {
		t.Fatalf("expected entries %#v after mutation, got %#v", expected, actual)
	}
}

func TestRegistryReturnsCommandErrors(t *testing.T) {
	expectedErr := errors.New("boom")
	registry := NewRegistry(&recordingCommand{err: expectedErr})

	_, handled, err := registry.Execute(context.Background(), "/test")
	if !handled {
		t.Fatal("expected command to be handled")
	}
	if !errors.Is(err, expectedErr) {
		t.Fatalf("expected error %v, got %v", expectedErr, err)
	}
}

type recordingCommand struct {
	args []string
	err  error
}

func (cmd *recordingCommand) Name() string {
	return "test"
}

func (cmd *recordingCommand) Mapping() []string {
	return []string{"/test"}
}

func (cmd *recordingCommand) Execute(_ context.Context, args []string) (Result, error) {
	cmd.args = args
	return Result{Output: "ok"}, cmd.err
}

type describedCommand struct {
	name        string
	mappings    []string
	description string
}

func (cmd *describedCommand) Name() string {
	return cmd.name
}

func (cmd *describedCommand) Mapping() []string {
	return append([]string(nil), cmd.mappings...)
}

func (cmd *describedCommand) Description() string {
	return cmd.description
}

func (cmd *describedCommand) Execute(context.Context, []string) (Result, error) {
	return Result{}, nil
}
