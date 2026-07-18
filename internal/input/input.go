package input

type Input interface {
	Receive() string
	Response(string)
}
