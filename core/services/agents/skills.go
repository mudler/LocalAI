package agents

import (
	"fmt"
	"html"
	"strings"
	"text/template"

	"github.com/Masterminds/sprig/v3"
	"github.com/mudler/xlog"
)

// SkillInfo represents a skill available to the agent.
type SkillInfo struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	Content     string `json:"content,omitempty"` // full skill content for prompt-mode injection
}

// SkillContentProvider loads full skill info (including content) for a user.
// Used by the scheduler to enrich NATS events without needing a direct DB dependency.
type SkillContentProvider func(userID string) ([]SkillInfo, error)

// SkillProvider loads available skills.
type SkillProvider interface {
	ListSkills() ([]SkillInfo, error)
}

// SkillsToolsHint is injected into the system prompt when skills_mode is "tools"
// to guide the agent on using the request_skill tool.
const SkillsToolsHint = `You have access to skills via the ` + "`request_skill`" + ` tool. ` +
	`Call it with a skill name to retrieve the full skill instructions, then follow them to complete the task.`

const defaultSkillsTemplate = `You can use the following skills to help with the task.
To request the skill, you need to use the ` + "`request_skill`" + ` tool. The skill name is the name of the skill you want to use.
<available_skills>
{{range .Skills}}
  <skill>
    <name>{{escapeXML .Name}}</name>
    {{if .Content}}<content>{{escapeXML .Content}}</content>{{else}}<description>{{escapeXML .Description}}</description>{{end}}
  </skill>
{{end}}
</available_skills>`

// RenderSkillsPrompt generates the skills prompt text for injection into the system prompt.
// Uses the agent's custom template if set, otherwise the default XML format.
func RenderSkillsPrompt(skills []SkillInfo, customTemplate string) string {
	if len(skills) == 0 {
		return ""
	}

	tmplText := customTemplate
	if tmplText == "" {
		tmplText = defaultSkillsTemplate
	}

	funcMap := sprig.FuncMap()
	funcMap["escapeXML"] = html.EscapeString

	tmpl, err := template.New("skills").Funcs(funcMap).Parse(tmplText)
	if err != nil {
		xlog.Error("Failed to parse skills template", "error", err)
		// Fallback: simple listing
		var sb strings.Builder
		sb.WriteString("Available skills:\n")
		for _, s := range skills {
			sb.WriteString(fmt.Sprintf("- %s: %s\n", s.Name, s.Description))
		}
		return sb.String()
	}

	data := map[string]any{
		"Skills": skills,
	}

	var sb strings.Builder
	if err := tmpl.Execute(&sb, data); err != nil {
		xlog.Error("Failed to execute skills template", "error", err)
		return ""
	}

	return sb.String()
}

// RequestSkillArgs defines the arguments for the request_skill tool.
type RequestSkillArgs struct {
	SkillName string `json:"skill_name" jsonschema:"description=The name of the skill to request"`
}

// RequestSkillTool implements the request_skill cogito tool.
type RequestSkillTool struct {
	Skills []SkillInfo
}

func (t RequestSkillTool) Run(args RequestSkillArgs) (string, any, error) {
	for _, s := range t.Skills {
		if s.Name == args.SkillName {
			body := s.Content
			if body == "" {
				body = s.Description
			}
			return fmt.Sprintf("Skill '%s':\n%s", s.Name, body), nil, nil
		}
	}
	available := skillNames(t.Skills)
	return fmt.Sprintf("Skill '%s' not found. Available skills: %s", args.SkillName, available), nil, nil
}

// skillNames returns a comma-separated list of skill names.
func skillNames(skills []SkillInfo) string {
	names := make([]string, len(skills))
	for i, s := range skills {
		names[i] = s.Name
	}
	return strings.Join(names, ", ")
}

// FilterSkills filters skills by the agent's selected_skills list.
// If selectedSkills is empty/nil, all skills are returned.
func FilterSkills(all []SkillInfo, selectedSkills []string) []SkillInfo {
	if len(selectedSkills) == 0 {
		return all
	}

	selected := make(map[string]bool, len(selectedSkills))
	for _, s := range selectedSkills {
		selected[s] = true
	}

	var filtered []SkillInfo
	for _, s := range all {
		if selected[s.Name] {
			filtered = append(filtered, s)
		}
	}
	return filtered
}
