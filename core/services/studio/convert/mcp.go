package convert

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"path"
	"sort"
	"strings"
	"unicode/utf8"
)

// oldMCP is one MCP server of an old agent.
type oldMCP struct {
	transport string // "http" or "stdio"
	url       string
	token     string
	command   string
	args      []string
	env       map[string]string
	label     string
}

// readMCP lists the MCP servers of an old agent configuration: remote servers
// (mcp_servers: url and token) and local commands (mcp_stdio_servers: a list
// of cmd, args and env as "K=V" lines, or the JSON text of {"mcpServers": ...}).
func readMCP(cfg map[string]any) []oldMCP {
	var out []oldMCP
	if list, ok := cfg["mcp_servers"].([]any); ok {
		for _, x := range list {
			m, _ := x.(map[string]any)
			e := oldMCP{transport: "http", url: str(m, "url"), token: str(m, "token")}
			e.label = e.url
			if u, err := url.Parse(e.url); err == nil && u.Hostname() != "" {
				e.label = u.Hostname()
			}
			out = append(out, e)
		}
	}
	return append(out, readStdio(cfg["mcp_stdio_servers"])...)
}

func readStdio(v any) []oldMCP {
	if s, ok := v.(string); ok {
		var parsed any
		if json.Unmarshal([]byte(s), &parsed) != nil {
			return nil
		}
		if m, ok := parsed.(map[string]any); ok {
			if servers, ok := m["mcpServers"].(map[string]any); ok {
				names := make([]string, 0, len(servers))
				for n := range servers {
					names = append(names, n)
				}
				sort.Strings(names)
				var list []any
				for _, n := range names {
					if sm, ok := servers[n].(map[string]any); ok {
						sm["name"] = n
						list = append(list, sm)
					}
				}
				return readStdio(list)
			}
		}
		return readStdio(parsed)
	}
	list, _ := v.([]any)
	var out []oldMCP
	for _, x := range list {
		m, _ := x.(map[string]any)
		cmd := str(m, "cmd")
		if cmd == "" {
			cmd = str(m, "command")
		}
		e := oldMCP{transport: "stdio", command: cmd, args: stringList(m["args"]), env: map[string]string{}}
		switch env := m["env"].(type) {
		case []any:
			for _, kv := range stringList(env) {
				if k, val, ok := strings.Cut(kv, "="); ok && k != "" {
					e.env[k] = val
				}
			}
		case map[string]any:
			for k, val := range env {
				if s, ok := val.(string); ok {
					e.env[k] = s
				}
			}
		}
		e.label = str(m, "name")
		if e.label == "" {
			e.label = path.Base(cmd)
		}
		out = append(out, e)
	}
	return out
}

// mcpConnectors makes a switched-off connector of the owner for each MCP server
// of an old agent and returns their IDs, for the agent to name. A local command
// converts only for an administrator. The second result is false when one could
// not be made for a reason that may pass (the agent then waits for the next run,
// because its grants cannot be added later).
func (r *run) mcpConnectors(ctx context.Context, owner string, a oldAgent) ([]string, bool) {
	var ids []string
	allOK := true
	admin, adminErr := false, error(nil)
	for i, e := range readMCP(a.cfg) {
		key := fmt.Sprintf("mcp|%s|%s|%d", a.user, a.name, i)
		name := fmt.Sprintf("%s: MCP server %d", a.name, i+1) // no address: a report holds no value
		if r.handled(ctx, key, KindConnector, a.user, name) {
			if ref, _ := r.done(ctx, key); ref != refDropped && ref != refExists {
				ids = append(ids, ref)
			}
			continue
		}
		switch {
		case e.transport == "http" && e.url == "":
			r.drop(ctx, key, KindConnector, a.user, name, "the server has no URL")
			continue
		case e.transport == "stdio" && e.command == "":
			r.drop(ctx, key, KindConnector, a.user, name, "the local command has no command")
			continue
		case e.transport == "stdio":
			if admin, adminErr = r.tg.IsAdmin(ctx, owner); adminErr != nil {
				r.add(KindConnector, a.user, name, StatusFailed, "could not check whether the owner is an administrator: "+adminErr.Error())
				allOK = false
				continue
			}
			if !admin {
				r.drop(ctx, key, KindConnector, a.user, name, "a local command needs an administrator: make it again in the Connectors page if you are one")
				continue
			}
		}
		in := MCPIn{Transport: e.transport, URL: e.url, Token: e.token, Command: e.command, Args: e.args, Env: e.env}
		base := fmt.Sprintf("%s MCP %s", a.name, e.label)
		id, _, err := r.createMCP(ctx, owner, base, in)
		if err != nil {
			if errors.Is(err, ErrInvalid) {
				r.drop(ctx, key, KindConnector, a.user, name, strings.TrimPrefix(err.Error(), ErrInvalid.Error()+": "))
				continue
			}
			r.add(KindConnector, a.user, name, StatusFailed, err.Error())
			allOK = false
			continue
		}
		r.mark(ctx, key, id)
		ids = append(ids, id)
		r.add(KindConnector, a.user, name, StatusConverted, "switched off; check it and turn it on in Connectors")
	}
	return ids, allOK
}

// createMCP makes the connector, adding a number to the name when it is taken.
func (r *run) createMCP(ctx context.Context, owner, base string, in MCPIn) (id, name string, err error) {
	base = truncateRunes(base, 70)
	for n := 1; n <= 9; n++ {
		in.Name = base
		if n > 1 {
			in.Name = fmt.Sprintf("%s (%d)", base, n)
		}
		if id, err = r.tg.CreateMCPConnector(ctx, owner, in); err == nil {
			return id, in.Name, nil
		}
		if !errors.Is(err, ErrNameTaken) {
			return "", "", err
		}
	}
	return "", "", err
}

func truncateRunes(s string, n int) string {
	if utf8.RuneCountInString(s) <= n {
		return s
	}
	return string([]rune(s)[:n])
}
