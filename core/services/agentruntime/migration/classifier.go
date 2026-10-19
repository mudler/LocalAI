// SPDX-License-Identifier: MIT
package migration

import (
	"encoding/json"
	"fmt"
	"strconv"
)

// Public paths are built only from schema constants and ordinal positions.
// Exact paths and raw values belong exclusively to the private definition.
type classifier struct {
	report  *Report
	legacy  bool
	ref     string
	def     *definition
	unknown int
}

func (c *classifier) find(path, exact, disposition string) {
	code, remediation := "unsupported_field", "Preserve privately; review before restoring."
	switch disposition {
	case "mapped":
		c.report.Mapped++
		code, remediation = "mapped_metadata", "Metadata only; no execution authority."
	case "invalid":
		c.report.Invalid++
		code, remediation = "invalid_field", "Correct the source before conversion."
	default:
		c.report.Unsupported++
	}
	c.report.Findings = append(c.report.Findings, Finding{Definition: c.ref, Path: path, Code: code, Disposition: disposition, Remediation: remediation})
	if c.def != nil && disposition != "mapped" {
		// A validation error may refer to the same supplied path more than once.
		for _, p := range c.def.Unsupported {
			if p == exact {
				return
			}
		}
		c.def.Unsupported = append(c.def.Unsupported, exact)
	}
}

func (c *classifier) unknownChildren(path, exact string, value any) {
	switch v := value.(type) {
	case map[string]any:
		for i, k := range keys(v) {
			p := fmt.Sprintf("%s.unknown[%d]", path, i+1)
			e := exact + "." + k
			c.find(p, e, "unsupported")
			c.unknownChildren(p, e, v[k])
		}
	case []any:
		for i, item := range v {
			p := fmt.Sprintf("%s[%d]", path, i)
			e := fmt.Sprintf("%s[%d]", exact, i)
			c.find(p, e, "unsupported")
			c.unknownChildren(p, e, item)
		}
	}
}

func stringsOnly(value any) bool {
	if value == nil {
		return true
	}
	list, ok := value.([]any)
	if !ok {
		return false
	}
	for _, item := range list {
		if _, ok := item.(string); !ok {
			return false
		}
	}
	return true
}
func (c *classifier) valid(kind, field string, value any) bool {
	switch kind {
	case "string":
		_, ok := value.(string)
		return ok
	case "bool":
		_, ok := value.(bool)
		return ok
	case "cancel":
		if value == nil {
			return true
		}
		_, ok := value.(bool)
		return ok
	case "list":
		if field == "allowed_tools" || field == "excluded_tools" {
			if _, ok := value.(string); ok {
				return true
			}
		}
		return stringsOnly(value)
	case "int":
		var text string
		switch v := value.(type) {
		case json.Number:
			text = string(v)
		case string:
			if !c.legacy {
				return false
			}
			switch field {
			case "max_evaluation_loops", "max_attempts", "required_tool_before_finish_attempts", "parallel_jobs", "kb_results":
				text = v
			default:
				return false
			}
		default:
			return false
		}
		// ParseInt enforces exact integer syntax and int64 bounds without floats.
		_, err := strconv.ParseInt(text, 10, 64)
		return err == nil
	}
	return false
}

func (c *classifier) field(field string, value any) {
	c.def.Presence[field] = raw(value)
	kind, known := catalog[field]
	if !known {
		c.unknown++
		p := fmt.Sprintf("unknown[%d]", c.unknown)
		c.find(p, field, "unsupported")
		c.unknownChildren(p, field, value)
		return
	}
	if kind == "integration" {
		c.integrations(field, value)
		return
	}
	if kind == "stdio" {
		c.stdio(field, value)
		return
	}
	disposition := "unsupported"
	if !c.valid(kind, field, value) {
		disposition = "invalid"
	} else {
		switch field {
		case "name", "description", "model", "api_url", "api_key", "system_prompt":
			disposition = "mapped"
			c.def.Normalized[field] = value.(string)
		}
	}
	c.find(field, field, disposition)
	c.unknownChildren(field, field, value)
}

func (c *classifier) integrations(field string, value any) {
	if value == nil {
		c.find(field, field, "unsupported")
		return
	}
	list, ok := value.([]any)
	if !ok {
		c.find(field, field, "invalid")
		c.unknownChildren(field, field, value)
		return
	}
	c.find(field, field, "unsupported")
	for i, item := range list {
		path := fmt.Sprintf("%s[%d]", field, i)
		m, ok := item.(map[string]any)
		if !ok {
			c.find(path, path, "invalid")
			c.unknownChildren(path, path, item)
			continue
		}
		expected := "type"
		if field == "actions" && c.legacy {
			expected = "name"
		}
		schema := map[string]string{expected: "string", "config": "string"}
		if field == "mcp_servers" {
			schema = map[string]string{"url": "string", "token": "string"}
			expected = "url"
		}
		if _, ok := m[expected].(string); !ok {
			c.find(path+"."+expected, path+"."+expected, "invalid")
		}
		if field == "actions" {
			alias := "name"
			if c.legacy {
				alias = "type"
			}
			if _, ok := m[alias]; ok {
				schema[alias] = "invalid"
			}
		}
		c.properties(path, path, m, schema, false)
	}
}

