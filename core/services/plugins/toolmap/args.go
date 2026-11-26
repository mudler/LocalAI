package toolmap

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"regexp"
	"sort"
	"strings"
)

var (
	// ErrNoLocalTool means a format tool has no LocalAI tool.
	ErrNoLocalTool = errors.New("toolmap: the tool has no LocalAI tool")
	// ErrUnmappedField means an input has a field that LocalAI cannot
	// apply. Running a call with half of a change is worse than not running
	// it, so the whole input fails.
	ErrUnmappedField = errors.New("toolmap: a field does not map to LocalAI")
)

// Call is one LocalAI tool call.
type Call struct {
	Name string
	Args json.RawMessage
}

// droppedFields are the fields that section 11.1 drops on purpose, per
// format tool. "description" of Bash and Task is a label for the person and
// changes nothing in the call.
var droppedFields = map[string][]string{
	"Bash":      {"description"},
	"Grep":      {"-n", "-A", "-B", "-C", "output_mode", "glob", "type", "head_limit", "multiline"},
	"WebFetch":  {"prompt"},
	"WebSearch": {"blocked_domains"},
	"Task":      {"description"},
	"Agent":     {"description"},
}

// input is a JSON object whose fields are taken one by one; what is left at
// the end did not map.
type input struct {
	tool   string
	fields map[string]json.RawMessage
}

func decode(tool string, raw json.RawMessage) (*input, error) {
	in := &input{tool: tool, fields: map[string]json.RawMessage{}}
	if len(bytes.TrimSpace(raw)) == 0 {
		return in, nil
	}
	if err := json.Unmarshal(raw, &in.fields); err != nil {
		return nil, fmt.Errorf("toolmap: %s input is not a JSON object: %w", tool, err)
	}
	if in.fields == nil {
		in.fields = map[string]json.RawMessage{}
	}
	return in, nil
}

// take reads field into dst and removes it. It reports whether the field
// was there.
func (in *input) take(field string, dst any) (bool, error) {
	raw, ok := in.fields[field]
	if !ok {
		return false, nil
	}
	delete(in.fields, field)
	if err := json.Unmarshal(raw, dst); err != nil {
		return true, fmt.Errorf("toolmap: %s.%s: %w", in.tool, field, err)
	}
	return true, nil
}

// copyTo takes field into out[as], keeping its JSON type T.
func copyTo[T any](in *input, out map[string]any, field, as string) error {
	var v T
	ok, err := in.take(field, &v)
	if ok && err == nil {
		out[as] = v
	}
	return err
}

func (in *input) drop(fields ...string) {
	for _, f := range fields {
		delete(in.fields, f)
	}
}

// done fails when a field is left.
func (in *input) done() error {
	if len(in.fields) == 0 {
		return nil
	}
	left := make([]string, 0, len(in.fields))
	for f := range in.fields {
		left = append(left, f)
	}
	sort.Strings(left)
	return fmt.Errorf("%w: %s.%s", ErrUnmappedField, in.tool, strings.Join(left, ", "))
}

func marshal(name string, args map[string]any) (Call, error) {
	b, err := json.Marshal(args)
	return Call{Name: name, Args: b}, err
}

