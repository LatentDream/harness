package session

import (
	_ "embed"

	"latentdream/harness/internal/llm"
)

type Session struct {
	Conversation []llm.Message
}

//go:embed prompt/simple.md
var simpleSystemPrompt string

func (s *Session) BuildSystemPrompt() string {
	return simpleSystemPrompt
}
