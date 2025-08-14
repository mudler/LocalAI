package templates

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/core/schema"
	"github.com/mudler/LocalAI/pkg/functions"
	"github.com/rs/zerolog/log"
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
	ReasoningEffort      string
	Metadata             map[string]string
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
	ChatPromptTemplate TemplateType = iota
	ChatMessageTemplate
	CompletionPromptTemplate
	EditPromptTemplate
	FunctionsPromptTemplate
)

type Evaluator struct {
	cache *templateCache
}

func NewEvaluator(modelPath string) *Evaluator {
	return &Evaluator{
		cache: newTemplateCache(modelPath),
	}
}

func (e *Evaluator) EvaluateTemplateForPrompt(templateType TemplateType, config config.ModelConfig, in PromptTemplateData) (string, error) {
	template := ""

	// A model can have a "file.bin.tmpl" file associated with a prompt template prefix
	if e.cache.existsInModelPath(fmt.Sprintf("%s.tmpl", config.Model)) {
		template = config.Model
	}

	switch templateType {
	case CompletionPromptTemplate:
		if config.TemplateConfig.Completion != "" {
			template = config.TemplateConfig.Completion
		}
	case EditPromptTemplate:
		if config.TemplateConfig.Edit != "" {
			template = config.TemplateConfig.Edit
		}
	case ChatPromptTemplate:
		if config.TemplateConfig.Chat != "" {
			template = config.TemplateConfig.Chat
		}
	case FunctionsPromptTemplate:
		if config.TemplateConfig.Functions != "" {
			template = config.TemplateConfig.Functions
		}
	}

	if template == "" {
		return in.Input, nil
	}

	if config.TemplateConfig.JinjaTemplate {
		return e.evaluateJinjaTemplateForPrompt(templateType, template, in)
	}

	return e.cache.evaluateTemplate(templateType, template, in)
}

func (e *Evaluator) evaluateTemplateForChatMessage(templateName string, messageData ChatMessageTemplateData) (string, error) {
	return e.cache.evaluateTemplate(ChatMessageTemplate, templateName, messageData)
}

func (e *Evaluator) templateJinjaChat(templateName string, messageData []ChatMessageTemplateData, funcs []functions.Function) (string, error) {

	conversation := make(map[string]interface{})
	messages := make([]map[string]interface{}, len(messageData))

	// convert from ChatMessageTemplateData to what the jinja template expects

	for _, message := range messageData {
		// TODO: this seems to cover minimum text templates. Can be expanded to cover more complex interactions
		var data []byte
		data, _ = json.Marshal(message.FunctionCall)
		messages = append(messages, map[string]interface{}{
			"role":      message.RoleName,
			"content":   message.Content,
			"tool_call": string(data),
		})
	}

	conversation["messages"] = messages

	// if tools are detected, add these
	if len(funcs) > 0 {
		conversation["tools"] = funcs
	}

	return e.cache.evaluateJinjaTemplate(ChatMessageTemplate, templateName, conversation)
}

func (e *Evaluator) evaluateJinjaTemplateForPrompt(templateType TemplateType, templateName string, in PromptTemplateData) (string, error) {

	conversation := make(map[string]interface{})

	conversation["system_prompt"] = in.SystemPrompt
	conversation["content"] = in.Input

	return e.cache.evaluateJinjaTemplate(templateType, templateName, conversation)
}

