package session

import (
	_ "embed"
	"os"
	"path/filepath"
	"strings"

	"latentdream/harness/internal/session/llm"
)

var projectContextFiles = []string{"AGENTS.md", "AGENT.md", "CLAUDE.md"}

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
