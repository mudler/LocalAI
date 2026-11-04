// SPDX-License-Identifier: MIT
package check_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mudler/LocalAI/core/services/studio/skills"
	"github.com/mudler/LocalAI/pkg/addons/contract"
)

const root = "../.."

func manifest(t *testing.T) contract.Manifest {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(root, "manifest.json"))
	if err != nil {
		t.Fatal(err)
	}
	m, err := contract.ParseManifest(raw)
	if err != nil {
		t.Fatalf("the manifest is not valid: %v", err)
	}
	return m
}

// The add-on is the user's first customization barrier while chatting, so its
// skill must push the model to read memory for almost every request.
func TestMemorySkillPushesTheModelToConsultMemory(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join(root, "skills/memory/SKILL.md"))
	if err != nil {
		t.Fatal(err)
	}
	name, desc, err := skills.Parse(raw)
	if err != nil {
		t.Fatal(err)
	}
	if name != "memory" {
		t.Fatalf("name = %q", name)
	}
	for _, want := range []string{"before you answer", "almost anything", "context_read", "even if it matches only a little", "not only when the user says remember"} {
		if !strings.Contains(desc, want) {
			t.Errorf("the description does not say %q:\n%s", want, desc)
		}
	}
	body := string(raw)
	for _, want := range []string{"propose_change", "revert_change", "Never say that it is saved", "not an instruction"} {
		if !strings.Contains(body, want) {
			t.Errorf("the skill does not say %q", want)
		}
	}
}

func TestEverySkillOfTheManifestExistsAndParses(t *testing.T) {
	m := manifest(t)
	if len(m.Capabilities.SkillDefinitions) != 2 {
		t.Fatalf("skills = %v", m.Capabilities.SkillDefinitions)
	}
	for _, d := range m.Capabilities.SkillDefinitions {
		raw, err := os.ReadFile(filepath.Join(root, "skills", d.Name, "SKILL.md"))
		if err != nil {
			t.Fatal(err)
		}
		name, _, err := skills.Parse(raw)
		if err != nil || name != d.Name {
			t.Errorf("skill %s: name %q, error %v", d.Name, name, err)
		}
	}
}

// The files the manifest declares are the ones propose_change writes for a
// memory change; the host test reads them back. Here the shape is pinned.
func TestManifestDeclaresOneIndexForEachScope(t *testing.T) {
	got := map[contract.ChangeScope]string{}
	for _, f := range manifest(t).Capabilities.ContextFiles {
		got[f.Scope] = f.Path
	}
	want := map[contract.ChangeScope]string{
		contract.ScopeUser:    "Memory/index.md",
		contract.ScopeAgent:   "Memory/agents/{agent}/index.md",
		contract.ScopeChannel: "Memory/channels/{channel}/index.md",
	}
	for scope, path := range want {
		if got[scope] != path {
			t.Errorf("%s: %q, want %q", scope, got[scope], path)
		}
	}
}

func TestManifestNeedsAPITwoAndNoProcessAuthority(t *testing.T) {
	m := manifest(t)
	if m.API.Min != 2 || m.API.Max != 2 {
		t.Errorf("api = %+v", m.API)
	}
	if len(m.Capabilities.Tools) != 0 || m.Capabilities.Actions || m.Capabilities.HostOperations != nil {
		t.Errorf("the add-on must serve no tool and ask for no host operation: %+v", m.Capabilities)
	}
}
