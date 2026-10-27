// SPDX-License-Identifier: MIT

// Package mcpapps holds the host side rules of MCP Apps: tools that declare an
// interactive HTML view in _meta.ui.resourceUri. It parses what a server says,
// checks it, and bounds what is passed on. It holds no state and does no I/O.
package mcpapps

import (
	"encoding/json"
	"errors"
	"fmt"
	"mime"
	"regexp"
	"strings"
	"unicode/utf8"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// ErrBadResource means a resource is not an MCP App page the host will serve:
// the wrong type, too large, or not text.
var ErrBadResource = errors.New("not an MCP App page")

// Limits.
const (
	// MaxResourceBytes bounds the HTML of an app.
	MaxResourceBytes = 4 << 20
	// MaxResultBytes bounds the JSON result of a tool call that goes to an app.
	MaxResultBytes = 256 << 10
	// MaxInputBytes bounds the tool arguments kept for an app.
	MaxInputBytes = 16 << 10
	// MaxURIBytes bounds a resource URI.
	MaxURIBytes = 2048
	// MaxDomains bounds each list of CSP domains.
	MaxDomains = 20
)

// Visibility words of _meta.ui.visibility.
const (
	VisibilityModel = "model"
	VisibilityApp   = "app"
)

// UI is what a tool says about its view.
type UI struct {
	// ResourceURI is the ui:// resource that holds the HTML. Empty: no view.
	ResourceURI string
	// Visibility says who may call the tool: the model, the app, or both.
	Visibility []string
}

// ForModel reports whether the model may call the tool.
func (u UI) ForModel() bool { return has(u.Visibility, VisibilityModel) }

// ForApp reports whether an app of the same server may call the tool.
func (u UI) ForApp() bool { return has(u.Visibility, VisibilityApp) }

func has(list []string, v string) bool {
	for _, s := range list {
		if s == v {
			return true
		}
	}
	return false
}

// ParseUI reads the ui part of a tool's _meta. A tool without it is visible to
// both, as the specification says. The legacy flat key "ui/resourceUri" is read
// too. A resource URI that is not a valid ui:// URI is ignored.
func ParseUI(meta map[string]any) UI {
	u := UI{Visibility: []string{VisibilityModel, VisibilityApp}}
	ui, _ := meta["ui"].(map[string]any)
	if s, ok := ui["resourceUri"].(string); ok && CheckURI(s) == nil {
		u.ResourceURI = s
	} else if s, ok := meta["ui/resourceUri"].(string); ok && CheckURI(s) == nil {
		u.ResourceURI = s
	}
	if raw, ok := ui["visibility"].([]any); ok {
		u.Visibility = []string{}
		for _, v := range raw {
			if s, ok := v.(string); ok && (s == VisibilityModel || s == VisibilityApp) && !has(u.Visibility, s) {
				u.Visibility = append(u.Visibility, s)
			}
		}
	}
	return u
}

// CheckURI accepts only a ui:// URI of a sane size.
func CheckURI(uri string) error {
	switch {
	case len(uri) > MaxURIBytes:
		return errors.New("the resource URI is too long")
	case !strings.HasPrefix(uri, "ui://") || len(uri) == len("ui://"):
		return errors.New("the resource URI must start with ui://")
	}
	for _, r := range uri {
		if r < 0x21 || r == 0x7f {
			return errors.New("the resource URI has a space or a control character")
		}
	}
	return nil
}

// CheckContent accepts text/html, with no profile or the mcp-app one, up to the
// size cap.
func CheckContent(mimeType string, size int) error {
	mt, params, err := mime.ParseMediaType(mimeType)
	if err != nil || mt != "text/html" {
		return fmt.Errorf("%w: the resource is %q", ErrBadResource, mimeType)
	}
	if p, ok := params["profile"]; ok && p != "mcp-app" {
		return fmt.Errorf("%w: the resource has the profile %q, not mcp-app", ErrBadResource, p)
	}
	if size > MaxResourceBytes {
		return fmt.Errorf("%w: the resource is larger than %d MiB", ErrBadResource, MaxResourceBytes>>20)
	}
	return nil
}

// CSP lists the origins an app asks to reach, by the names of the specification.
type CSP struct {
	ConnectDomains  []string `json:"connectDomains,omitempty"`
	ResourceDomains []string `json:"resourceDomains,omitempty"`
	FrameDomains    []string `json:"frameDomains,omitempty"`
	BaseURIDomains  []string `json:"baseUriDomains,omitempty"`
}

// Permissions are the browser features an app asks for.
type Permissions struct {
	Camera         bool `json:"camera,omitempty"`
	Microphone     bool `json:"microphone,omitempty"`
	Geolocation    bool `json:"geolocation,omitempty"`
	ClipboardWrite bool `json:"clipboardWrite,omitempty"`
}

// Meta is the _meta.ui of a resource after the host cleaned it.
type Meta struct {
	CSP           CSP         `json:"csp"`
	Permissions   Permissions `json:"permissions"`
	PrefersBorder *bool       `json:"prefersBorder,omitempty"`
}

// domain is an origin a CSP source list may hold: https or wss, a host that may
// start with one wildcard label, and a port. Anything else could change the
// meaning of the header, so it is dropped.
var domain = regexp.MustCompile(`^(https|wss)://(\*\.)?[A-Za-z0-9]([A-Za-z0-9.-]{0,251}[A-Za-z0-9])?(:[0-9]{1,5})?$`)

func cleanDomains(v any) []string {
	raw, _ := v.([]any)
	var out []string
	seen := map[string]bool{}
	for _, d := range raw {
		s, ok := d.(string)
		if !ok || !domain.MatchString(s) || seen[s] {
			continue
		}
		seen[s] = true
		out = append(out, s)
		if len(out) == MaxDomains {
			break
		}
	}
	return out
}

// ResourceMeta reads the ui part of a resource's _meta and keeps only what the
// host can use safely.
func ResourceMeta(meta map[string]any) Meta {
	var m Meta
	ui, _ := meta["ui"].(map[string]any)
	if csp, ok := ui["csp"].(map[string]any); ok {
		m.CSP = CSP{
			ConnectDomains:  cleanDomains(csp["connectDomains"]),
			ResourceDomains: cleanDomains(csp["resourceDomains"]),
			FrameDomains:    cleanDomains(csp["frameDomains"]),
			BaseURIDomains:  cleanDomains(csp["baseUriDomains"]),
		}
	}
	if p, ok := ui["permissions"].(map[string]any); ok {
		_, m.Permissions.Camera = p["camera"]
		_, m.Permissions.Microphone = p["microphone"]
		_, m.Permissions.Geolocation = p["geolocation"]
		_, m.Permissions.ClipboardWrite = p["clipboardWrite"]
	}
	if b, ok := ui["prefersBorder"].(bool); ok {
		m.PrefersBorder = &b
	}
	return m
}

// FromCallResult turns a tool result into the JSON an app receives. A result
// over MaxResultBytes loses its structured content and keeps its text, cut.
func FromCallResult(res *mcp.CallToolResult) json.RawMessage {
	raw, err := json.Marshal(res)
	if err == nil && len(raw) <= MaxResultBytes {
		return raw
	}
	var text strings.Builder
	for _, c := range res.Content {
		if tc, ok := c.(*mcp.TextContent); ok {
			text.WriteString(tc.Text)
			text.WriteString("\n")
		}
	}
	t := text.String()
	if len(t) > MaxResultBytes/2 {
		t = strings.ToValidUTF8(t[:MaxResultBytes/2], "") + "\n[result cut]"
	}
	out, _ := json.Marshal(map[string]any{
		"content": []map[string]any{{"type": "text", "text": t}},
		"isError": res.IsError,
	})
	return out
}

// Resource is the page of an app with what the host needs to frame it.
type Resource struct {
	URI      string `json:"uri"`
	MIMEType string `json:"mimeType"`
	HTML     string `json:"html"`
	Meta     Meta   `json:"meta"`
}

// FromReadResult picks the content of uri from a resources/read answer and
// checks it: a text/html page within the size cap.
func FromReadResult(res *mcp.ReadResourceResult, uri string) (Resource, error) {
	if res != nil {
		for _, c := range res.Contents {
			if c == nil || c.URI != uri {
				continue
			}
			body := c.Text
			if body == "" {
				body = string(c.Blob)
			}
			if err := CheckContent(c.MIMEType, len(body)); err != nil {
				return Resource{}, err
			}
			if !utf8.ValidString(body) {
				return Resource{}, fmt.Errorf("%w: the resource is not valid UTF-8 text", ErrBadResource)
			}
			meta := map[string]any(c.Meta)
			if meta == nil {
				meta = res.Meta
			}
			return Resource{URI: uri, MIMEType: c.MIMEType, HTML: body, Meta: ResourceMeta(meta)}, nil
		}
	}
	return Resource{}, errors.New("the server returned no content for the resource")
}

// CallResult is the outcome of a tool call for an app: the JSON of the MCP
// result, and the text the model would see.
type CallResult struct {
	Text   string
	Result json.RawMessage
}
