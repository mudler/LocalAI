// Package builtintools lists the tools the chats serve themselves and that an
// administrator can switch off: their names, the per-chat switch group they
// belong to and the runtime settings that control them. The Tools section of
// the Settings page and the chat's Tools menu read this list, so a new
// built-in tool appears there by adding one entry.
package builtintools

// Tool is one built-in tool.
type Tool struct {
	// Name is the name the model calls, such as "web_search".
	Name string `json:"name"`
	// Group is the per-chat switch the tool belongs to, such as "web". The
	// values are what PUT /api/chats/{id}/builtins accepts.
	Group string `json:"group"`
	// Title and Description are English text for a settings row.
	Title       string `json:"title"`
	Description string `json:"description"`
	// Setting is the /api/settings key of the administrator's on/off switch.
	// The switch is true when the tool is on.
	Setting string `json:"setting"`
	// Options are the other /api/settings keys that configure the tool.
	Options []string `json:"options,omitempty"`
	// ReadOnly says that the tool only reads: it needs no confirmation.
	ReadOnly bool `json:"read_only"`
	// Replaceable says that an add-on may replace the tool (see the design of
	// tool shadowing). False for every tool until that lands.
	Replaceable bool `json:"replaceable"`
}

// The groups.
const (
	GroupWeb = "web"
)

// The runtime settings of the web tools.
const (
	SettingWebSearch       = "chats_web_search"
	SettingWebFetch        = "chats_web_fetch"
	SettingWebSearXNGURL   = "chats_web_searxng_url"
	SettingWebAllowPrivate = "chats_web_allow_private"
	SettingWebPorts        = "chats_web_ports"
)

var all = []Tool{
	{
		Name: "web_search", Group: GroupWeb, Title: "Web search",
		Description: "Search the web with DuckDuckGo, or with your SearXNG instance when one is set.",
		Setting:     SettingWebSearch, Options: []string{SettingWebSearXNGURL},
		ReadOnly: true,
	},
	{
		Name: "web_fetch", Group: GroupWeb, Title: "Read web pages",
		Description: "Read a public web page or file by its address. Addresses inside your network are refused unless you allow them.",
		Setting:     SettingWebFetch, Options: []string{SettingWebAllowPrivate, SettingWebPorts},
		ReadOnly: true,
	},
}

// All returns the built-in tools, in a stable order. The caller may change
// the copy it gets.
func All() []Tool {
	out := make([]Tool, len(all))
	for i, t := range all {
		t.Options = append([]string(nil), t.Options...)
		out[i] = t
	}
	return out
}

// Lookup returns the tool with that name.
func Lookup(name string) (Tool, bool) {
	for _, t := range all {
		if t.Name == name {
			return t, true
		}
	}
	return Tool{}, false
}

// Groups returns the switch groups, in a stable order.
func Groups() []string {
	var out []string
	seen := map[string]bool{}
	for _, t := range all {
		if !seen[t.Group] {
			seen[t.Group] = true
			out = append(out, t.Group)
		}
	}
	return out
}

// IsGroup says whether g is a switch group.
func IsGroup(g string) bool {
	for _, t := range all {
		if t.Group == g {
			return true
		}
	}
	return false
}