// ToLocal maps a format tool call to LocalAI calls (section 11.1). A
// MultiEdit gives one edit_file call per edit, in order. MCP names, and
// names that are not format tools (LocalAI's own tools, which section 11.2
// passes with their own name), pass unchanged. A format tool that LocalAI
// lacks is ErrNoLocalTool, and a field that does not map is
// ErrUnmappedField.
func ToLocal(name string, raw json.RawMessage) ([]Call, error) {
	if isMCP(name) || !formatTools[name] {
		return []Call{{Name: name, Args: raw}}, nil
	}
	if _, ok := localNames[name]; !ok {
		return nil, fmt.Errorf("%w: %s", ErrNoLocalTool, name)
	}
	in, err := decode(name, raw)
	if err != nil {
		return nil, err
	}
	in.drop(droppedFields[name]...)
	out := map[string]any{}
	local := localNames[name][0]
	var calls []Call
	switch name {
	case "Bash":
		var background bool
		var timeoutMS float64
		err = errors.Join(copyTo[string](in, out, "command", "command"), take(in, "run_in_background", &background), take(in, "timeout", &timeoutMS))
		if background {
			local, out["action"] = Job, "start"
		} else if timeoutMS > 0 {
			out["timeoutSeconds"] = int(math.Ceil(timeoutMS / 1000))
		}
	case "BashOutput":
		out["action"] = "read"
		err = copyTo[string](in, out, "bash_id", "id")
	case "KillShell", "KillBash":
		out["action"] = "kill"
		err = copyTo[string](in, out, "shell_id", "id")
	case "Read":
		err = errors.Join(copyTo[string](in, out, "file_path", "path"), copyTo[int](in, out, "offset", "offset"), copyTo[int](in, out, "limit", "limit"))
	case "Write":
		err = errors.Join(copyTo[string](in, out, "file_path", "path"), copyTo[string](in, out, "content", "content"))
	case "Edit":
		err = errors.Join(copyTo[string](in, out, "file_path", "path"), editFields(in, out))
	case "MultiEdit":
		calls, err = multiEdit(in)
	case "Glob":
		err = errors.Join(copyTo[string](in, out, "pattern", "pattern"), copyTo[string](in, out, "path", "path"))
	case "LS":
		err = copyTo[string](in, out, "path", "path")
	case "Grep":
		var ignoreCase bool
		err = errors.Join(copyTo[string](in, out, "pattern", "query"), copyTo[string](in, out, "path", "path"), take(in, "-i", &ignoreCase))
		// The format's Grep is a regular expression and case sensitive
		// unless -i; LocalAI's search ignores case by default.
		out["regex"], out["ignore_case"] = true, ignoreCase
	case "WebFetch":
		err = copyTo[string](in, out, "url", "url")
	case "WebSearch":
		var domains []string
		err = errors.Join(copyTo[string](in, out, "query", "query"), take(in, "allowed_domains", &domains))
		if len(domains) > 0 {
			out["site"] = domains[0]
		}
	case "Task", "Agent":
		err = errors.Join(copyTo[string](in, out, "prompt", "task"), copyTo[string](in, out, "subagent_type", "agent"))
	case "Skill":
		err = copyTo[string](in, out, "skill", "name")
	}
	if err != nil {
		return nil, err
	}
	if err := in.done(); err != nil {
		return nil, err
	}
	if calls != nil {
		return calls, nil
	}
	c, err := marshal(local, out)
	if err != nil {
		return nil, err
	}
	return []Call{c}, nil
}

func take(in *input, field string, dst any) error {
	_, err := in.take(field, dst)
	return err
}

func editFields(in *input, out map[string]any) error {
	return errors.Join(copyTo[string](in, out, "old_string", "old_string"),
		copyTo[string](in, out, "new_string", "new_string"),
		copyTo[bool](in, out, "replace_all", "replace_all"))
}

func multiEdit(in *input) ([]Call, error) {
	var path string
	var edits []json.RawMessage
	if err := errors.Join(take(in, "file_path", &path), take(in, "edits", &edits)); err != nil {
		return nil, err
	}
	calls := make([]Call, 0, len(edits))
	for i, raw := range edits {
		e, err := decode(fmt.Sprintf("MultiEdit.edits[%d]", i), raw)
		if err != nil {
			return nil, err
		}
		out := map[string]any{"path": path}
		if err := editFields(e, out); err != nil {
			return nil, err
		}
		if err := e.done(); err != nil {
			return nil, err
		}
		c, err := marshal(EditFile, out)
		if err != nil {
			return nil, err
		}
		calls = append(calls, c)
	}
	return calls, nil
}

