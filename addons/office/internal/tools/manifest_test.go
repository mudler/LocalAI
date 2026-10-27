// SPDX-License-Identifier: MIT
package tools_test

import (
	"encoding/json"
	"os"
	"reflect"
	"testing"

	"github.com/mudler/LocalAI/addons/office/internal/tools"
	"github.com/mudler/LocalAI/pkg/addons/contract"
)

const manifestPath = "../../manifest.json"

// The manifest declares exactly the tools of the runtime, with the
// definitions the runtime builds. Run with UPDATE_MANIFEST=1 to rewrite the
// declared part after a change.
func TestTheManifestDeclaresTheToolsOfTheRuntime(t *testing.T) {
	raw, err := os.ReadFile(manifestPath)
	if err != nil {
		t.Fatal(err)
	}
	var doc map[string]any
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatal(err)
	}
	caps := doc["capabilities"].(map[string]any)
	if os.Getenv("UPDATE_MANIFEST") == "1" {
		caps["tools"] = tools.Tools
		caps["toolDefinitions"] = tools.Definitions()
		caps["actions"] = true
		out, _ := json.MarshalIndent(doc, "", "  ")
		if err := os.WriteFile(manifestPath, append(out, '\n'), 0o644); err != nil {
			t.Fatal(err)
		}
		raw = append(out, '\n')
	}
	m, err := contract.ParseManifest(raw)
	if err != nil {
		t.Fatalf("the manifest does not parse: %v", err)
	}
	if !reflect.DeepEqual(m.Capabilities.Tools, tools.Tools) {
		t.Errorf("manifest tools = %v, want %v", m.Capabilities.Tools, tools.Tools)
	}
	if !m.Capabilities.Actions {
		t.Error("the manifest does not say it has action tools")
	}
	want := tools.Definitions()
	if len(m.Capabilities.ToolDefinitions) != len(want) {
		t.Fatalf("manifest has %d definitions, want %d", len(m.Capabilities.ToolDefinitions), len(want))
	}
	for i, d := range m.Capabilities.ToolDefinitions {
		if d.Name != want[i].Name || d.Kind != want[i].Kind || d.Description != want[i].Description {
			t.Errorf("definition %d differs: %s", i, d.Name)
		}
		var a, b any
		_ = json.Unmarshal(d.InputSchema, &a)
		_ = json.Unmarshal(want[i].InputSchema, &b)
		if !reflect.DeepEqual(a, b) {
			t.Errorf("the schema of %s differs from the runtime", d.Name)
		}
		if err := contract.ValidateToolSchema(d.InputSchema, d.Kind); err != nil {
			t.Errorf("the schema of %s is refused: %v", d.Name, err)
		}
	}
}

func TestOnlyTheEditAndCreateToolsAreActions(t *testing.T) {
	for _, d := range tools.Definitions() {
		wantAction := d.Name[:4] == "edit" || d.Name[:6] == "create"
		if (d.Kind == contract.ToolKindAction) != wantAction {
			t.Errorf("%s has kind %s", d.Name, d.Kind)
		}
	}
}
