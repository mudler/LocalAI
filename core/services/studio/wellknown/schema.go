// Package wellknown reads and writes the files in a user's home that change how
// LocalAI looks and behaves for that user: persona.json, user.json,
// shortcuts.json, home.json and ui.json in the LocalAI/ folder, and the data
// files that Home widgets read. Each has a JSON Schema. The schemas live in the built-in skill
// localai-workspace, so the server validates with the same text that the
// assistant reads.
package wellknown

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"sort"
	"strings"
	"sync"

	"github.com/google/jsonschema-go/jsonschema"

	"github.com/mudler/LocalAI/core/services/studio/builtinskills"
	"github.com/mudler/LocalAI/core/services/studio/session"
)

// Name names a kind of well-known file.
type Name string

// The kinds of well-known files.
const (
	Persona    Name = "persona"
	User       Name = "user"
	Shortcuts  Name = "shortcuts"
	Home       Name = "home"
	UI         Name = "ui"
	WidgetData Name = "widget-data" // a data file a Home widget reads; it has no fixed path
)

// SkillName is the built-in skill that documents the files and holds the schemas.
const SkillName = "localai-workspace"

// MaxSize caps one well-known file in bytes.
const MaxSize = 64 << 10

// ErrUnknown means the name is not a well-known file.
var ErrUnknown = errors.New("wellknown: not a well-known file")

var names = []Name{Persona, User, Shortcuts, Home, UI, WidgetData}

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
	if errs := check(c, obj); len(errs) > 0 {
		if n == WidgetData {
			errs = append(errs, itemURLErrors(obj)...)
			sort.Strings(errs)
		}
		return nil, errs
	}
	return obj, nil
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

func clip(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
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