// formatNames is the reverse name table of section 11.2. job depends on its
// action and is handled apart.
var formatNames = map[string]string{
	Bash: "Bash", ReadFile: "Read", WriteFile: "Write", EditFile: "Edit", ListFiles: "Glob",
	SearchFiles: "Grep", WebFetch: "WebFetch", WebSearch: "WebSearch", SpawnAgent: "Task", LoadSkill: "Skill",
}

// ToFormat maps a LocalAI tool call to the format's name and input, for
// hook payloads (section 11.2). LocalAI arguments that the format has no
// field for (spawn_agent's wait and tools, job's label) are left out. A tool
// without a format name, and a job action other than start, read and kill,
// passes with its own name and arguments, so a matcher "*" or a regular
// expression still sees it. Connector tools also pass unchanged: their
// format name needs the plugin and server (FormatMCPName), which the caller
// knows.
func ToFormat(name string, raw json.RawMessage) (string, json.RawMessage) {
	fname, ok := formatNames[name]
	if name == Job {
		fname, ok = jobFormatName(raw)
	}
	if !ok {
		return name, raw
	}
	var a map[string]json.RawMessage
	if json.Unmarshal(raw, &a) != nil || a == nil {
		return fname, raw
	}
	out := map[string]json.RawMessage{}
	cp := func(from, to string) {
		if v, ok := a[from]; ok {
			out[to] = v
		}
	}
	switch name {
	case Bash:
		cp("command", "command")
		var sec float64
		if json.Unmarshal(a["timeoutSeconds"], &sec) == nil && sec > 0 {
			out["timeout"] = mustJSON(int64(sec * 1000))
		}
	case Job:
		switch fname {
		case "Bash":
			cp("command", "command")
			out["run_in_background"] = mustJSON(true)
		case "BashOutput":
			cp("id", "bash_id")
		case "KillShell":
			cp("id", "shell_id")
		}
	case ReadFile:
		cp("path", "file_path")
		cp("offset", "offset")
		cp("limit", "limit")
	case WriteFile:
		cp("path", "file_path")
		cp("content", "content")
	case EditFile:
		cp("path", "file_path")
		cp("old_string", "old_string")
		cp("new_string", "new_string")
		cp("replace_all", "replace_all")
	case ListFiles:
		cp("path", "path")
		cp("pattern", "pattern")
	case SearchFiles:
		var query string
		var regex bool
		ignoreCase := true // LocalAI's default
		_ = json.Unmarshal(a["query"], &query)
		_ = json.Unmarshal(a["regex"], &regex)
		if v, ok := a["ignore_case"]; ok {
			_ = json.Unmarshal(v, &ignoreCase)
		}
		if !regex {
			query = regexp.QuoteMeta(query)
		}
		out["pattern"] = mustJSON(query)
		out["-i"] = mustJSON(ignoreCase)
		cp("path", "path")
	case WebFetch:
		cp("url", "url")
	case WebSearch:
		cp("query", "query")
		var site string
		if json.Unmarshal(a["site"], &site) == nil && site != "" {
			out["allowed_domains"] = mustJSON([]string{site})
		}
	case SpawnAgent:
		cp("task", "prompt")
		cp("agent", "subagent_type")
	case LoadSkill:
		cp("name", "skill")
	}
	return fname, mustJSON(out)
}

func jobFormatName(raw json.RawMessage) (string, bool) {
	var a struct {
		Action string `json:"action"`
	}
	_ = json.Unmarshal(raw, &a)
	switch a.Action {
	case "start":
		return "Bash", true
	case "read":
		return "BashOutput", true
	case "kill":
		return "KillShell", true
	}
	return "", false
}

func mustJSON(v any) json.RawMessage {
	b, err := json.Marshal(v)
	if err != nil {
		// Only values built in this file reach here; they always marshal.
		panic(err)
	}
	return b
}
