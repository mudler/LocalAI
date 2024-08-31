package model

import (
	"fmt"

	"github.com/mudler/LocalAI/pkg/functions"
	"github.com/mudler/LocalAI/pkg/templates"
)

// Rather than pass an interface{} to the prompt template:
// These are the definitions of all possible variables LocalAI will currently populate for use in a prompt template file
// Please note: Not all of these are populated on every endpoint - your template should either be tested for each endpoint you map it to, or tolerant of zero values.
type PromptTemplateData struct {
	SystemPrompt         string
	SuppressSystemPrompt bool // used by chat specifically to indicate that SystemPrompt above should be _ignored_
	Input                string
	Instruction          string
	Functions            []functions.Function
	MessageIndex         int
}

type ChatMessageTemplateData struct {
	SystemPrompt string
	Role         string
	RoleName     string
	FunctionName string
	Content      string
	MessageIndex int
	Function     bool
	FunctionCall interface{}
	LastMessage  bool
}

const (
	ChatPromptTemplate templates.TemplateType = iota
	ChatMessageTemplate
	CompletionPromptTemplate
	EditPromptTemplate
	FunctionsPromptTemplate
)

func (ml *ModelLoader) EvaluateTemplateForPrompt(templateType templates.TemplateType, templateName string, in PromptTemplateData) (string, error) {
	// TODO: should this check be improved?
	if templateType == ChatMessageTemplate {
		return "", fmt.Errorf("invalid templateType: ChatMessage")
	}
	return ml.templates.EvaluateTemplate(templateType, templateName, in)
}

func (ml *ModelLoader) EvaluateTemplateForChatMessage(templateName string, messageData ChatMessageTemplateData) (string, error) {
	return ml.templates.EvaluateTemplate(ChatMessageTemplate, templateName, messageData)
}
