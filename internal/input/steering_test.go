package input

import (
	"context"
	"errors"
	"reflect"
	"testing"
)

func TestSteeringInboxPreservesFIFOAndClosesAtomically(t *testing.T) {
	inbox := NewSteeringInbox()
	if err := inbox.BeginTurn(); err != nil {
		t.Fatal(err)
	}
	for _, text := range []string{"first", "second"} {
		if err := inbox.SendMessage(context.Background(), text); err != nil {
			t.Fatal(err)
		}
	}
	messages, closed := inbox.DrainOrClose()
	if closed || !reflect.DeepEqual(messages, []string{"first", "second"}) {
		t.Fatalf("DrainOrClose = %#v, %v", messages, closed)
	}
	messages, closed = inbox.DrainOrClose()
	if !closed || len(messages) != 0 {
		t.Fatalf("empty DrainOrClose = %#v, %v", messages, closed)
	}
	if err := inbox.SendMessage(context.Background(), "late"); !errors.Is(err, ErrNoActiveTurn) {
		t.Fatalf("late send error = %v", err)
	}
}

func TestSteeringInboxEndTurnReturnsPendingGuidance(t *testing.T) {
	inbox := NewSteeringInbox()
	if err := inbox.BeginTurn(); err != nil {
		t.Fatal(err)
	}
	if err := inbox.SendMessage(context.Background(), "pending"); err != nil {
		t.Fatal(err)
	}
	if got := inbox.EndTurn(); !reflect.DeepEqual(got, []string{"pending"}) {
		t.Fatalf("EndTurn = %#v", got)
	}
}
