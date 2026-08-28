package tool

import (
	"latentdream/harness/internal/session/llm"
	"latentdream/harness/internal/tool/bash"
	"latentdream/harness/internal/tool/edit"
	"latentdream/harness/internal/tool/glob"
	"latentdream/harness/internal/tool/grep"
	"latentdream/harness/internal/tool/model"
	"latentdream/harness/internal/tool/read"
	"latentdream/harness/internal/tool/webfetch"
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

func WithCapability(tools []model.Tool, capability model.Capability) []model.Tool {
	filtered := make([]model.Tool, 0, len(tools))
	for _, item := range tools {
		if item != nil && item.Capability() == capability {
			filtered = append(filtered, item)
		}
	}
	return filtered
}

func NewDefault() []model.Tool {
	return []model.Tool{read.New(), write.New(), edit.New(), glob.New(), grep.New(), webfetch.New(), bash.New()}
}

func NewChatDefault() []model.Tool {
	return []model.Tool{webfetch.New()}
}
