// SPDX-License-Identifier: MIT
package packaging

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("A windows/amd64 row with execution wsl2", func() {
	// manifestPath is the manifest template of a project.
	manifestPath := func(p *project) string { return filepath.Join(p.root, "addons", "tables", "manifest.json") }
	// wslProject marks the windows/amd64 row of a project wsl2.
	wslProject := func(enabled map[string][]string) *project {
		p := newProject(enabled)
		b, err := os.ReadFile(p.config)
		Expect(err).NotTo(HaveOccurred())
		s := string(b)
		i := strings.Index(s, "  - platform: windows/amd64\n")
		Expect(i).To(BeNumerically(">=", 0))
		row := s[i:]
		row = strings.Replace(row, "    native_check: executable-start\n", "    native_check: executable-start\n    execution: wsl2\n", 1)
		Expect(os.WriteFile(p.config, []byte(s[:i]+row), 0o644)).To(Succeed())
		return p
	}
	elf := func() string {
		p := filepath.Join(GinkgoT().TempDir(), "tables")
		Expect(os.WriteFile(p, []byte("\x7fELF linux program"), 0o755)).To(Succeed())
		return p
	}

	It("is built for linux/amd64 and keeps the plain executable name", func() {
		p := wslProject(map[string][]string{"windows/amd64": nil})
		out, err := run("build-platform", "--config", p.config, "--platform", "windows/amd64")
		Expect(err).NotTo(HaveOccurred())
		Expect(out).To(Equal("linux/amd64\n"))
		out, err = run("build-platform", "--config", p.config, "--platform", "linux/arm64")
		Expect(err).NotTo(HaveOccurred())
		Expect(out).To(Equal("linux/arm64\n"))
	})

	It("makes a Windows package that carries the Linux executable and says osSettings.windows.execution wsl2 at feature level 10", func() {
		p := wslProject(map[string][]string{"windows/amd64": nil})
		dir, err := p.build("windows/amd64", "1.2.3", elf())
		Expect(err).NotTo(HaveOccurred())
		Expect(p.verify(dir, "windows/amd64", "1.2.3")).To(Succeed())
		dest := filepath.Join(GinkgoT().TempDir(), "out")
		_, err = run("extract", "--config", p.config, "--layout", dir, "--platform", "windows/amd64", "--version", "1.2.3", "--dest", dest)
		Expect(err).NotTo(HaveOccurred())
		Expect(filepath.Join(dest, "tables")).To(BeARegularFile())
		Expect(filepath.Join(dest, "tables.exe")).NotTo(BeAnExistingFile())
		b, err := os.ReadFile(filepath.Join(dest, "manifest.json"))
		Expect(err).NotTo(HaveOccurred())
		var m struct {
			Executable, OS, Arch string
			FeatureLevel         uint64
			OSSettings           json.RawMessage
			Execution            *string
		}
		Expect(json.Unmarshal(b, &m)).To(Succeed())
		Expect(m.Executable).To(Equal("tables"))
		Expect(m.OS + "/" + m.Arch).To(Equal("windows/amd64"))
		Expect(m.OSSettings).To(MatchJSON(`{"windows":{"execution":"wsl2"}}`))
		Expect(m.Execution).To(BeNil(), "no top-level execution field")
		Expect(m.FeatureLevel).To(BeNumerically(">=", 10))
	})

	It("refuses an executable that is not a Linux program", func() {
		p := wslProject(map[string][]string{"windows/amd64": nil})
		dir, err := p.build("windows/amd64", "1.2.3", exe("windows bin"))
		Expect(err).NotTo(HaveOccurred())
		expectCode(p.verify(dir, "windows/amd64", "1.2.3"), "E_ENTRYPOINT")
	})

	It("refuses a package whose manifest disagrees with the row", func() {
		native := newProject(map[string][]string{"windows/amd64": nil})
		dir := native.mustBuild("windows/amd64", "1.2.3", "windows bin")
		p := wslProject(map[string][]string{"windows/amd64": nil})
		expectCode(p.verify(dir, "windows/amd64", "1.2.3"), "E_MANIFEST")

		// The other way: a wsl2 package checked against a native row.
		dir, err := p.build("windows/amd64", "1.2.3", elf())
		Expect(err).NotTo(HaveOccurred())
		expectCode(native.verify(dir, "windows/amd64", "1.2.3"), "E_MANIFEST")
	})

	It("takes the osSettings block only from the row, never from the manifest template", func() {
		for _, rows := range []func(map[string][]string) *project{newProject, wslProject} {
			p := rows(map[string][]string{amd: nil, "windows/amd64": nil})
			b, err := os.ReadFile(manifestPath(p))
			Expect(err).NotTo(HaveOccurred())
			var m map[string]any
			Expect(json.Unmarshal(b, &m)).To(Succeed())
			m["osSettings"] = map[string]any{"windows": map[string]any{"execution": "wsl2"}}
			m["featureLevel"] = 10
			b, err = json.Marshal(m)
			Expect(err).NotTo(HaveOccurred())
			Expect(os.WriteFile(manifestPath(p), b, 0o644)).To(Succeed())
			_, err = p.build("windows/amd64", "1.2.3", elf())
			Expect(err).To(MatchError(ContainSubstring("osSettings")))
			_, err = p.build(amd, "1.2.3", exe("linux bin"))
			Expect(err).To(MatchError(ContainSubstring("osSettings")))
		}
	})

	It("is refused on any other platform, and any other value", func() {
		for _, bad := range []string{
			"  - platform: linux/amd64\n    enabled: false\n    execution: wsl2\n",
			"  - platform: windows/amd64\n    enabled: false\n    execution: wsl1\n",
		} {
			f := filepath.Join(GinkgoT().TempDir(), "platforms.yaml")
			Expect(os.WriteFile(f, []byte("schema: 1\naddon: tables\nexecutable: tables\nmanifest: m.json\nplatforms:\n"+bad), 0o644)).To(Succeed())
			_, err := loadPlatforms(f)
			Expect(err).To(MatchError(ContainSubstring("execution")))
		}
	})
})
