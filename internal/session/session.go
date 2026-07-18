package session

import "latentdream/harness/internal/llm"

type Session struct {
	SystemPrompt string
	Conversation []llm.Message
}
