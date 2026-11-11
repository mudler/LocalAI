// Package wellknown reads and writes the files in a user's home that change how
// LocalAI looks and behaves for that user: persona.json, user.json,
// shortcuts.json, setups.json, defaults.json, home.json, ui.json, nav.json and
// commands.json in the LocalAI/ folder,
// and the data files that Home widgets read. Each has a JSON Schema. The
// schemas live in the built-in skill localai-workspace, so the server
// validates with the same text that the assistant reads.
package wellknown

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"path"
	"regexp"
	"slices"
	"sort"
	"strings"
	"sync"

	"github.com/google/jsonschema-go/jsonschema"

	"github.com/mudler/LocalAI/core/services/studio/builtinskills"
	"github.com/mudler/LocalAI/core/services/studio/pages"
	"github.com/mudler/LocalAI/core/services/studio/session"
)

// Name names a kind of well-known file.
type Name string

// The kinds of well-known files.
const (
	Persona    Name = "persona"
	User       Name = "user"
	Shortcuts  Name = "shortcuts"
	Setups     Name = "setups"
	Defaults   Name = "defaults"
	Home       Name = "home"
	UI         Name = "ui"
	Nav        Name = "nav"
	Commands   Name = "commands"
	WidgetData Name = "widget-data" // a data file a Home widget reads; it has no fixed path
)

// SkillName is the built-in skill that documents the files and holds the schemas.
const SkillName = "localai-workspace"

// MaxSize caps one well-known file in bytes.
const MaxSize = 64 << 10

// ErrUnknown means the name is not a well-known file.
var ErrUnknown = errors.New("wellknown: not a well-known file")

var names = []Name{Persona, User, Shortcuts, Setups, Defaults, Home, UI, Nav, Commands, WidgetData}

// Names lists the kinds of well-known files.
func Names() []Name { return append([]Name(nil), names...) }

// Parse returns the Name for s, or ErrUnknown.
func Parse(s string) (Name, error) {
	for _, n := range names {
		if string(n) == s {
			return n, nil
		}
	}
	return "", fmt.Errorf("%w: %q", ErrUnknown, s)
}

// Dir is the folder of the home that holds the settings files. It is visible:
// the user, the settings pages and the assistant edit the same files.
const Dir = "/" + session.SettingsDir

// File is the path of the file in the home, or "" for WidgetData.
func File(n Name) string {
	if n == WidgetData {
		return ""
	}
	return Dir + "/" + string(n) + ".json"
}

type compiled struct {
	raw      []byte
	resolved *jsonschema.Resolved
	props    map[string]*jsonschema.Resolved // each top-level property on its own
}

func resolve(data []byte) (*jsonschema.Resolved, error) {
	var s jsonschema.Schema
	if err := json.Unmarshal(data, &s); err != nil {
		return nil, err
	}
	return s.Resolve(nil)
}

var schemas = sync.OnceValues(func() (map[Name]compiled, error) {
	out := make(map[Name]compiled, len(names))
	for _, n := range names {
		data, err := fs.ReadFile(builtinskills.FS(), SkillName+"/schemas/"+string(n)+".schema.json")
		if err != nil {
			return nil, fmt.Errorf("wellknown: read the schema of %s: %w", n, err)
		}
		r, err := resolve(data)
		if err != nil {
			return nil, fmt.Errorf("wellknown: the schema of %s: %w", n, err)
		}
		var top struct {
			Properties map[string]json.RawMessage `json:"properties"`
		}
		if err := json.Unmarshal(data, &top); err != nil {
			return nil, fmt.Errorf("wellknown: the schema of %s: %w", n, err)
		}
		c := compiled{raw: data, resolved: r, props: make(map[string]*jsonschema.Resolved, len(top.Properties))}
		for k, sub := range top.Properties {
			if c.props[k], err = resolve(sub); err != nil {
				return nil, fmt.Errorf("wellknown: the schema of %s, property %s: %w", n, k, err)
			}
		}
		out[n] = c
	}
	return out, nil
})

