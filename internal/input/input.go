package input

type Input interface {
	Receive() (string, error)
	Write(string) error
	Writef(string, ...any) error
	WriteErr(string) error
	WriteErrf(string, ...any) error
}
