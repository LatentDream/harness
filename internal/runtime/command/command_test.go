package command

import (
	"errors"
	"reflect"
	"testing"
)

func TestRegistryExecutesMappedCommand(t *testing.T) {
	testCommand := &recordingCommand{}
	registry := NewRegistry(testCommand)

	result, handled, err := registry.Execute("  /test one two  ")
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
	result, handled, err := DefaultRegistry().Execute("hello there")
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if handled {
		t.Fatalf("expected input not to be handled, got %#v", result)
	}
}

func TestRegistryHandlesUnknownCommand(t *testing.T) {
	result, handled, err := DefaultRegistry().Execute("/missing arg")
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
		result, handled, err := DefaultRegistry().Execute(input)
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

func TestRegistryReturnsCommandErrors(t *testing.T) {
	expectedErr := errors.New("boom")
	registry := NewRegistry(&recordingCommand{err: expectedErr})

	_, handled, err := registry.Execute("/test")
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

func (cmd *recordingCommand) Execute(args []string) (Result, error) {
	cmd.args = args
	return Result{Output: "ok"}, cmd.err
}