func schemaOf(n Name) (compiled, error) {
	all, err := schemas()
	if err != nil {
		return compiled{}, err
	}
	c, ok := all[n]
	if !ok {
		return compiled{}, fmt.Errorf("%w: %q", ErrUnknown, n)
	}
	return c, nil
}

// Schema returns the JSON Schema of a kind.
func Schema(n Name) ([]byte, error) {
	c, err := schemaOf(n)
	if err != nil {
		return nil, err
	}
	return c.raw, nil
}

// Validate parses data and checks it against the schema of n. It returns the
// object and nil, or what is wrong. A file that is not a JSON object is wrong.
func Validate(n Name, data []byte) (map[string]any, []string) {
	c, err := schemaOf(n)
	if err != nil {
		return nil, []string{err.Error()}
	}
	if len(data) > MaxSize {
		return nil, []string{fmt.Sprintf("the file is larger than %d bytes", MaxSize)}
	}
	var obj map[string]any
	if err := json.Unmarshal(data, &obj); err != nil {
		return nil, []string{"the file is not a JSON object: " + err.Error()}
	}
	if obj == nil {
		return nil, []string{"the file is not a JSON object"}
	}
	errs := check(c, obj)
	if n == Home {
		// The schema says the same, but its message names a keyword; these
		// say what to change. They are checked even when the schema passes.
		errs = append(errs, pageWidgetErrors(obj)...)
		errs = append(errs, pageAreaErrors(obj)...)
		errs = append(errs, templateErrors("headline", obj["headline"], identityPlaceholders)...)
	}
	if n == Persona {
		// The schema cannot read a placeholder or follow a path, so these
		// rules live here.
		errs = append(errs, imagePathErrors("avatar", obj["avatar"])...)
		errs = append(errs, templateErrors("greeting", obj["greeting"], identityPlaceholders)...)
		if ec, ok := obj["emptyChat"].(map[string]any); ok {
			errs = append(errs, templateErrors("emptyChat.title", ec["title"], identityPlaceholders)...)
			errs = append(errs, templateErrors("emptyChat.subtitle", ec["subtitle"], identityPlaceholders)...)
		}
	}
	if n == UI {
		// The schema bounds the path of the picture; these say what is wrong
		// with a path that climbs out of the home or is not a picture.
		errs = append(errs, backgroundErrors(obj)...)
		errs = append(errs, stringErrors(obj["strings"])...)
		errs = append(errs, soundErrors(obj["sound"])...)
		errs = append(errs, voiceErrors(obj["voice"])...)
		errs = append(errs, slotErrors(obj["slots"])...)
	}
	if n == Commands {
		// The schema types each command; these say what it cannot: names that
		// repeat or clash with a built-in command, a template that uses an
		// argument it does not list, a key that would set a system prompt.
		errs = append(errs, commandErrors(obj)...)
	}
	if n == Defaults {
		errs = append(errs, defaultsErrors(obj)...)
	}
	if n == Nav {
		// The schema types the fields; these say what is wrong with the
		// list as a whole, in words that tell the writer what to change.
		errs = append(errs, navErrors(obj)...)
	}
	if n == Setups {
		// JSON Schema cannot say that ids are unique or that default names one.
		errs = append(errs, setupErrors(obj)...)
	}
	if len(errs) > 0 {
		if n == WidgetData {
			errs = append(errs, itemURLErrors(obj)...)
		}
		sort.Strings(errs)
		return nil, errs
	}
	return obj, nil
}

// backgroundImageExts are the picture types a background may use.
// SVG is left out on purpose: a picture is data here, never a document.
var backgroundImageExts = []string{".png", ".jpg", ".jpeg", ".webp", ".gif", ".avif"}

