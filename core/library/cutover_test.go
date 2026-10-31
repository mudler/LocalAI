package library

import (
	"os"
	"strings"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"gopkg.in/yaml.v3"
)

// The default source of a build is chosen at build time, so these tests read
// the files that choose it. They keep the three places that carry the value
// (the Makefile, GoReleaser and the release workflow) in step with the
// variables in this package, for the models and for the backends.

func readRepoFile(path string) string {
	b, err := os.ReadFile("../../" + path)
	Expect(err).ToNot(HaveOccurred())
	return string(b)
}

var _ = Describe("the build-time source cutover", func() {
	It("has the Makefile pass the source of both kinds", func() {
		mk := readRepoFile("Makefile")
		for _, c := range []struct{ env, variable string }{
			{"LOCALAI_MODEL_SOURCE", "ModelSource"},
			{"LOCALAI_BACKEND_SOURCE", "BackendSource"},
		} {
			// A tagged commit reads the gallery; any other commit reads the library.
			rule := c.env + `?=$(shell git describe --exact-match --tags >/dev/null 2>&1 && echo gallery || echo library)`
			Expect(mk).To(ContainSubstring(rule), "the Makefile must default %s to the gallery on a tag and to the library elsewhere", c.env)
			flag := `-X "github.com/mudler/LocalAI/core/library.` + c.variable + `=$(` + c.env + `)"`
			Expect(mk).To(ContainSubstring(flag), "the Makefile must pass %s to the binary: want %s", c.env, flag)
		}
	})

	It("has GoReleaser read the gallery unless the workflow says otherwise", func() {
		gr := readRepoFile(".goreleaser.yaml")
		for _, c := range []struct{ env, variable string }{
			{"LOCALAI_MODEL_SOURCE", "ModelSource"},
			{"LOCALAI_BACKEND_SOURCE", "BackendSource"},
		} {
			want := `-X "github.com/mudler/LocalAI/core/library.` + c.variable + `={{ envOrDefault "` + c.env + `" "gallery" }}"`
			Expect(gr).To(ContainSubstring(want), "GoReleaser must default %s to the gallery: want %s", c.env, want)
		}
	})

	It("has the release workflow cut both sources over before the build", func() {
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
		Expect(yaml.Unmarshal([]byte(readRepoFile(".github/workflows/release.yaml")), &wf)).To(Succeed())
		steps := wf.Jobs["goreleaser"].Steps
		build := -1
		for i, s := range steps {
			if strings.HasPrefix(s.Uses, "goreleaser/goreleaser-action") {
				build = i
			}
		}
		Expect(build).To(BeNumerically(">=", 0), "the release workflow has no GoReleaser step")
		for _, env := range []string{"LOCALAI_MODEL_SOURCE", "LOCALAI_BACKEND_SOURCE"} {
			cut := -1
			for i, s := range steps {
				if strings.Contains(s.Run, env+"=library") && strings.Contains(s.Run, "$GITHUB_ENV") {
					cut = i
				}
			}
			Expect(cut).To(BeNumerically(">=", 0), "the cutover step for %s must exist", env)
			Expect(cut).To(BeNumerically("<=", build), "the cutover step for %s must run before GoReleaser (step %d, build %d)", env, cut, build)
		}
	})

	// The values the cutover passes are the values this package knows, and the
	// variables the Makefile names exist.
	It("passes values the package knows", func() {
		Expect(ModelSourceLibrary).To(Equal("library"), "the build values are library and gallery; the Makefile, GoReleaser and the workflow pass those")
		Expect(ModelSourceGallery).To(Equal("gallery"))
		Expect(BackendSourceLibrary).To(Equal("library"))
		Expect(BackendSourceGallery).To(Equal("gallery"))
		// A mistyped build value reads the library, so a build never has no source.
		for _, v := range []string{"", "Gallery", "gallery ", "lib"} {
			setModelSource(v)
			setBackendSource(v)
			Expect(UseModelLibrary()).To(BeTrue(), "the value %q must read the library", v)
			Expect(UseBackendLibrary()).To(BeTrue(), "the value %q must read the library", v)
		}
	})
})
