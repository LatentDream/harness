package session

import (
	_ "embed"
	"os"
	"path/filepath"
	"strings"

	"latentdream/harness/internal/session/llm"
)

var projectContextFiles = []string{"AGENTS.md", "AGENT.md", "CLAUDE.md"}

const PlanModeInstruction = "You are in Plan mode. Investigate using only read-only tools. Do not modify files or execute mutating actions. Return a clear implementation plan instead of making changes."

const ChatModeInstruction = "You are in Chat mode. Answer conversationally. Do not modify the workspace. Do not claim to inspect files, run commands, or use tools unless a tool is explicitly available in this request."

type Session struct {
	Conversation []llm.Message
}

//go:embed prompt/simple.md
var simpleSystemPrompt string

func (s *Session) BuildSystemPrompt() string {
	prompt := strings.TrimRight(simpleSystemPrompt, "\n")

	cwd, err := os.Getwd()
	if err != nil {
		return prompt
	}

	if projectContext := readProjectContext(cwd); projectContext != "" {
		prompt += "\n\nProject Context:\n" + projectContext
	}
	prompt += "\n\nCurrent working directory:\n" + cwd

	return prompt
}

func (s *Session) Init() {
	if len(s.Conversation) == 0 {
		s.Conversation = append(
			s.Conversation,
			llm.Message{Role: llm.RoleSystem, Content: s.BuildSystemPrompt()},
		)
	}
}

func readProjectContext(cwd string) string {
	sections := make([]string, 0, len(projectContextFiles))
	for _, filename := range projectContextFiles {
		contents, err := os.ReadFile(filepath.Join(cwd, filename))
		if err != nil {
			continue
		}

		text := strings.TrimSpace(strings.ReplaceAll(string(contents), "\r\n", "\n"))
		if text == "" {
			continue
		}

		sections = append(sections, "## "+filename+"\n"+text)
	}

	return strings.Join(sections, "\n\n")
}
