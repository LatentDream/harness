package tool

import (
	"latentdream/harness/internal/session/llm"
	"latentdream/harness/internal/tool/glob"
	"latentdream/harness/internal/tool/grep"
	"latentdream/harness/internal/tool/model"
	"latentdream/harness/internal/tool/read"
	"latentdream/harness/internal/tool/write"
)

func Definitions(tools []model.Tool) []llm.ToolDefinition {
	definitions := make([]llm.ToolDefinition, 0, len(tools))
	for _, item := range tools {
		if item == nil {
			continue
		}
		definitions = append(definitions, item.Definition())
	}
	return definitions
}

func ByName(tools []model.Tool) map[string]model.Tool {
	byName := make(map[string]model.Tool, len(tools))
	for _, item := range tools {
		if item == nil {
			continue
		}
		definition := item.Definition()
		if definition.Name != "" {
			byName[definition.Name] = item
		}
	}
	return byName
}

func NewDefault() []model.Tool {
	return []model.Tool{read.New(), write.New(), glob.New(), grep.New()}
}