// BackgroundImageOK says whether p may be the image of a background: a path of a
// picture inside the user's home, relative and without a leading slash. It
// refuses an absolute path, an address, a backslash, an empty or dot segment
// (so no ..), a control character and any file that is not a picture type.
func BackgroundImageOK(p string) bool {
	if p == "" || len(p) > 400 || strings.HasPrefix(p, "/") || strings.ContainsAny(p, "\\:?#") {
		return false
	}
	if hasControlChar(p) {
		return false
	}
	for _, seg := range strings.Split(p, "/") {
		if seg == "" || seg == "." || seg == ".." {
			return false
		}
	}
	ext := strings.ToLower(path.Ext(p))
	return slices.Contains(backgroundImageExts, ext)
}

// backgroundKeys are the keys of ui.json that hold a background. They share
// one shape, so one rule checks the picture path of each.
var backgroundKeys = []string{"homeBackground", "chatBackground"}

// backgroundErrors says, in words, what is wrong with the image of each
// background.
func backgroundErrors(obj map[string]any) []string {
	var errs []string
	for _, key := range backgroundKeys {
		bg, _ := obj[key].(map[string]any)
		if raw, ok := bg["image"]; ok {
			errs = append(errs, imagePathErrors(key+".image", raw)...)
		}
	}
	return errs
}

// imagePathErrors says, in words, why raw is not the path of a picture in the
// home. A value that is not a string is left to the schema, which names it.
func imagePathErrors(field string, raw any) []string {
	p, isString := raw.(string)
	if !isString || p == "" || BackgroundImageOK(p) {
		return nil
	}
	return []string{fmt.Sprintf("%s: %q is not the path of a picture in your files; use a path without a leading slash and without .., such as Pictures/sky.jpg, ending in .png, .jpg, .jpeg, .webp, .gif or .avif", field, clip(p, 80))}
}

// identityPlaceholders are the placeholders of the greeting, the empty chat
// and the Home headline. The UI fills them; the model never sees them.
var identityPlaceholders = []string{"{user.name}", "{time_of_day}", "{date}"}

// overridableStrings is the allow-list of the text a user may change in
// ui.json strings, with the placeholders each one may use. Text that guards
// a decision (security, permission, consent, trust) is not on it on purpose:
// a file the assistant can write must not be able to reword those prompts.
// The schema lists the same keys; a test keeps the two in step.
var overridableStrings = map[string][]string{
	"home.headline":        identityPlaceholders,
	"composer.placeholder": {"{model}"},
	"chat.empty.title":     nil,
	"nav.newChat":          nil,
	"nav.home":             nil,
	"nav.recentChats":      nil,
	"nav.allChats":         nil,
}

