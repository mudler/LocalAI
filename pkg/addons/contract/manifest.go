// SPDX-License-Identifier: MIT
// Package contract defines version-one addon manifests and runtime wire DTOs.
package contract

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"path"
	"regexp"
	"strings"
	"unicode"
	"unicode/utf8"
)

const SchemaVersion = 1

// HostSDKVersion is the add-on UI SDK version this host provides. Version 2
// adds the shared UI primitives, the hooks, the viewer and sdk.core.fetch. An
// add-on declares the range it accepts in ui.sdk; a host outside that range
// refuses it at install.
const HostSDKVersion uint64 = 2

// SDKRefusal is the install-time refusal of an add-on whose ui.sdk range does
// not contain the host SDK version. Its text is meant for the administrator.
type SDKRefusal struct{ Min, Max, Host uint64 }

func (e *SDKRefusal) Error() string {
	return fmt.Sprintf("the add-on needs SDK %d to %d and this host provides SDK %d", e.Min, e.Max, e.Host)
}

// Bounds of the optional apps-surface fields. They are character counts, not
// bytes, so a title in any script has the same room.
const (
	MaxTitleChars       = 80
	MaxDescriptionChars = 280
	MaxLanguages        = 16
	// MaxIconBytes bounds a packaged icon. Package validation enforces it
	// against the real file.
	MaxIconBytes = 256 << 10
	maxAssetPath = 256
)

// LaunchModeAlways is the only launch mode of version one: the owner runs the
// addon for as long as it is enabled. An absent mode means the same.
const LaunchModeAlways = "always"

// IconTypes maps the extensions an icon may have to their media type.
var IconTypes = map[string]string{".png": "image/png", ".webp": "image/webp", ".jpg": "image/jpeg", ".jpeg": "image/jpeg"}

type VersionRange struct {
	Min uint64 `json:"min"`
	Max uint64 `json:"max"`
}

func (r VersionRange) accepts(v uint64) bool {
	return r.Min > 0 && r.Max >= r.Min && v >= r.Min && v <= r.Max
}

type HostCompatibility struct {
	Host       uint64 `json:"host"`
	API        uint64 `json:"api"`
	Protocol   uint64 `json:"protocol"`
	SDK        uint64 `json:"sdk"`
	DataFormat uint64 `json:"dataFormat"`
	OS         string `json:"os"`
	Arch       string `json:"arch"`
}
type Manifest struct {
	SchemaVersion int          `json:"schemaVersion"`
	ID            string       `json:"id"`
	Version       string       `json:"version"`
	Executable    string       `json:"executable"`
	OS            string       `json:"os"`
	Arch          string       `json:"arch"`
	Host          VersionRange `json:"host"`
	API           VersionRange `json:"api"`
	Protocol      VersionRange `json:"protocol"`
	DataFormat    VersionRange `json:"dataFormat"`
	// LaunchMode is absent or "always": the owner starts the addon while it is
	// enabled. "on-demand" is reserved and rejected until it is implemented.
	LaunchMode   string       `json:"launchMode,omitempty"`
	Capabilities Capabilities `json:"capabilities"`
}
type Capabilities struct {
	Service bool     `json:"service,omitempty"`
	Tools   []string `json:"tools,omitempty"`
	// ToolDefinitions describe the tools a model may call: the schema of the
	// arguments and the host authority of each. A declared tool without a
	// definition is not offered to models.
	ToolDefinitions []ToolDefinition `json:"toolDefinitions,omitempty"`
	// AdminTools are tools that only the host's own UI-only resource
	// management endpoints may run. They are never in Tools, never have a
	// tool definition and never reach a model, a proposal, a typed dispatch
	// or an agent: the declared-tool rule of those paths reads Tools only.
	AdminTools []string `json:"adminTools,omitempty"`
	// Resources says which tools the UI-only resource management endpoints
	// use for this add-on's resources (create, rename, inspect, delete).
	Resources *ResourceTools `json:"resources,omitempty"`
	UI        *UI            `json:"ui,omitempty"`
}

