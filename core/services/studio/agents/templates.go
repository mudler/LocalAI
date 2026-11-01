package agents

// Template is a starter agent: a definition a person can adjust. It grants
// nothing (all four lists are empty), so a chat with it gets only what the
// person grants after saving it.
type Template struct {
	ID           string
	Name         string
	Description  string
	Instructions string
}

// Templates returns the starter agents, in the order they are offered.
func Templates() []Template {
	return []Template{
		{
			ID: "researcher", Name: "research-assistant",
			Description: "Looks things up in your collections and tools and writes short briefs.",
			Instructions: "You are a careful research assistant. Use the tools you have to find sources, read at least two of them, " +
				"then summarise in plain language. Cite every claim with a numbered source. If a source cannot be read, say so, and say what you could not check.",
		},
		{
			ID: "reviewer", Name: "code-reviewer",
			Description: "Reviews a change and lists what to fix first.",
			Instructions: "You review code changes. Read the diff, find bugs and unclear code, and list the most important problems first. " +
				"Quote the line you mean. Do not rewrite the change.",
		},
		{
			ID: "digest", Name: "daily-digest",
			Description: "Collects what is new on a topic and summarises it. Pair it with a schedule trigger.",
			Instructions: "You collect what is new on the topics you are given and write a short digest: one line per item, newest first, each with its source. " +
				"If nothing is new, say so in one line.",
		},
		{
			ID: "editor", Name: "writing-editor",
			Description: "Edits your text for clarity and keeps your voice.",
			Instructions: "You edit text. Fix errors, cut filler and make unclear sentences direct. Keep the writer's voice and meaning. " +
				"Return the edited text, then a short list of the changes that matter.",
		},
	}
}

// TemplateByID returns the template with this ID.
func TemplateByID(id string) (Template, bool) {
	for _, t := range Templates() {
		if t.ID == id {
			return t, true
		}
	}
	return Template{}, false
}
