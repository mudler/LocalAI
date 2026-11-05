// Package providerconformance is the test every provider add-on of the notes
// slot must pass. It checks the manifest against the host's descriptor and the
// behaviour of the role tools against what the host expects when it calls them
// by itself: the context at the start of a turn, the export and the forget.
//
// An add-on author calls Run from a Go test with the manifest and a function
// that runs a tool of the add-on (the add-on's handler, or a client of a test
// instance). The package is part of the contract: a provider that passes it
// works with the host's circuit breaker, size cap and wrapping.
package providerconformance

import (
	"context"
	"encoding/json"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/mudler/LocalAI/pkg/addons/contract"
)

// Slot is the slot the suite tests.
const Slot = "memory"

// CallFunc runs one tool of the add-on with JSON arguments and returns the JSON
// payload of the answer.
type CallFunc func(ctx context.Context, tool string, args json.RawMessage) (json.RawMessage, error)

// Subject is a provider under test.
type Subject struct {
	// Manifest is the manifest.json of the add-on.
	Manifest []byte
	// Call runs a tool of the add-on.
	Call CallFunc
	// ContextBudget is the byte budget the suite offers the context tool.
	// Zero is 512.
	ContextBudget int
	// Timeout is the time a tool may take. The host waits 3 seconds for the
	// context. Zero is that.
	Timeout time.Duration
}

// Run runs the suite as subtests of t.
func Run(t *testing.T, s Subject) {
	t.Helper()
	m, decl := checkManifest(t, s)
	if t.Failed() {
		return
	}
	budget := s.ContextBudget
	if budget <= 0 {
		budget = 512
	}
	timeout := s.Timeout
	if timeout <= 0 {
		timeout = 3 * time.Second
	}
	call := func(t *testing.T, tool string, args any) json.RawMessage {
		t.Helper()
		raw, err := json.Marshal(args)
		if err != nil {
			t.Fatal(err)
		}
		ctx, cancel := context.WithTimeout(context.Background(), timeout)
		defer cancel()
		start := time.Now()
		out, err := s.Call(ctx, tool, raw)
		if err != nil {
			t.Fatalf("tool %s failed: %v", tool, err)
		}
		if time.Since(start) > timeout {
			t.Fatalf("tool %s took longer than %s", tool, timeout)
		}
		if !json.Valid(out) {
			t.Fatalf("tool %s did not answer JSON", tool)
		}
		return out
	}
	// argsFor offers a tool only the arguments its schema declares, as the host does.
	argsFor := func(tool string, all map[string]any) map[string]any {
		out := map[string]any{}
		for _, d := range m.Capabilities.ToolDefinitions {
			if d.Name != tool {
				continue
			}
			var sc struct {
				Properties map[string]json.RawMessage `json:"properties"`
			}
			_ = json.Unmarshal(d.InputSchema, &sc)
			for k, v := range all {
				if _, ok := sc.Properties[k]; ok {
					out[k] = v
				}
			}
		}
		return out
	}

	if tools := decl.Roles["context"]; len(tools) > 0 {
		t.Run("context answers text within the budget", func(t *testing.T) {
			out := call(t, tools[0], argsFor(tools[0], map[string]any{
				"query": "what do you know about me?", "scopes": []string{"user"}, "budgetBytes": budget, "origin": "studio", "userRef": "0123456789abcdef"}))
			text, ok := textOf(out)
			if !ok {
				t.Fatalf("the context answer must be a JSON string or an object with a text: %s", out)
			}
			if len(text) > budget {
				t.Fatalf("the context text has %d bytes and the budget is %d", len(text), budget)
			}
		})
		t.Run("context copes with an empty message and no scope", func(t *testing.T) {
			out := call(t, tools[0], argsFor(tools[0], map[string]any{"query": "", "budgetBytes": budget}))
			if _, ok := textOf(out); !ok {
				t.Fatalf("the context answer must carry a text, which may be empty: %s", out)
			}
		})
		t.Run("context does not require a resource", func(t *testing.T) {
			for _, d := range m.Capabilities.ToolDefinitions {
				if d.Name == tools[0] && strings.Contains(string(d.InputSchema), `"required"`) {
					var sc struct {
						Required []string `json:"required"`
					}
					_ = json.Unmarshal(d.InputSchema, &sc)
					if slices.Contains(sc.Required, contract.ResourceArgument) {
						t.Fatalf("the host calls the context for a turn that names no resource")
					}
				}
			}
		})
	}
	if tools := decl.Roles["export"]; len(tools) > 0 {
		t.Run("export answers JSON, with files when it has them", func(t *testing.T) {
			out := call(t, tools[0], argsFor(tools[0], map[string]any{}))
			var doc struct {
				Files []struct{ Path, Content string } `json:"files"`
			}
			if err := json.Unmarshal(out, &doc); err != nil {
				t.Fatalf("the export must be a JSON object: %v", err)
			}
			for _, f := range doc.Files {
				if f.Path == "" || strings.HasPrefix(f.Path, "/") || strings.Contains(f.Path, "..") {
					t.Fatalf("an exported path must be relative and stay inside the archive: %q", f.Path)
				}
			}
		})
	}
	if tools := decl.Roles["read"]; len(tools) > 0 {
		t.Run("read tools answer JSON", func(t *testing.T) {
			for _, tool := range tools {
				call(t, tool, argsFor(tool, map[string]any{"query": "tea"}))
			}
		})
	}
	if tools := decl.Roles["forget"]; len(tools) > 0 {
		t.Run("forget answers JSON", func(t *testing.T) {
			call(t, tools[0], argsFor(tools[0], map[string]any{"group": "agent", "key": "none-such"}))
		})
	}
	t.Run("a tool that does not exist is refused", func(t *testing.T) {
		if _, err := s.Call(context.Background(), "no-such-tool", json.RawMessage(`{}`)); err == nil {
			t.Fatal("an unknown tool must fail")
		}
	})
}