// ResourceTools maps the host's UI-only resource management operations to
// tool names of the add-on. Every field is optional; an operation without a
// tool is not offered for the add-on.
type ResourceTools struct {
	// Noun is the singular word for one resource in the UI, such as database.
	Noun string `json:"noun,omitempty"`
	// Create is a tool of Tools whose definition has the kind create.
	Create string `json:"create,omitempty"`
	// Rename is a tool of Tools whose definition has the kind schema and takes
	// a name argument.
	Rename string `json:"rename,omitempty"`
	// Inspect is a tool of Tools whose definition has the kind read. Its
	// result is an object with a name string; the host shows it as the
	// display name of the resource.
	Inspect string `json:"inspect,omitempty"`
	// Delete is a tool of AdminTools that removes the resource and its
	// backing data. It is reachable only from the UI-only delete endpoint.
	Delete string `json:"delete,omitempty"`
}

// MaxAdminTools bounds capabilities.adminTools.
const MaxAdminTools = 4

// ResourceExistsTool is the read tool an add-on declares to say whether the
// backing of one of its resources exists.
const ResourceExistsTool = "resource-exists"

// validateResourceTools checks AdminTools and Resources against the declared
// tools and their definitions.
func validateResourceTools(c Capabilities) error {
	if len(c.AdminTools) > MaxAdminTools || !uniqueIDs(c.AdminTools) {
		return fmt.Errorf("invalid admin tools")
	}
	declared := map[string]bool{}
	for _, t := range c.Tools {
		declared[t] = true
	}
	for _, t := range c.AdminTools {
		if declared[t] {
			return fmt.Errorf("admin tool %q is also a model tool", t)
		}
	}
	kinds := map[string]string{}
	for _, d := range c.ToolDefinitions {
		kinds[d.Name] = d.Kind
	}
	r := c.Resources
	if r == nil {
		if len(c.AdminTools) > 0 {
			return fmt.Errorf("admin tools need a resources mapping")
		}
		return nil
	}
	if r.Create == "" && r.Rename == "" && r.Inspect == "" && r.Delete == "" {
		return fmt.Errorf("empty resources mapping")
	}
	if r.Noun != "" && !plainText(r.Noun, 40) {
		return fmt.Errorf("invalid resource noun")
	}
	for _, want := range []struct{ name, kind, label string }{
		{r.Create, ToolKindCreate, "create"}, {r.Rename, ToolKindSchema, "rename"}, {r.Inspect, ToolKindRead, "inspect"},
	} {
		if want.name == "" {
			continue
		}
		if !declared[want.name] || kinds[want.name] != want.kind {
			return fmt.Errorf("resources %s must name a declared tool of kind %s", want.label, want.kind)
		}
	}
	if (r.Create != "" || r.Delete != "") && !declared[ResourceExistsTool] {
		// The host settles an interrupted creation or deletion only on an
		// explicit answer of this tool. Without it a missing answer would be
		// read as a missing backing and its record dropped over live data.
		return fmt.Errorf("resources that are created or deleted must declare the %s tool", ResourceExistsTool)
	}
	if r.Delete == "" {
		if len(c.AdminTools) > 0 {
			return fmt.Errorf("admin tool is not mapped")
		}
		return nil
	}
	if len(c.AdminTools) != 1 || c.AdminTools[0] != r.Delete {
		return fmt.Errorf("resources delete must name the one admin tool")
	}
	return nil
}

