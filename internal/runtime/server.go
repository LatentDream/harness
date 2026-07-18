package app

import (
	"github.com/google/uuid"
	"latentdream/harness/internal/environment"
	"latentdream/harness/internal/input"
	"latentdream/harness/internal/orchestrator"
	"latentdream/harness/internal/provider"
	"latentdream/harness/internal/session"
	"latentdream/harness/internal/tool"
)

type Runtime struct {
	id uuid.UUID

	Provider    provider.Provider
	Tools       []tool.Tool
	Controller  orchestrator.Orchestrator
	Environment environment.Environment
	State       session.Session

	input input.Input
}

func (r *Runtime) loop() {
	// Receive input
}
