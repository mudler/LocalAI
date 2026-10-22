package library

import (
	"os"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

// The default source of a build is chosen at build time, so these tests read
// the files that choose it. They keep the three places that carry the value
// (the Makefile, GoReleaser and the release workflow) in step with the
// variables in this package, for the models and for the backends.

func readRepoFile(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile("../../" + path)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestMakefilePassesTheSourceOfBothKinds(t *testing.T) {
	mk := readRepoFile(t, "Makefile")
	for _, c := range []struct{ env, variable string }{
		{"LOCALAI_MODEL_SOURCE", "ModelSource"},
		{"LOCALAI_BACKEND_SOURCE", "BackendSource"},
	} {
		// A tagged commit reads the gallery; any other commit reads the library.
		rule := c.env + `?=$(shell git describe --exact-match --tags >/dev/null 2>&1 && echo gallery || echo library)`
		if !strings.Contains(mk, rule) {
			t.Errorf("the Makefile must default %s to the gallery on a tag and to the library elsewhere", c.env)
		}
		flag := `-X "github.com/mudler/LocalAI/core/library.` + c.variable + `=$(` + c.env + `)"`
		if !strings.Contains(mk, flag) {
			t.Errorf("the Makefile must pass %s to the binary: want %s", c.env, flag)
		}
	}
}

func TestGoReleaserReadsTheGalleryUnlessTheWorkflowSaysOtherwise(t *testing.T) {
	gr := readRepoFile(t, ".goreleaser.yaml")
	for _, c := range []struct{ env, variable string }{
		{"LOCALAI_MODEL_SOURCE", "ModelSource"},
		{"LOCALAI_BACKEND_SOURCE", "BackendSource"},
	} {
		want := `-X "github.com/mudler/LocalAI/core/library.` + c.variable + `={{ envOrDefault "` + c.env + `" "gallery" }}"`
		if !strings.Contains(gr, want) {
			t.Errorf("GoReleaser must default %s to the gallery: want %s", c.env, want)
		}
	}
}

func TestReleaseWorkflowCutsBothSourcesOverBeforeTheBuild(t *testing.T) {
	var wf struct {
		Jobs map[string]struct {
			Steps []struct {
				Name string            `yaml:"name"`
				Uses string            `yaml:"uses"`
				Run  string            `yaml:"run"`
				Env  map[string]string `yaml:"env"`
			} `yaml:"steps"`
		} `yaml:"jobs"`
	}
	if err := yaml.Unmarshal([]byte(readRepoFile(t, ".github/workflows/release.yaml")), &wf); err != nil {
		t.Fatal(err)
	}
	steps := wf.Jobs["goreleaser"].Steps
	build := -1
	for i, s := range steps {
		if strings.HasPrefix(s.Uses, "goreleaser/goreleaser-action") {
			build = i
		}
	}
	if build < 0 {
		t.Fatal("the release workflow has no GoReleaser step")
	}
	for _, env := range []string{"LOCALAI_MODEL_SOURCE", "LOCALAI_BACKEND_SOURCE"} {
		cut := -1
		for i, s := range steps {
			if strings.Contains(s.Run, env+"=library") && strings.Contains(s.Run, "$GITHUB_ENV") {
				cut = i
			}
		}
		if cut < 0 || cut > build {
			t.Errorf("the cutover step for %s must exist and run before GoReleaser (step %d, build %d)", env, cut, build)
		}
	}
}

// The values the cutover passes are the values this package knows, and the
// variables the Makefile names exist.
func TestCutoverValuesAreTheOnesThePackageKnows(t *testing.T) {
	if ModelSourceLibrary != "library" || ModelSourceGallery != "gallery" || BackendSourceLibrary != "library" || BackendSourceGallery != "gallery" {
		t.Error("the build values are library and gallery; the Makefile, GoReleaser and the workflow pass those")
	}
	// A mistyped build value reads the library, so a build never has no source.
	for _, v := range []string{"", "Gallery", "gallery ", "lib"} {
		setModelSource(t, v)
		setBackendSource(t, v)
		if !UseModelLibrary() || !UseBackendLibrary() {
			t.Errorf("the value %q must read the library", v)
		}
	}
}