type UI struct {
	Entry      string       `json:"entry"`
	CSS        []string     `json:"css,omitempty"`
	SDK        VersionRange `json:"sdk"`
	Pages      []string     `json:"pages,omitempty"`
	Navigation []string     `json:"navigation,omitempty"`
	// ChatActions are the chat actions: a bare identifier (inert) or a full
	// declaration with label, icon, placement and target.
	ChatActions []ChatAction `json:"chatActions,omitempty"`
	// Slots place exported components in closed host slots. Renderers draw
	// the approval card or the result of one of the addon's own tools.
	Slots     []SlotContribution `json:"slots,omitempty"`
	Renderers []Renderer         `json:"renderers,omitempty"`
	// MainPage is the route the Apps surface opens. It must be one of Pages;
	// without it the addon has no Open action.
	MainPage string `json:"mainPage,omitempty"`
	// Icon is a packaged immutable image, never a URL.
	Icon string `json:"icon,omitempty"`
	// Title and Description are plain text, either one string or a map of
	// BCP 47 language tag to string.
	Title       *Localized `json:"title,omitempty"`
	Description *Localized `json:"description,omitempty"`
}

// Localized is plain text that is either one string or a map of language tag
// to string. Exactly one of Text and ByLanguage is set.
type Localized struct {
	Text       string
	ByLanguage map[string]string
}

func (l Localized) MarshalJSON() ([]byte, error) {
	if l.ByLanguage != nil {
		return json.Marshal(l.ByLanguage)
	}
	return json.Marshal(l.Text)
}
func (l *Localized) UnmarshalJSON(b []byte) error {
	b = bytes.TrimSpace(b)
	var out Localized
	switch {
	case len(b) > 0 && b[0] == '"':
		if e := json.Unmarshal(b, &out.Text); e != nil {
			return e
		}
	case len(b) > 0 && b[0] == '{':
		out.ByLanguage = map[string]string{}
		if e := json.Unmarshal(b, &out.ByLanguage); e != nil {
			return e
		}
	default:
		return fmt.Errorf("localized text must be a string or a language map")
	}
	*l = out
	return nil
}

var namespace = regexp.MustCompile(`^[a-z][a-z0-9-]{0,63}$`)
var immutableVersion = regexp.MustCompile(`^(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)(-[a-z][a-z0-9-]*(\.[a-z0-9-]+)*)?$`)
var assetPath = regexp.MustCompile(`^[a-zA-Z0-9_./-]+$`)

// reservedAddonIDs are the first path segments the host itself serves under
// /api/addons/. An add-on ID shares that position (/api/addons/<id>/assets/...),
// so an add-on with one of these IDs would have its asset URLs captured by the
// host route of the same name. A test derives the registered segments from the
// real routes and requires each to be listed here.
var reservedAddonIDs = []string{"apps", "proposals", "artifacts", "resources", "conversations", "dispatch", "relay", "catalog", "icon", "assets", "installed", "sources", "builtin", "pins"}

// ReservedAddonIDs returns a copy of the reserved add-on IDs.
func ReservedAddonIDs() []string { return append([]string(nil), reservedAddonIDs...) }

// ReservedAddonID reports whether id is a path segment the host serves.
func ReservedAddonID(id string) bool {
	for _, r := range reservedAddonIDs {
		if r == id {
			return true
		}
	}
	return false
}

// ValidAddonID reports whether id is a well-formed add-on ID that no host
// route uses.
func ValidAddonID(id string) bool { return namespace.MatchString(id) && !ReservedAddonID(id) }