// checkManifest runs the manifest rules and returns the declaration of the slot.
func checkManifest(t *testing.T, s Subject) (contract.Manifest, contract.ProviderDeclaration) {
	t.Helper()
	var m contract.Manifest
	var decl contract.ProviderDeclaration
	t.Run("the manifest is valid and declares the slot", func(t *testing.T) {
		var err error
		if m, err = contract.ParseManifest(s.Manifest); err != nil {
			t.Fatalf("manifest: %v", err)
		}
		host := contract.HostCompatibility{Host: 1, API: contract.HostAPIVersion, Protocol: 1, SDK: contract.HostSDKVersion, DataFormat: 1, FeatureLevel: contract.HostFeatureLevel, OS: m.OS, Arch: m.Arch}
		if err := contract.ValidateManifest(m, host); err != nil {
			t.Fatalf("the host refuses the manifest: %v", err)
		}
		i := slices.IndexFunc(m.Capabilities.Provides, func(d contract.ProviderDeclaration) bool { return d.Slot == Slot })
		if i < 0 {
			t.Fatalf("capabilities.provides must declare the slot %q", Slot)
		}
		decl = m.Capabilities.Provides[i]
		if remote, _ := decl.Properties["remote"].(bool); remote && m.Capabilities.Network == nil {
			t.Fatalf("a provider with remote true must declare its network access")
		}
		for role, tools := range decl.Roles {
			for _, tool := range tools {
				if !slices.Contains(m.Capabilities.Tools, tool) {
					t.Fatalf("role %s names %s, which is not a declared tool", role, tool)
				}
			}
		}
	})
	if decl.Slot == "" {
		t.Fatalf("no declaration of the slot")
	}
	return m, decl
}

// textOf reads the text of a context answer: a JSON string or an object with a
// text.
func textOf(raw json.RawMessage) (string, bool) {
	var s string
	if json.Unmarshal(raw, &s) == nil {
		return s, true
	}
	var o struct {
		Text *string `json:"text"`
	}
	if json.Unmarshal(raw, &o) == nil && o.Text != nil {
		return *o.Text, true
	}
	return "", false
}
