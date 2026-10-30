// SPDX-License-Identifier: MIT
package tools

import (
	"encoding/json"

	"github.com/mudler/LocalAI/addons/web/internal/providers"
	"github.com/mudler/LocalAI/pkg/addons/contract"
	"github.com/mudler/LocalAI/pkg/web/webtools"
)

// The built-in chat tools that search and fetch replace.
const (
	ShadowsSearch = webtools.SearchName
	ShadowsFetch  = webtools.FetchName
)

func schema(v any) json.RawMessage {
	b, err := json.Marshal(v)
	if err != nil {
		panic(err)
	}
	return b
}

type obj = map[string]any

// Definitions returns what a model and the page see of each tool. The chat
// tools have the bounds of the built-in tools; the contract allows no
// underscore in an argument name, so max_results and max_chars are
// maxResults and maxChars here.
func Definitions() []contract.ToolDefinition {
	names := make([]any, len(providers.Names))
	for i, n := range providers.Names {
		names[i] = n
	}
	keyed := []any{providers.Brave, providers.Tavily, providers.Google}
	return []contract.ToolDefinition{
		{
			Name: ToolSearch, Kind: contract.ToolKindRead, Shadows: ShadowsSearch,
			Description: "Search the web. Returns up to 10 results with title, URL and snippet. Use web_fetch to read a result.",
			InputSchema: schema(obj{
				"type": "object", "additionalProperties": false, "required": []any{"query"},
				"properties": obj{
					"query":      obj{"type": "string", "description": "What to search for.", "maxLength": 400},
					"maxResults": obj{"type": "integer", "minimum": 1, "maximum": 10, "description": "How many results (default 5)."},
					"freshness":  obj{"type": "string", "enum": []any{"day", "week", "month", "year"}, "description": "Only results from this recent period."},
					"site":       obj{"type": "string", "description": "Only results from this host, for example docs.python.org."},
				},
			}),
		},
		{
			Name: ToolFetch, Kind: contract.ToolKindRead, Shadows: ShadowsFetch,
			Description: "Read a public web page or file by URL (http or https), PDF included. Returns its main text as Markdown. Long pages are cut; the end says how much is left.",
			InputSchema: schema(obj{
				"type": "object", "additionalProperties": false, "required": []any{"url"},
				"properties": obj{
					"url":      obj{"type": "string", "description": "The http or https address to read."},
					"maxChars": obj{"type": "integer", "minimum": 100, "maximum": webtools.HardMaxChars, "description": "Most characters to return (default 20000)."},
					"format":   obj{"type": "string", "enum": []any{"markdown", "text"}, "description": "markdown (default) keeps headings, links and tables; text is plain."},
				},
			}),
		},
		{
			Name: ToolStatus, Kind: contract.ToolKindRead, UIOnly: true,
			Description: "Say which search providers serve now. An administrator also gets the settings, without the keys. The Settings page calls it; no model does.",
			InputSchema: schema(obj{"type": "object", "properties": obj{}, "additionalProperties": false}),
		},
		{
			Name: ToolSetSettings, Kind: contract.ToolKindRead, UIOnly: true,
			Description: "Save the search providers, their order and their keys. Administrators only. The Settings page calls it; no model does.",
			InputSchema: schema(obj{
				"type": "object", "additionalProperties": false,
				"properties": obj{
					"order": obj{"type": "array", "maxItems": len(providers.Names), "items": obj{"type": "string", "enum": names}, "description": "The providers to try, first to last."},
					"keys": obj{"type": "object", "additionalProperties": false, "description": "A new key for each named provider. An empty value keeps the saved key.",
						"properties": obj{
							providers.Brave:  obj{"type": "string", "maxLength": 256},
							providers.Tavily: obj{"type": "string", "maxLength": 256},
							providers.Google: obj{"type": "string", "maxLength": 256},
						}},
					"clear":        obj{"type": "array", "maxItems": 3, "items": obj{"type": "string", "enum": keyed}, "description": "Remove the saved key of these providers."},
					"googleEngine": obj{"type": "string", "maxLength": 128, "description": "The Programmable Search Engine ID (cx)."},
					"searxngUrl":   obj{"type": "string", "maxLength": 2048, "description": "The address of a SearXNG instance."},
				},
			}),
		},
		{
			Name: ToolTestSearch, Kind: contract.ToolKindRead, UIOnly: true,
			Description: "Run one search with each provider of the order and say which answer. Administrators only. The Settings page calls it; no model does.",
			InputSchema: schema(obj{
				"type": "object", "additionalProperties": false,
				"properties": obj{"query": obj{"type": "string", "maxLength": 400, "description": "What to search for. Default LocalAI."}},
			}),
		},
	}
}
