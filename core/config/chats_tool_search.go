package config

// The values of the setting chats_tool_search. The chats can send the large
// LocalAI tool groups to the model as typed tools, or hide them behind three
// small tools that search, describe and call them.
const (
	// ToolSearchAuto defers the groups when their tool schemas take a tenth
	// or more of the model's context window.
	ToolSearchAuto = "auto"
	// ToolSearchOn always defers the groups, and the tools of the user's MCP
	// connectors with them.
	ToolSearchOn = "on"
	// ToolSearchOff never defers them.
	ToolSearchOff = "off"
)

// ValidToolSearch says whether v is a value of chats_tool_search. Empty is
// valid and means auto.
func ValidToolSearch(v string) bool {
	switch v {
	case "", ToolSearchAuto, ToolSearchOn, ToolSearchOff:
		return true
	}
	return false
}

// EffectiveChatsToolSearch returns the mode of the tool search: auto when the
// setting is empty or not a known value.
func (o *ApplicationConfig) EffectiveChatsToolSearch() string {
	switch o.ChatsToolSearch {
	case ToolSearchOn, ToolSearchOff:
		return o.ChatsToolSearch
	}
	return ToolSearchAuto
}