func relativePath(s string) bool {
	return s != "" && s != "." && s != ".." && assetPath.MatchString(s) && !strings.HasPrefix(s, "/") && path.Clean(s) == s && !strings.HasPrefix(s, "../")
}
func uniqueIDs(ids []string) bool {
	seen := map[string]bool{}
	for _, id := range ids {
		if !namespace.MatchString(id) || seen[id] {
			return false
		}
		seen[id] = true
	}
	return true
}
func ParseManifest(data []byte) (Manifest, error) {
	var m Manifest
	if err := strictJSON(data, &m, DefaultLimits().ManifestBytes); err != nil {
		return m, err
	}
	// A raw tool schema is kept as the bytes a JSON encoder writes for it:
	// compact, with HTML-significant characters escaped. The supervisor sends
	// the manifest's capabilities to the addon and compares what the addon
	// echoes with deep equality, so a schema held with the indentation of the
	// file would never match its own echo.
	for i := range m.Capabilities.ToolDefinitions {
		c, err := json.Marshal(m.Capabilities.ToolDefinitions[i].InputSchema)
		if err != nil {
			return m, fmt.Errorf("invalid tool schema")
		}
		m.Capabilities.ToolDefinitions[i].InputSchema = c
	}
	// Parsing validates intrinsic constraints without imposing this machine's platform.
	h := HostCompatibility{Host: m.Host.Min, API: m.API.Min, Protocol: m.Protocol.Min, DataFormat: m.DataFormat.Min, OS: m.OS, Arch: m.Arch}
	if m.Capabilities.UI != nil {
		h.SDK = m.Capabilities.UI.SDK.Min
	}
	return m, ValidateManifest(m, h)
}
func ValidateManifest(m Manifest, h HostCompatibility) error {
	if ReservedAddonID(m.ID) {
		return fmt.Errorf("add-on ID %q is reserved for a host route", m.ID)
	}
	if m.SchemaVersion != SchemaVersion || !namespace.MatchString(m.ID) || !immutableVersion.MatchString(m.Version) || !relativePath(m.Executable) {
		return fmt.Errorf("invalid manifest identity, version or executable")
	}
	// on-demand is reserved: accepting it would promise behavior the owner does
	// not implement, so every value but the default is refused at install.
	if m.LaunchMode != "" && m.LaunchMode != LaunchModeAlways {
		return fmt.Errorf("unsupported launch mode")
	}
	if !namespace.MatchString(m.OS) || !namespace.MatchString(m.Arch) || m.OS != h.OS || m.Arch != h.Arch {
		return fmt.Errorf("incompatible platform")
	}
	if !m.Host.accepts(h.Host) || !m.API.accepts(h.API) || !m.Protocol.accepts(h.Protocol) || !m.DataFormat.accepts(h.DataFormat) {
		return fmt.Errorf("incompatible host, API, protocol or data format")
	}
	c := m.Capabilities
	if (!c.Service && len(c.Tools) == 0 && c.UI == nil) || !uniqueIDs(c.Tools) {
		return fmt.Errorf("invalid capabilities")
	}
	if e := validateToolDefinitions(c.ToolDefinitions, c.Tools); e != nil {
		return e
	}
	if e := validateResourceTools(c); e != nil {
		return e
	}
	if u := c.UI; u != nil {
		if u.SDK.Min > 0 && u.SDK.Max >= u.SDK.Min && !u.SDK.accepts(h.SDK) {
			return &SDKRefusal{Min: u.SDK.Min, Max: u.SDK.Max, Host: h.SDK}
		}
		if !relativePath(u.Entry) || !u.SDK.accepts(h.SDK) || !uniqueIDs(u.Pages) || !uniqueIDs(u.Navigation) {
			return fmt.Errorf("invalid or incompatible UI")
		}
		seen := map[string]bool{u.Entry: true}
		for _, css := range u.CSS {
			if !relativePath(css) || seen[css] {
				return fmt.Errorf("invalid UI asset")
			}
			seen[css] = true
		}
		if u.Icon != "" {
			if !ValidIconPath(u.Icon) || seen[u.Icon] {
				return fmt.Errorf("invalid UI icon")
			}
		}
		if u.Title != nil && !u.Title.valid(MaxTitleChars) || u.Description != nil && !u.Description.valid(MaxDescriptionChars) {
			return fmt.Errorf("invalid UI title or description")
		}
		pages := map[string]bool{}
		for _, p := range u.Pages {
			pages[p] = true
		}
		if u.MainPage != "" && !pages[u.MainPage] {
			return fmt.Errorf("main page requires declared page")
		}
		for _, n := range u.Navigation {
			if !pages[n] {
				return fmt.Errorf("navigation requires declared page")
			}
		}
		if e := validateContributions(u, c.Tools); e != nil {
			return e
		}
	}
	return nil
}

