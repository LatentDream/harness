package git

type Repository interface {
	// Status() (Status, error)
	// Diff() (string, error)
	Commit(message string) error
}
