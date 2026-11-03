// Package providerguard reads the shipped add-ons so tests can check that core
// names none of them. Only tests import it.
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
		AdminTools       json.RawMessage            `json:"adminTools"`
		Credentials      json.RawMessage            `json:"credentials"`
		ContextFiles     json.RawMessage            `json:"contextFiles"`
		UI               struct {
			Title json.RawMessage `json:"title"`
		} `json:"ui"`
	} `json:"capabilities"`
}

// Addon is one shipped add-on as its manifest declares it.
type Addon struct {
	ID string
	// Titles are the declared display titles: one string, or one per locale.
	Titles []string
	// Role is true when the add-on gives the host something to act on: it is
	// a connector or the execution provider, it shadows a built-in tool or
	// skill, or it declares admin tools, credentials or context files. Core
	// can special-case such an add-on, so the guard matches its id and
	// titles. The others (face, voice, talk, studio, fine-tuning,
	// quantization) are a page and a service that core owns; their names are
	// ordinary words of the product ("Voice", "Quantization") and are not
	// matched.
	Role bool
}

// Addons reads addons/*/manifest.json and returns every shipped add-on with
// its declared titles, sorted by id. The guard is driven by this list, so a
// new add-on is covered the day its manifest lands, with no list to update.
func Addons() ([]Addon, error) {
	root, err := RepoRoot()
	if err != nil {
		return nil, err
	}
	files, err := filepath.Glob(filepath.Join(root, "addons", "*", "manifest.json"))
	if err != nil || len(files) == 0 {
		return nil, fmt.Errorf("no add-on manifests found: %v", err)
	}
	var out []Addon
	for _, f := range files {
		raw, err := os.ReadFile(f)
		if err != nil {
			return nil, err
		}
		var m manifest
		if json.Unmarshal(raw, &m) != nil || m.ID == "" {
			continue
		}
		a := Addon{ID: m.ID}
		present := func(r json.RawMessage) bool { return len(r) > 0 && string(r) != "null" }
		a.Role = present(m.Capabilities.Connector) || present(m.Capabilities.ExecutionProv) ||
			present(m.Capabilities.AdminTools) || present(m.Capabilities.Credentials) || present(m.Capabilities.ContextFiles)
		for _, d := range m.Capabilities.ToolDefinitions {
			a.Role = a.Role || d.Shadows != ""
		}
		for _, d := range m.Capabilities.SkillDefinitions {
			a.Role = a.Role || d.Shadows != ""
		}
		seen := map[string]bool{}
		add := func(t string) {
			if t != "" && !seen[t] {
				seen[t] = true
				a.Titles = append(a.Titles, t)
			}
		}
		var title string
		var titles map[string]string
		if json.Unmarshal(m.Capabilities.UI.Title, &title) == nil {
			add(title)
		} else if json.Unmarshal(m.Capabilities.UI.Title, &titles) == nil {
			for _, v := range titles {
				add(v)
			}
		}
		sort.Strings(a.Titles)
		out = append(out, a)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out, nil
}

// Names returns the capitalised id and the declared titles of the add-ons with
// a role, sorted. Tests of the prompt builders use it to check that no prompt
// holds the name of a shipped add-on.
func Names() ([]string, error) {
	addons, err := Addons()
	if err != nil {
		return nil, err
	}
	set := map[string]bool{}
	for _, a := range addons {
		if !a.Role {
			continue
		}
		set[strings.ToUpper(a.ID[:1])+a.ID[1:]] = true
		for _, t := range a.Titles {
			set[t] = true
		}
	}
	var out []string
	for n := range set {
		out = append(out, n)
	}
	sort.Strings(out)
	return out, nil
}
