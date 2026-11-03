// Package providerguard reads the shipped add-ons so tests can check that core
// names none of the ones that provide a generic role. Only tests import it.
package providerguard

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// RepoRoot is the module root, found from this package's directory.
func RepoRoot() (string, error) {
	dir, err := os.Getwd()
	if err != nil {
		return "", err
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir, nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", fmt.Errorf("go.mod not found above %s", dir)
		}
		dir = parent
	}
}

type manifest struct {
	ID           string `json:"id"`
	Capabilities struct {
		ToolDefinitions  []struct{ Shadows string } `json:"toolDefinitions"`
		SkillDefinitions []struct{ Shadows string } `json:"skillDefinitions"`
		Connector        json.RawMessage            `json:"connector"`
		ExecutionProv    json.RawMessage            `json:"executionProvider"`
		UI               struct {
			Title json.RawMessage `json:"title"`
		} `json:"ui"`
	} `json:"capabilities"`
}

// Names reads addons/*/manifest.json and returns the names of the
// add-ons that provide a generic role (they shadow a built-in tool or skill,
// serve as a connector, or declare the execution provider): the capitalised
// id and every declared title. The other add-ons (voice, face, tables ...)
// are ordinary words of the model features that core owns.
func Names() ([]string, error) {
	root, err := RepoRoot()
	if err != nil {
		return nil, err
	}
	files, err := filepath.Glob(filepath.Join(root, "addons", "*", "manifest.json"))
	if err != nil || len(files) == 0 {
		return nil, fmt.Errorf("no add-on manifests found: %v", err)
	}
	set := map[string]bool{}
	for _, f := range files {
		raw, err := os.ReadFile(f)
		if err != nil {
			return nil, err
		}
		var m manifest
		if json.Unmarshal(raw, &m) != nil || m.ID == "" {
			continue
		}
		role := len(m.Capabilities.Connector) > 0 && string(m.Capabilities.Connector) != "null"
		role = role || (len(m.Capabilities.ExecutionProv) > 0 && string(m.Capabilities.ExecutionProv) != "null")
		for _, d := range m.Capabilities.ToolDefinitions {
			role = role || d.Shadows != ""
		}
		for _, d := range m.Capabilities.SkillDefinitions {
			role = role || d.Shadows != ""
		}
		if !role {
			continue
		}
		set[strings.ToUpper(m.ID[:1])+m.ID[1:]] = true
		var title string
		var titles map[string]string
		if json.Unmarshal(m.Capabilities.UI.Title, &title) == nil && title != "" {
			set[title] = true
		} else if json.Unmarshal(m.Capabilities.UI.Title, &titles) == nil {
			for _, v := range titles {
				set[v] = true
			}
		}
	}
	var out []string
	for n := range set {
		out = append(out, n)
	}
	sort.Strings(out)
	return out, nil
}