func (e *Evaluator) TemplateMessages(input schema.OpenAIRequest, messages []schema.Message, config *config.ModelConfig, funcs []functions.Function, shouldUseFn bool) string {

	if config.TemplateConfig.JinjaTemplate {
		var messageData []ChatMessageTemplateData
		for messageIndex, i := range messages {
			fcall := i.FunctionCall
			if len(i.ToolCalls) > 0 {
				fcall = i.ToolCalls
			}
			messageData = append(messageData, ChatMessageTemplateData{
				SystemPrompt: config.SystemPrompt,
				Role:         config.Roles[i.Role],
				RoleName:     i.Role,
				Content:      i.StringContent,
				FunctionCall: fcall,
				FunctionName: i.Name,
				LastMessage:  messageIndex == (len(messages) - 1),
				Function:     config.Grammar != "" && (messageIndex == (len(messages) - 1)),
				MessageIndex: messageIndex,
			})
		}

		templatedInput, err := e.templateJinjaChat(config.TemplateConfig.ChatMessage, messageData, funcs)
		if err == nil {
			return templatedInput
		}
	}

	var predInput string
	suppressConfigSystemPrompt := false
	mess := []string{}
	for messageIndex, i := range messages {
		var content string
		role := i.Role

		// if function call, we might want to customize the role so we can display better that the "assistant called a json action"
		// if an "assistant_function_call" role is defined, we use it, otherwise we use the role that is passed by in the request
		if (i.FunctionCall != nil || i.ToolCalls != nil) && i.Role == "assistant" {
			roleFn := "assistant_function_call"
			r := config.Roles[roleFn]
			if r != "" {
				role = roleFn
			}
		}
		r := config.Roles[role]
		contentExists := i.Content != nil && i.StringContent != ""

		fcall := i.FunctionCall
		if len(i.ToolCalls) > 0 {
			fcall = i.ToolCalls
		}

		// First attempt to populate content via a chat message specific template
		if config.TemplateConfig.ChatMessage != "" {
			chatMessageData := ChatMessageTemplateData{
				SystemPrompt: config.SystemPrompt,
				Role:         r,
				RoleName:     role,
				Content:      i.StringContent,
				FunctionCall: fcall,
				FunctionName: i.Name,
				LastMessage:  messageIndex == (len(messages) - 1),
				Function:     config.Grammar != "" && (messageIndex == (len(messages) - 1)),
				MessageIndex: messageIndex,
			}
			templatedChatMessage, err := e.evaluateTemplateForChatMessage(config.TemplateConfig.ChatMessage, chatMessageData)
			if err != nil {
				log.Error().Err(err).Interface("message", chatMessageData).Str("template", config.TemplateConfig.ChatMessage).Msg("error processing message with template, skipping")
			} else {
				if templatedChatMessage == "" {
					log.Warn().Msgf("template \"%s\" produced blank output for %+v. Skipping!", config.TemplateConfig.ChatMessage, chatMessageData)
					continue // TODO: This continue is here intentionally to skip over the line `mess = append(mess, content)` below, and to prevent the sprintf
				}
				log.Debug().Msgf("templated message for chat: %s", templatedChatMessage)
				content = templatedChatMessage
			}
		}

		marshalAnyRole := func(f any) {
			j, err := json.Marshal(f)
			if err == nil {
				if contentExists {
					content += "\n" + fmt.Sprint(r, " ", string(j))
				} else {
					content = fmt.Sprint(r, " ", string(j))
				}
			}
		}
		marshalAny := func(f any) {
			j, err := json.Marshal(f)
			if err == nil {
				if contentExists {
					content += "\n" + string(j)
				} else {
					content = string(j)
				}
			}
		}
		// If this model doesn't have such a template, or if that template fails to return a value, template at the message level.
		if content == "" {
			if r != "" {
				if contentExists {
					content = fmt.Sprint(r, i.StringContent)
				}

				if i.FunctionCall != nil {
					marshalAnyRole(i.FunctionCall)
				}
				if i.ToolCalls != nil {
					marshalAnyRole(i.ToolCalls)
				}
			} else {
				if contentExists {
					content = fmt.Sprint(i.StringContent)
				}
				if i.FunctionCall != nil {
					marshalAny(i.FunctionCall)
				}
				if i.ToolCalls != nil {
					marshalAny(i.ToolCalls)
				}
			}
			// Special Handling: System. We care if it was printed at all, not the r branch, so check separately
			if contentExists && role == "system" {
				suppressConfigSystemPrompt = true
			}
		}

		mess = append(mess, content)
	}

	joinCharacter := "\n"
	if config.TemplateConfig.JoinChatMessagesByCharacter != nil {
		joinCharacter = *config.TemplateConfig.JoinChatMessagesByCharacter
	}

	predInput = strings.Join(mess, joinCharacter)
	log.Debug().Msgf("Prompt (before templating): %s", predInput)

	promptTemplate := ChatPromptTemplate

	if config.TemplateConfig.Functions != "" && shouldUseFn {
		promptTemplate = FunctionsPromptTemplate
	}

	templatedInput, err := e.EvaluateTemplateForPrompt(promptTemplate, *config, PromptTemplateData{
		SystemPrompt:         config.SystemPrompt,
		SuppressSystemPrompt: suppressConfigSystemPrompt,
		Input:                predInput,
		Functions:            funcs,
		ReasoningEffort:      input.ReasoningEffort,
		Metadata:             input.Metadata,
	})
	if err == nil {
		predInput = templatedInput
		log.Debug().Msgf("Template found, input modified to: %s", predInput)
	} else {
		log.Debug().Msgf("Template failed loading: %s", err.Error())
	}

	return predInput
}
