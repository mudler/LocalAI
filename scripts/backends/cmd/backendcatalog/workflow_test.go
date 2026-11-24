// SPDX-License-Identifier: MIT
package main

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

// The CI check lives in a workflow that cannot run here, so these tests read it
// the way a parser does: it must be valid, regenerate the gallery with the same
// command the generator documents, and name tests that exist.
func TestBackendLibraryWorkflowRegeneratesTheGalleryAndNamesRealTests(t *testing.T) {
	raw, err := os.ReadFile("../../../../.github/workflows/backend-library.yml")
	if err != nil {
		t.Fatal(err)
	}
	var wf struct {
		Jobs map[string]struct {
			Steps []struct {
				Name string `yaml:"name"`
				Run  string `yaml:"run"`
			} `yaml:"steps"`
		} `yaml:"jobs"`
	}
	if err := yaml.Unmarshal(raw, &wf); err != nil {
		t.Fatalf("the workflow is not valid YAML: %v", err)
	}
	steps := wf.Jobs["backend-library"].Steps
	if len(steps) == 0 {
		t.Fatal("the workflow has no job backend-library")
	}
	regenerate := "go run ./scripts/backends/cmd/backendcatalog --library gallery/backends/index.yaml --out backend/index.yaml"
	var checks, tests string
	for _, s := range steps {
		if strings.Contains(s.Run, regenerate) && strings.Contains(s.Run, "git diff --exit-code -- backend/index.yaml") {
			checks = s.Run
		}
		if tests == "" && strings.Contains(s.Run, "go test ./core/gallery -run '") {
			tests = s.Run
		}
	}
	if checks == "" {
		t.Error("no step regenerates backend/index.yaml and fails on a diff")
	}
	if strings.Contains(string(raw), "--repin") && !strings.Contains(string(raw), "never reads the image registry") {
		t.Error("CI must not pin digests: it never reads the registry")
	}
	m := regexp.MustCompile(`go test \./core/gallery -run '([^']+)'`).FindStringSubmatch(tests)
	if m == nil {
		t.Fatal("no step runs the core/gallery tests by name")
	}
	var sources strings.Builder
	files, _ := filepath.Glob("../../../../core/gallery/*_test.go")
	for _, f := range files {
		b, _ := os.ReadFile(f)
		sources.Write(b)
	}
	for _, name := range strings.Split(m[1], "|") {
		if !regexp.MustCompile(`func ` + name + `\w*\(t \*testing\.T\)`).MatchString(sources.String()) {
			t.Errorf("the workflow runs %q, which matches no test in core/gallery", name)
		}
	}
	// The command in the workflow is the one the generated gallery says to run.
	gen, err := os.ReadFile("../../../../backend/index.yaml")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(gen), "#   "+regenerate) {
		t.Error("the generated gallery names another regenerate command than the workflow")
	}
}