// StringKeys lists the keys of ui.json strings, sorted.
func StringKeys() []string {
	keys := make([]string, 0, len(overridableStrings))
	for k := range overridableStrings {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// StringPlaceholders lists the placeholders the text of key may use.
func StringPlaceholders(key string) []string {
	return append([]string{}, overridableStrings[key]...)
}

// stringErrors says, in words, which text override is not allowed: a key that
// is not on the list, or a text with a placeholder its key does not have.
func stringErrors(raw any) []string {
	m, ok := raw.(map[string]any)
	if !ok {
		return nil
	}
	var out []string
	for k, v := range m {
		allowed, known := overridableStrings[k]
		if !known {
			out = append(out, fmt.Sprintf("strings.%q: this text cannot be changed; the keys that can are %s", clip(k, 60), strings.Join(StringKeys(), ", ")))
			continue
		}
		out = append(out, templateErrors("strings."+k, v, allowed)...)
	}
	return out
}

var placeholderRE = regexp.MustCompile(`\{[^{}]*\}`)

// templateErrors checks the placeholders of a text: each {name} must be one
// of allowed, and no brace may stand alone. A broken placeholder would show
// as raw braces, or as an empty gap, so it is refused rather than shown.
func templateErrors(field string, raw any, allowed []string) []string {
	s, ok := raw.(string)
	if !ok {
		return nil
	}
	var out []string
	for _, ph := range placeholderRE.FindAllString(s, -1) {
		if !slices.Contains(allowed, ph) {
			if len(allowed) == 0 {
				out = append(out, fmt.Sprintf("%s: %s is not a placeholder of this text; it takes none", field, clip(ph, 40)))
			} else {
				out = append(out, fmt.Sprintf("%s: %s is not a placeholder of this text; use %s", field, clip(ph, 40), strings.Join(allowed, ", ")))
			}
		}
	}
	if strings.ContainsAny(placeholderRE.ReplaceAllString(s, ""), "{}") {
		out = append(out, fmt.Sprintf("%s: a brace without its pair; write a placeholder as {name}, with one brace on each side", field))
	}
	return out
}

// setupErrors says, in words, what the schema cannot: two setups with one id,
// and a default that is not the id of a setup. The UI picks a setup by id, so
// either one would make its choice ambiguous or empty.
func setupErrors(obj map[string]any) []string {
	list, _ := obj["setups"].([]any)
	seen := make(map[string]int, len(list))
	var out []string
	for i, it := range list {
		m, _ := it.(map[string]any)
		id, _ := m["id"].(string)
		if id == "" {
			continue
		}
		if j, dup := seen[id]; dup {
			out = append(out, fmt.Sprintf("setups[%d].id: %q is already the id of setups[%d]; give each setup its own id", i, id, j))
			continue
		}
		seen[id] = i
	}
	if d, _ := obj["default"].(string); d != "" {
		if _, ok := seen[d]; !ok {
			out = append(out, fmt.Sprintf("default: %q is not the id of a setup; use one of the ids in setups, or leave default out", clip(d, 80)))
		}
	}
	return out
}

// NavBuiltinIDs are the sidebar entries nav.json can move, rename and hide, in
// the order the sidebar shows them without a nav.json. The server fixes the
// list: an id that is not here is ignored by the sidebar, so a file written
// for a newer or an older version still loads. New chat, Chats, the settings
// entries and the search are not here on purpose: nav.json cannot touch them.
var NavBuiltinIDs = []string{"home", "files", "pages", "agents", "connections", "memory", "skills", "models", "addons"}

// The bounds of nav.json: the whole list, the groups in it and the custom links.
const (
	MaxNavItems  = 60
	MaxNavGroups = 12
	MaxNavLinks  = 30
)

// NavURLOK says whether u may be the address of a custom link: an http or
// https address with a host and no user name or password. The sidebar asks the
// person before it opens one.
func NavURLOK(u string) bool {
	if u == "" || len(u) > 2048 || strings.ContainsAny(u, " \t\n\r\f\v\\") {
		return false
	}
	for _, scheme := range []string{"https://", "http://"} {
		if rest, ok := strings.CutPrefix(u, scheme); ok {
			host, _, _ := strings.Cut(rest, "/")
			host, _, _ = strings.Cut(host, "?")
			host, _, _ = strings.Cut(host, "#")
			return host != "" && !strings.Contains(host, "@")
		}
	}
	return false
}

// navKey names an item of nav.json the way the editor merges lists: a built-in
// by its id, a group by its title, a link by what it points at. "" is an item
// that is none of these.
func navKey(m map[string]any) string {
	if id, ok := m["id"].(string); ok && id != "" {
		return "id:" + id
	}
	if g, ok := m["group"].(string); ok && g != "" {
		return "group:" + g
	}
	if t, ok := m["type"].(string); ok {
		if target, ok := m[t].(string); ok && target != "" {
			return "link:" + t + ":" + target
		}
	}
	return ""
}

// navErrors says, in words, what the schema cannot: an item must be exactly one
// of a built-in entry, a group or a link; a link needs the field of its type;
// no item may appear twice; the list has bounds.
func navErrors(obj map[string]any) []string {
	list, _ := obj["items"].([]any)
	seen := make(map[string]int, len(list))
	var out []string
	groups, links := 0, 0
	for i, it := range list {
		m, ok := it.(map[string]any)
		if !ok {
			continue
		}
		kinds := 0
		for _, k := range []string{"id", "group", "type"} {
			if _, has := m[k]; has {
				kinds++
			}
		}
		if kinds != 1 {
			out = append(out, fmt.Sprintf("items[%d]: an item has exactly one of id (a built-in entry), group (a section title) or type (a link to a page, a chat or an address)", i))
			continue
		}
		if g, has := m["group"]; has && g != "" {
			groups++
		}
		if t, has := m["type"]; has {
			links++
			ts, _ := t.(string)
			target, _ := m[ts].(string)
			if ts == "page" || ts == "chat" || ts == "url" {
				if target == "" {
					out = append(out, fmt.Sprintf("items[%d]: a link of type %q needs %q, the %s it opens", i, ts, ts, map[string]string{"page": "name of a page", "chat": "id of a chat", "url": "http or https address"}[ts]))
					continue
				}
			}
			if ts == "page" && !pages.ValidName(target) {
				out = append(out, pageNameError(fmt.Sprintf("items[%d].page", i), target))
			}
			if ts == "url" && !NavURLOK(target) {
				out = append(out, fmt.Sprintf("items[%d].url: %q is not an http or https address without a user name; a link that opens a site needs one, such as https://localai.io/docs", i, clip(target, 80)))
			}
		}
		key := navKey(m)
		if key == "" {
			continue
		}
		if j, dup := seen[key]; dup {
			out = append(out, fmt.Sprintf("items[%d]: the same entry as items[%d] (%s); list each entry once", i, j, key))
			continue
		}
		seen[key] = i
	}
	if len(list) > MaxNavItems {
		out = append(out, fmt.Sprintf("items: the sidebar takes at most %d items; this list has %d", MaxNavItems, len(list)))
	}
	if groups > MaxNavGroups {
		out = append(out, fmt.Sprintf("items: at most %d groups; this list has %d", MaxNavGroups, groups))
	}
	if links > MaxNavLinks {
		out = append(out, fmt.Sprintf("items: at most %d custom links; this list has %d", MaxNavLinks, links))
	}
	return out
}

// SlotNames are the slots of ui.json, in the order the Slots tab shows them.
var SlotNames = []string{"chatHeader", "sidebarFooter", "composerToolbar"}

// SlotCaps bounds the items of each slot. A slot is a strip about 40 px high
// in a bar that also holds the shell's own controls, so it is small; the
// schema says the same with maxItems, and a test keeps the two in step.
var SlotCaps = map[string]int{"chatHeader": 4, "sidebarFooter": 3, "composerToolbar": 6}

// slotFields are the fields each type of slot item may have.
var slotFields = map[string][]string{
	"page":    {"type", "page", "folder", "label", "icon"},
	"tool":    {"type", "tool", "arguments", "label", "icon"},
	"command": {"type", "command", "label", "icon"},
}

// maxSlotArguments bounds the fixed arguments of a tool button, as JSON.
const maxSlotArguments = 2048

// slotKey names an item the way the Slots tab merges a slot: a page by its
// folder and name, a tool by its name and arguments, a command by its name.
func slotKey(m map[string]any) string {
	switch m["type"] {
	case "page":
		return fmt.Sprintf("page:%v/%v", m["folder"], m["page"])
	case "tool":
		args, _ := json.Marshal(m["arguments"]) // map keys are sorted
		return fmt.Sprintf("tool:%v:%s", m["tool"], args)
	case "command":
		return fmt.Sprintf("command:%v", m["command"])
	}
	return ""
}

// slotErrors says, in words, what the schema cannot: an unknown slot, a slot
// over its cap, an item without the field of its type or with the field of
// another type, the same item twice in a slot, and arguments that are too big.
func slotErrors(raw any) []string {
	slots, ok := raw.(map[string]any)
	if !ok {
		return nil
	}
	var out []string
	for name, v := range slots {
		limit, known := SlotCaps[name]
		if !known {
			out = append(out, fmt.Sprintf("slots.%s: not a slot; the slots are %s", clip(name, 60), strings.Join(SlotNames, ", ")))
			continue
		}
		list, ok := v.([]any)
		if !ok {
			continue
		}
		if len(list) > limit {
			out = append(out, fmt.Sprintf("slots.%s: this slot takes at most %d items; it has %d, so remove %d", name, limit, len(list), len(list)-limit))
		}
		seen := map[string]int{}
		for i, it := range list {
			m, ok := it.(map[string]any)
			if !ok {
				continue
			}
			at := fmt.Sprintf("slots.%s[%d]", name, i)
			ty, _ := m["type"].(string)
			fields, known := slotFields[ty]
			if !known {
				continue // the schema names the type that is wrong
			}
			for k := range m {
				if !slices.Contains(fields, k) {
					out = append(out, fmt.Sprintf("%s: an item of type %s has no %s; it takes %s", at, ty, clip(k, 40), strings.Join(fields, ", ")))
				}
			}
			switch ty {
			case "page":
				if p, _ := m["page"].(string); p == "" {
					out = append(out, fmt.Sprintf("%s.page: a page tile names its page, such as \"weather\" for the folder pages/weather/ of your home", at))
				}
			case "tool":
				if t, _ := m["tool"].(string); t == "" {
					out = append(out, fmt.Sprintf("%s.tool: a tool button names the tool it runs", at))
				}
				if l, _ := m["label"].(string); strings.TrimSpace(l) == "" {
					out = append(out, fmt.Sprintf("%s.label: a tool button needs the text it shows, such as \"Summarize chat\"", at))
				}
				if args, has := m["arguments"]; has {
					if b, err := json.Marshal(args); err == nil && len(b) > maxSlotArguments {
						out = append(out, fmt.Sprintf("%s.arguments: at most %d bytes of JSON; these are %d", at, maxSlotArguments, len(b)))
					}
				}
			case "command":
				if c, _ := m["command"].(string); c == "" {
					out = append(out, fmt.Sprintf("%s.command: a command item names the command, without the slash", at))
				}
			}
			key := slotKey(m)
			if j, dup := seen[key]; dup {
				out = append(out, fmt.Sprintf("%s: the same item as slots.%s[%d]; list each item once in a slot", at, name, j))
				continue
			}
			seen[key] = i
		}
	}
	return out
}

// MaxPageWidgets bounds the page widgets of home.json: each one runs its page
// in a frame of its own.
const MaxPageWidgets = 3

// pageWidgetErrors says, in words, what is wrong with the page widgets of
// home.json: too many of them, or a page that is not a page name.
func pageWidgetErrors(obj map[string]any) []string {
	widgets, _ := obj["widgets"].([]any)
	var out []string
	count := 0
	for i, it := range widgets {
		m, _ := it.(map[string]any)
		if m["type"] != "page" {
			continue
		}
		count++
		raw, ok := m["page"]
		name, isString := raw.(string)
		switch {
		case !ok || (isString && name == ""):
			out = append(out, fmt.Sprintf("widgets[%d].page: a page widget names its page, such as \"news\" for the folder pages/news/ of your home", i))
		case !isString || !pages.ValidName(name):
			out = append(out, pageNameError(fmt.Sprintf("widgets[%d].page", i), raw))
		}
	}
	if count > MaxPageWidgets {
		out = append(out, fmt.Sprintf("widgets: Home shows at most %d page widgets, because each one runs its page; this layout has %d, so remove %d", MaxPageWidgets, count, count-MaxPageWidgets))
	}
	return out
}

// pageAreaErrors says, in words, what is wrong with the pageArea of home.json:
// null (or no key) shows no page area; a page name shows a page of your home;
// {page, folder} shows a page of a shared folder.
func pageAreaErrors(obj map[string]any) []string {
	raw, ok := obj["pageArea"]
	if !ok || raw == nil {
		return nil
	}
	const hint = "pageArea names a page: a string such as \"today\" for the folder pages/today/ of your home, {\"page\": \"today\", \"folder\": 3} for a page of a shared folder, or null for no page area"
	switch v := raw.(type) {
	case string:
		if !pages.ValidName(v) {
			return []string{pageNameError("pageArea", v)}
		}
	case map[string]any:
		name, isString := v["page"].(string)
		if !isString || !pages.ValidName(name) {
			return []string{pageNameError("pageArea.page", v["page"])}
		}
		if f, has := v["folder"]; has {
			n, isNum := f.(float64)
			if !isNum || n < 1 || n != float64(int64(n)) {
				return []string{"pageArea.folder: the folder_id of a shared folder, a whole number from 1; leave it out for a page of your home"}
			}
		}
		for k := range v {
			if k != "page" && k != "folder" {
				return []string{fmt.Sprintf("pageArea.%s: no such key; %s", clip(k, 40), hint)}
			}
		}
	default:
		return []string{"pageArea: " + hint}
	}
	return nil
}

// itemURLErrors says, in words, which item url of widget data breaks the
// schema: its pattern alone tells a trigger run nothing it can fix.
func itemURLErrors(obj map[string]any) []string {
	items, _ := obj["items"].([]any)
	var out []string
	for i, it := range items {
		m, _ := it.(map[string]any)
		raw, ok := m["url"]
		if !ok {
			continue
		}
		u, isString := raw.(string)
		if isString && ItemURLOK(u) {
			continue
		}
		out = append(out, fmt.Sprintf("items[%d].url: a link is an http or https address without a user name (https://example.com/a) or a path of the app such as /app/pages/news; %q is neither", i, clip(fmt.Sprint(raw), 80)))
	}
	return out
}

// The in-app paths an item may link to: the screens a person reads.
var appPrefixes = []string{"chat", "chats", "files", "pages", "agents", "skills", "triggers", "collections", "models", "account", "addons"}

// ItemURLOK says whether u may be the url of a widget item. It is the rule of
// the schema in code, for the error message and the tests.
func ItemURLOK(u string) bool {
	if u == "" || len(u) > 2048 || strings.ContainsAny(u, " \t\n\r\f\v") {
		return false
	}
	if rest, ok := strings.CutPrefix(u, "/app/"); ok {
		if strings.Contains(u, "..") || strings.Contains(u, "//") || strings.Contains(u, `\`) {
			return false
		}
		for _, p := range appPrefixes {
			if r, ok := strings.CutPrefix(rest, p); ok && (r == "" || strings.ContainsAny(r[:1], "/?#")) {
				return true
			}
		}
		return false
	}
	for _, scheme := range []string{"https://", "http://"} {
		if rest, ok := strings.CutPrefix(u, scheme); ok {
			host, _, _ := strings.Cut(rest, "/")
			host, _, _ = strings.Cut(host, "?")
			host, _, _ = strings.Cut(host, "#")
			return host != "" && !strings.ContainsAny(host, `@\`)
		}
	}
	return false
}

// hasControlChar says whether s holds an ASCII control character (C0 or DEL).
func hasControlChar(s string) bool {
	return strings.ContainsFunc(s, func(r rune) bool { return r < 0x20 || r == 0x7f })
}

// pageNameError says that v, at field, is not a page name.
func pageNameError(field string, v any) string {
	return fmt.Sprintf("%s: %q is not a page name; a page name is the folder name under pages/: lowercase letters, digits and dashes, at most 64", field, clip(fmt.Sprint(v), 80))
}

func clip(s string, n int) string {
	if len(s) <= n {
		return s
	}
	// Cut on a rune boundary: the text goes into a JSON error string.
	return strings.ToValidUTF8(s[:n], "") + "..."
}

// check validates obj, and when it fails, each top-level property on its own,
// so that the answer names every property that is wrong and not only the first.
func check(c compiled, obj map[string]any) []string {
	err := c.resolved.Validate(obj)
	if err == nil {
		return nil
	}
	var errs []string
	for k, v := range obj {
		sub := c.props[k]
		if sub == nil {
			continue
		}
		if e := sub.Validate(v); e != nil {
			errs = append(errs, k+": "+clean(e))
		}
	}
	if len(errs) == 0 {
		errs = append(errs, clean(err))
	}
	sort.Strings(errs)
	return errs
}

func clean(err error) string {
	return strings.TrimPrefix(err.Error(), "validating root: ")
}
