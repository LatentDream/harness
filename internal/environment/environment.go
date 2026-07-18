package environment

import (
	"latentdream/harness/internal/environment/filesystem"
	"latentdream/harness/internal/environment/git"
)

type Environment interface {
	FS() filesystem.FS
	Git() git.Repository
	// Progress() progress.Store
}
