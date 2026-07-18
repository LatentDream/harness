package input

import "context"

type IO interface {
	Receive(context.Context) (string, error)
	SetStatus(string) error
	SetStatusf(string, ...any) error
	Write(string) error
	Writef(string, ...any) error
	WriteErr(string) error
	WriteErrf(string, ...any) error
}