var languageTag = regexp.MustCompile(`^[A-Za-z]{2,3}(-[A-Za-z0-9]{2,8}){0,3}$`)

// ValidIconPath reports whether p is a package-relative path of an allowed
// image type. A URL, an absolute path and a traversal never are.
func ValidIconPath(p string) bool {
	return len(p) <= maxAssetPath && IconTypes[strings.ToLower(path.Ext(p))] != "" && relativePath(p)
}

// ValidPlainText reports whether s is non-empty display text of at most max
// characters with no markup, control characters or direction overrides.
func ValidPlainText(s string, max int) bool { return plainText(s, max) }

// ValidName reports whether s is a page, tool or addon identifier.
func ValidName(s string) bool { return namespace.MatchString(s) }

// plainText accepts text that cannot carry markup, control characters or
// direction overrides, within max characters.
func plainText(s string, max int) bool {
	n := utf8.RuneCountInString(s)
	if !utf8.ValidString(s) || n == 0 || n > max || strings.TrimSpace(s) != s {
		return false
	}
	for _, r := range s {
		if r == '<' || r == '>' || unicode.IsControl(r) || unicode.Is(unicode.Cf, r) {
			return false
		}
	}
	return true
}

func (l Localized) valid(max int) bool {
	if l.ByLanguage == nil {
		return plainText(l.Text, max)
	}
	if l.Text != "" || len(l.ByLanguage) == 0 || len(l.ByLanguage) > MaxLanguages {
		return false
	}
	seen := map[string]bool{}
	for tag, text := range l.ByLanguage {
		key := strings.ToLower(tag)
		if len(tag) > 35 || !languageTag.MatchString(tag) || seen[key] || !plainText(text, max) {
			return false
		}
		seen[key] = true
	}
	return true
}

// Default is the text a caller that does not choose a language gets: the
// plain string, else the English entry, else the first tag in sorted order.
func (l Localized) Default() string {
	if l.ByLanguage == nil {
		return l.Text
	}
	if t, ok := l.ByLanguage["en"]; ok {
		return t
	}
	best := ""
	for tag := range l.ByLanguage {
		if best == "" || tag < best {
			best = tag
		}
	}
	return l.ByLanguage[best]
}

// strictJSON rejects duplicate keys as well as unknown fields. The token walk
// also bounds nesting before the standard decoder materializes wire values.
func strictJSON(b []byte, v any, max int64) error {
	if int64(len(b)) > max || len(bytes.TrimSpace(b)) == 0 || bytes.TrimSpace(b)[0] != '{' {
		return fmt.Errorf("invalid or oversized JSON object")
	}
	d := json.NewDecoder(bytes.NewReader(b))
	d.UseNumber()
	if err := walkJSON(d, 0); err != nil {
		return err
	}
	if _, err := d.Token(); err != io.EOF {
		return fmt.Errorf("trailing JSON")
	}
	d = json.NewDecoder(bytes.NewReader(b))
	d.DisallowUnknownFields()
	return d.Decode(v)
}
func walkJSON(d *json.Decoder, depth int) error {
	if depth > 64 {
		return fmt.Errorf("JSON nesting exceeds limit")
	}
	t, err := d.Token()
	if err != nil {
		return err
	}
	delim, ok := t.(json.Delim)
	if !ok {
		return nil
	}
	switch delim {
	case '{':
		seen := map[string]bool{}
		for d.More() {
			key, e := d.Token()
			if e != nil {
				return e
			}
			k, ok := key.(string)
			if !ok || seen[k] {
				return fmt.Errorf("duplicate JSON key")
			}
			seen[k] = true
			if e = walkJSON(d, depth+1); e != nil {
				return e
			}
		}
	case '[':
		for d.More() {
			if e := walkJSON(d, depth+1); e != nil {
				return e
			}
		}
	default:
		return fmt.Errorf("invalid delimiter")
	}
	_, err = d.Token()
	return err
}