func (c *classifier) properties(path, exact string, m map[string]any, schema map[string]string, wrapper bool) {
	unknown := 0
	for _, k := range keys(m) {
		value := m[k]
		p, e := path+"."+k, exact+"."+k
		kind, known := schema[k]
		if !known {
			unknown++
			p = fmt.Sprintf("%s.unknown[%d]", path, unknown)
			c.find(p, e, "unsupported")
			c.unknownChildren(p, e, value)
			continue
		}
		good := false
		switch kind {
		case "string":
			_, good = value.(string)
		case "strings":
			good = stringsOnly(value)
		case "env":
			if wrapper {
				env, ok := value.(map[string]any)
				good = ok || value == nil
				if !ok {
					// Invalid shapes still need complete private accounting.
					c.unknownChildren(p, e, value)
				}
				for i, key := range keys(env) {
					ep := fmt.Sprintf("%s.unknown[%d]", p, i+1)
					ee := e + "." + key
					disposition := "unsupported"
					if _, ok := env[key].(string); !ok {
						good = false
						disposition = "invalid"
					}
					c.find(ep, ee, disposition)
					c.unknownChildren(ep, ee, env[key])
				}
			} else {
				good = stringsOnly(value)
			}
		}
		disposition := "unsupported"
		if !good {
			disposition = "invalid"
		}
		c.find(p, e, disposition)
		// Env object keys were already visited with redacted names above.
		if kind != "env" || !wrapper {
			c.unknownChildren(p, e, value)
		}
	}
}

func (c *classifier) stdio(field string, value any) {
	if value == nil {
		c.find(field, field, "unsupported")
		return
	}
	if text, ok := value.(string); ok {
		parsed, err := strictJSON([]byte(text))
		if err != nil {
			c.find(field, field, "invalid")
			return
		}
		root, ok := parsed.(map[string]any)
		if !ok {
			c.find(field, field, "invalid")
			return
		}
		c.find(field, field, "unsupported")
		servers, ok := root["mcpServers"].(map[string]any)
		if !ok {
			c.find(field+".mcpServers", field+".mcpServers", "invalid")
		}
		unknown := 0
		for _, k := range keys(root) {
			if k == "mcpServers" {
				continue
			}
			unknown++
			p := fmt.Sprintf("%s.unknown[%d]", field, unknown)
			e := field + "." + k
			c.find(p, e, "unsupported")
			c.unknownChildren(p, e, root[k])
		}
		path := field + ".mcpServers"
		c.find(path, path, "unsupported")
		for i, k := range keys(servers) {
			p := fmt.Sprintf("%s.unknown[%d]", path, i+1)
			c.server(p, path+"."+k, servers[k], true)
		}
		return
	}
	list, ok := value.([]any)
	if !ok {
		c.find(field, field, "invalid")
		c.unknownChildren(field, field, value)
		return
	}
	c.find(field, field, "unsupported")
	for i, item := range list {
		p := fmt.Sprintf("%s[%d]", field, i)
		c.server(p, p, item, false)
	}
}
func (c *classifier) server(path, exact string, value any, wrapper bool) {
	m, ok := value.(map[string]any)
	if !ok {
		c.find(path, exact, "invalid")
		c.unknownChildren(path, exact, value)
		return
	}
	c.find(path, exact, "unsupported")
	command := "cmd"
	schema := map[string]string{"name": "string", "cmd": "string", "args": "strings", "env": "env"}
	if wrapper {
		command = "command"
		schema = map[string]string{"command": "string", "args": "strings", "env": "env"}
		if !c.legacy {
			schema["cmd"] = "string"
			if _, ok := m["cmd"]; ok {
				command = "cmd"
			}
		}
	}
	_, hasCmd := m["cmd"]
	_, hasCommand := m["command"]
	if hasCmd && hasCommand {
		c.find(path+".cmd", exact+".cmd", "invalid")
	}
	if !wrapper && hasCommand {
		schema["command"] = "invalid"
	}
	if wrapper && c.legacy && hasCmd {
		schema["cmd"] = "invalid"
	}
	if _, ok := m[command].(string); !ok {
		c.find(path+"."+command, exact+"."+command, "invalid")
	}
	c.properties(path, exact, m, schema, wrapper)
}
