// SPDX-License-Identifier: MIT
package packaging

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"gopkg.in/yaml.v3"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/google/go-containerregistry/pkg/name"
	v1 "github.com/google/go-containerregistry/pkg/v1"
	"github.com/google/go-containerregistry/pkg/v1/remote"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

const (
	amd = "linux/amd64"
	arm = "linux/arm64"
	rev = "0123456789abcdef0123456789abcdef01234567"
)

var both = map[string][]string{amd: nil, arm: nil}

var _ = Describe("package layout", func() {
	It("builds a deterministic platform layout with executable, ui and manifest layers", func() {
		p := newProject(both)
		a := p.mustBuild(amd, "1.2.3", "same bytes", "--revision", rev)
		b := p.mustBuild(amd, "1.2.3", "same bytes", "--revision", rev)
		la, ra, err := loadLeg(a)
		Expect(err).NotTo(HaveOccurred())
		lb, _, err := loadLeg(b)
		Expect(err).NotTo(HaveOccurred())
		Expect(la.descriptor.Digest).To(Equal(lb.descriptor.Digest))
		Expect(la.manifest.ArtifactType).To(Equal(ArtifactType))
		Expect(la.manifest.Annotations).To(HaveKeyWithValue(AnnotationVersion, "1.2.3"))
		Expect(la.manifest.Annotations).To(HaveKeyWithValue(AnnotationPlatform, amd))
		Expect(la.manifest.Annotations).To(HaveKeyWithValue(AnnotationRevision, rev))
		Expect(la.config.OS).To(Equal("linux"))
		Expect(la.config.Architecture).To(Equal("amd64"))
		var roles []string
		for _, l := range ra.Layers {
			roles = append(roles, l.Role)
			Expect(l.MediaType).To(Equal("application/vnd.oci.image.layer.v1.tar+gzip"))
		}
		Expect(roles).To(Equal([]string{"executable", "ui", "manifest"}))
		Expect(ra.Publishable).To(BeTrue())
		Expect(p.verify(a, amd, "1.2.3")).To(Succeed())
		other := p.mustBuild(amd, "1.2.3", "other bytes", "--revision", rev)
		lo, _, _ := loadLeg(other)
		Expect(lo.descriptor.Digest).NotTo(Equal(la.descriptor.Digest))
	})

	It("restores the validated executable mode on extraction", func() {
		p := newProject(both)
		dir := p.mustBuild(amd, "1.2.3", "bin")
		dest := filepath.Join(GinkgoT().TempDir(), "out")
		_, err := run("extract", "--config", p.config, "--layout", dir, "--platform", amd, "--version", "1.2.3", "--dest", dest)
		Expect(err).NotTo(HaveOccurred())
		info, err := os.Stat(filepath.Join(dest, "tables"))
		Expect(err).NotTo(HaveOccurred())
		Expect(info.Mode().Perm()).To(Equal(os.FileMode(0o755)))
		ui, err := os.Stat(filepath.Join(dest, "ui", "tables.js"))
		Expect(err).NotTo(HaveOccurred())
		Expect(ui.Mode().Perm()).To(Equal(os.FileMode(0o644)))
		m, err := os.ReadFile(filepath.Join(dest, "manifest.json"))
		Expect(err).NotTo(HaveOccurred())
		Expect(string(m)).To(ContainSubstring(`"version": "1.2.3"`))
		Expect(string(m)).To(ContainSubstring(`"arch": "amd64"`))
	})

	It("refuses a platform that the matrix has not enabled unless it is a local check", func() {
		p := newProject(map[string][]string{amd: nil})
		_, err := p.build(arm, "1.2.3", exe("x"))
		Expect(err).To(MatchError(ContainSubstring("not enabled")))
		dir, err := p.build(arm, "1.2.3", exe("x"), "--local-check")
		Expect(err).NotTo(HaveOccurred())
		_, rec, err := loadLeg(dir)
		Expect(err).NotTo(HaveOccurred())
		Expect(rec.Publishable).To(BeFalse())
		_, err = p.build("windows/amd64", "1.2.3", exe("x"))
		Expect(err).To(HaveOccurred())
	})

	It("refuses an invalid version, an existing layout and an empty executable", func() {
		p := newProject(both)
		for _, v := range []string{"", "v1.2.3", "1.2", "1.2.3+build", "latest", "01.2.3"} {
			_, err := p.build(amd, v, exe("x"))
			Expect(err).To(HaveOccurred(), v)
		}
		dir := p.mustBuild(amd, "1.2.3", "x")
		_, err := run("build", "--config", p.config, "--root", p.root, "--layout", dir, "--version", "1.2.3", "--platform", amd, "--executable", exe("x"))
		Expect(err).To(MatchError(ContainSubstring("not empty")))
		_, err = p.build(amd, "1.2.3", exe(""))
		Expect(err).To(HaveOccurred())
	})
})

var _ = Describe("package rules", func() {
	var p *project
	var dir string
	BeforeEach(func() {
		p = newProject(map[string][]string{amd: {"lib/libruntime.so"}, arm: nil})
		rt := filepath.Join(GinkgoT().TempDir(), "rt")
		Expect(os.MkdirAll(filepath.Join(rt, "lib"), 0o755)).To(Succeed())
		Expect(os.WriteFile(filepath.Join(rt, "lib", "libruntime.so"), []byte("library"), 0o644)).To(Succeed())
		var err error
		dir, err = p.build(amd, "1.2.3", exe("binary"), "--runtime-dir", rt)
		Expect(err).NotTo(HaveOccurred())
		Expect(p.verify(dir, amd, "1.2.3")).To(Succeed())
	})

	It("rejects a package without its entrypoint", func() {
		s := readSpec(dir)
		find(s, "executable").drop("tables")
		rewrite(dir, s, nil)
		expectCode(p.verify(dir, amd, "1.2.3"), "E_ENTRYPOINT")
	})
	It("rejects a declared UI whose layer or files are missing", func() {
		s := readSpec(dir)
		find(s, "ui").drop("ui/tables.js")
		rewrite(dir, s, nil)
		expectCode(p.verify(dir, amd, "1.2.3"), "E_UI")
		s = readSpec(dir)
		var kept []layerSpec
		for _, l := range s {
			if l.role != "ui" {
				kept = append(kept, l)
			}
		}
		rewrite(dir, kept, nil)
		expectCode(p.verify(dir, amd, "1.2.3"), "E_UI")
	})
	It("rejects a package without a required runtime library", func() {
		s := readSpec(dir)
		find(s, "executable").drop("lib/libruntime.so")
		rewrite(dir, s, nil)
		expectCode(p.verify(dir, amd, "1.2.3"), "E_RUNTIME")
		_, err := p.build(amd, "1.2.4", exe("binary"))
		Expect(err).To(MatchError(ContainSubstring("--runtime-dir")))
		_, err = p.build(amd, "1.2.4", exe("binary"), "--runtime-dir", GinkgoT().TempDir())
		Expect(err).To(MatchError(ContainSubstring("runtime file")))
	})
	It("rejects a manifest whose platform differs from the image", func() {
		s := readSpec(dir)
		f := find(s, "manifest").get("manifest.json")
		f.data = bytes.Replace(f.data, []byte(`"os": "linux"`), []byte(`"os": "darwin"`), 1)
		rewrite(dir, s, nil)
		expectCode(p.verify(dir, amd, "1.2.3"), "E_PLATFORM")
	})
	It("rejects an image config or annotation of another platform", func() {
		rewrite(dir, readSpec(dir), func(_ *v1.Manifest, c *v1.ConfigFile, _ *Receipt) { c.Architecture = "arm64" })
		expectCode(p.verify(dir, amd, "1.2.3"), "E_PLATFORM")
	})
	It("rejects a manifest version that differs from the release version", func() {
		s := readSpec(dir)
		f := find(s, "manifest").get("manifest.json")
		f.data = bytes.Replace(f.data, []byte(`"version": "1.2.3"`), []byte(`"version": "9.9.9"`), 1)
		rewrite(dir, s, nil)
		expectCode(p.verify(dir, amd, "1.2.3"), "E_VERSION")
		expectCode(p.verify(dir, amd, "9.9.9"), "E_VERSION")
	})
	It("rejects another artifact type", func() {
		rewrite(dir, readSpec(dir), func(m *v1.Manifest, _ *v1.ConfigFile, _ *Receipt) { m.ArtifactType = "application/vnd.example.other" })
		expectCode(p.verify(dir, amd, "1.2.3"), "E_ARTIFACT_TYPE")
	})
	It("rejects unexpected layers, roles and layer media types", func() {
		s := readSpec(dir)
		extra := append(append([]layerSpec{}, s...), layerSpec{role: "extra", files: []tfile{{name: "extra.bin", data: []byte("x"), mode: 0o644}}})
		rewrite(dir, extra, nil)
		expectCode(p.verify(dir, amd, "1.2.3"), "E_LAYERS")
		s = readSpec(dir)[:3]
		s[0], s[1] = s[1], s[0]
		rewrite(dir, s, nil)
		expectCode(p.verify(dir, amd, "1.2.3"), "E_LAYERS")
		s = readSpec(dir)
		s[0], s[1] = s[1], s[0]
		s[0].mediaType = "application/vnd.docker.image.rootfs.diff.tar.gzip"
		rewrite(dir, s, nil)
		expectCode(p.verify(dir, amd, "1.2.3"), "E_LAYERS")
	})
	It("rejects an unexpected file in the executable layer", func() {
		s := readSpec(dir)
		e := find(s, "executable")
		e.files = append(e.files, tfile{name: "payload.sh", data: []byte("x"), mode: 0o644})
		rewrite(dir, s, nil)
		expectCode(p.verify(dir, amd, "1.2.3"), "E_LAYERS")
	})
	It("rejects a blob whose bytes differ from its digest", func() {
		l, _, err := loadLeg(dir)
		Expect(err).NotTo(HaveOccurred())
		path := filepath.Join(dir, "blobs", "sha256", l.manifest.Layers[0].Digest.Hex)
		b, err := os.ReadFile(path)
		Expect(err).NotTo(HaveOccurred())
		b[len(b)/2] ^= 0xff
		Expect(os.WriteFile(path, b, 0o644)).To(Succeed())
		expectCode(p.verify(dir, amd, "1.2.3"), "E_DIGEST")
	})
	It("rejects a receipt that does not match the image", func() {
		rb, err := os.ReadFile(filepath.Join(dir, receiptFile))
		Expect(err).NotTo(HaveOccurred())
		var r Receipt
		Expect(json.Unmarshal(rb, &r)).To(Succeed())
		r.ManifestDigest = "sha256:" + strings.Repeat("0", 64)
		rb, _ = json.Marshal(r)
		Expect(os.WriteFile(filepath.Join(dir, receiptFile), rb, 0o644)).To(Succeed())
		expectCode(p.verify(dir, amd, "1.2.3"), "E_DIGEST")
		Expect(os.Remove(filepath.Join(dir, receiptFile))).To(Succeed())
		expectCode(p.verify(dir, amd, "1.2.3"), "E_DIGEST")
	})
	It("rejects invalid extraction permissions", func() {
		cases := map[string]func(*layerSpec){
			"set-uid executable":        func(l *layerSpec) { l.get("tables").mode = 0o4755 },
			"world-writable file":       func(l *layerSpec) { l.get("lib/libruntime.so").mode = 0o666 },
			"executable library":        func(l *layerSpec) { l.get("lib/libruntime.so").mode = 0o755 },
			"non-executable entrypoint": func(l *layerSpec) { l.get("tables").mode = 0o644 },
		}
		for label, mut := range cases {
			d := p.mustBuild(amd, "1.2.5", "b", "--local-check", "--runtime-dir", mustRuntime())
			s := readSpec(d)
			mut(find(s, "executable"))
			rewrite(d, s, nil)
			err := p.verify(d, amd, "1.2.5")
			expectCode(err, "E_MODE")
			_ = label
		}
	})
	It("rejects a UI file that is executable or outside the declared directory", func() {
		s := readSpec(dir)
		find(s, "ui").get("ui/tables.css").mode = 0o755
		rewrite(dir, s, nil)
		expectCode(p.verify(dir, amd, "1.2.3"), "E_MODE")
		s = readSpec(dir)
		find(s, "ui").get("ui/tables.css").mode = 0o644
		u := find(s, "ui")
		u.files = append(u.files, tfile{name: "other/x.js", data: []byte("x"), mode: 0o644})
		rewrite(dir, s, nil)
		expectCode(p.verify(dir, amd, "1.2.3"), "E_LAYERS")
	})
	It("rejects traversal paths, links and duplicates", func() {
		for _, bad := range []tfile{
			{name: "../escape", data: []byte("x"), mode: 0o644},
			{name: "/abs", data: []byte("x"), mode: 0o644},
			{name: "ui/link", typeflag: 's', link: "../../etc/passwd", mode: 0o644},
			{name: "tables", data: []byte("dup"), mode: 0o755},
		} {
			s := readSpec(dir)
			e := find(s, "executable")
			e.files = append(e.files, bad)
			rewrite(dir, s, nil)
			expectCode(p.verify(dir, amd, "1.2.3"), "E_PATH")
			dir = p.mustBuild(amd, "1.2.3", "binary", "--local-check", "--runtime-dir", mustRuntime())
		}
	})
	It("rejects a UI that bundles its own React runtime", func() {
		s := readSpec(dir)
		f := find(s, "ui").get("ui/tables.js")
		f.data = append(f.data, []byte("var x=__SECRET_INTERNALS_DO_NOT_USE_OR_YOU_WILL_BE_FIRED;")...)
		rewrite(dir, s, nil)
		expectCode(p.verify(dir, amd, "1.2.3"), "E_REACT")
	})
	It("rejects an icon that is not an image of its type", func() {
		s := readSpec(dir)
		find(s, "ui").get("ui/icon.png").data = []byte("not a png")
		rewrite(dir, s, nil)
		expectCode(p.verify(dir, amd, "1.2.3"), "E_UI")
	})
	It("rejects an invalid platform layout marker or index", func() {
		Expect(os.WriteFile(filepath.Join(dir, "oci-layout"), []byte(`{"imageLayoutVersion":"2.0.0"}`), 0o644)).To(Succeed())
		expectCode(p.verify(dir, amd, "1.2.3"), "E_LAYOUT")
	})
})

func mustRuntime() string {
	GinkgoHelper()
	rt := filepath.Join(GinkgoT().TempDir(), "rt")
	Expect(os.MkdirAll(filepath.Join(rt, "lib"), 0o755)).To(Succeed())
	Expect(os.WriteFile(filepath.Join(rt, "lib", "libruntime.so"), []byte("library"), 0o644)).To(Succeed())
	return rt
}

var _ = Describe("platform matrix", func() {
	write := func(body string) string {
		p := filepath.Join(GinkgoT().TempDir(), "platforms.yaml")
		Expect(os.WriteFile(p, []byte(body), 0o644)).To(Succeed())
		return p
	}
	head := "schema: 1\naddon: tables\nexecutable: tables\nmanifest: m.json\nplatforms:\n"
	It("fails closed when no platform is enabled, the file is absent or empty", func() {
		_, err := run("matrix", "--config", write(head+"  - platform: linux/amd64\n    enabled: false\n"))
		Expect(err).To(MatchError(ContainSubstring("no enabled platform")))
		_, err = run("matrix", "--config", filepath.Join(GinkgoT().TempDir(), "absent.yaml"))
		Expect(err).To(HaveOccurred())
		_, err = run("matrix", "--config", write(""))
		Expect(err).To(HaveOccurred())
		_, err = run("matrix", "--config", write("schema: 1\naddon: tables\nexecutable: tables\nmanifest: m.json\nplatforms: []\n"))
		Expect(err).To(MatchError(ContainSubstring("no enabled platform")))
		_, err = run("matrix")
		Expect(err).To(HaveOccurred())
	})
	It("rejects enabled platforms without a native runner or check, duplicates and unknown fields", func() {
		for _, body := range []string{
			head + "  - platform: linux/amd64\n    enabled: true\n",
			head + "  - platform: linux/amd64\n    enabled: true\n    runner: r\n",
			head + "  - platform: linux/amd64\n    enabled: false\n  - platform: linux/amd64\n    enabled: false\n",
			head + "  - platform: windows/amd64\n    enabled: false\n",
			head + "  - platform: linux/amd64\n    enabled: false\n    registry: quay.io/x\n",
			head + "  - platform: linux/amd64\n    enabled: false\n    runtime: [../escape]\n",
		} {
			_, err := run("matrix", "--config", write(body))
			Expect(err).To(HaveOccurred(), body)
		}
	})
	It("prints the enabled platforms with their native runners", func() {
		out, err := run("matrix", "--config", write(head+"  - platform: linux/arm64\n    enabled: true\n    runner: r2\n    native_check: executable-start\n  - platform: linux/amd64\n    enabled: true\n    runner: r1\n    native_check: executable-start\n  - platform: darwin/arm64\n    enabled: false\n"))
		Expect(err).NotTo(HaveOccurred())
		Expect(strings.TrimSpace(out)).To(Equal(`{"include":[{"platform":"linux/amd64","runner":"r1","slug":"linux-amd64"},{"platform":"linux/arm64","runner":"r2","slug":"linux-arm64"}]}`))
	})
	It("ships the checked-in matrix with every platform disabled until its native check passes", func() {
		cfg, err := loadPlatforms("../../../addons/tables/package/platforms.yaml")
		Expect(err).NotTo(HaveOccurred())
		Expect(cfg.Platforms).NotTo(BeEmpty())
		for _, e := range cfg.Platforms {
			Expect(e.Enabled).To(BeFalse(), e.Platform)
		}
		_, err = cfg.enabled()
		Expect(err).To(HaveOccurred())
	})
})

var _ = Describe("event policy and channels", func() {
	sha40 := rev
	long := strings.Repeat("a", 1500)
	decision := func(event, ref string) Decision {
		GinkgoHelper()
		d, err := decide(event, ref, sha40, false, false)
		Expect(err).NotTo(HaveOccurred())
		return d
	}
	It("never publishes from a pull request, whatever the ref", func() {
		for _, ref := range []string{"refs/pull/1/merge", "refs/heads/master", "refs/tags/v1.0.0"} {
			for _, ev := range []string{"pull_request", "pull_request_target"} {
				d := decision(ev, ref)
				Expect(d.Publish).To(BeFalse())
				Expect(d.Aliases).To(BeEmpty())
				Expect(d.Immutable).To(BeEmpty())
			}
		}
	})
	It("maps master alone to development and publishes the commit tag", func() {
		d := decision("push", "refs/heads/master")
		Expect(d.Publish).To(BeTrue())
		Expect(d.Aliases).To(Equal([]string{"development"}))
		Expect(d.Immutable).To(Equal("sha-0123456789ab"))
		Expect(d.Version).To(Equal("0.0.0-dev.0123456789ab"))
	})
	It("never publishes from a branch push, whatever the branch is named", func() {
		for _, ref := range []string{"refs/heads/Master", "refs/heads/master2", "refs/heads/feature/master", "refs/heads/development", "refs/heads/MASTER", "refs/heads/latest", "refs/heads/feature/x"} {
			d := decision("push", ref)
			Expect(d.Publish).To(BeFalse(), ref)
			Expect(d.Aliases).To(BeEmpty(), ref)
			Expect(d.Immutable).To(BeEmpty(), ref)
			Expect(d.Channel).To(BeEmpty(), ref)
		}
	})
	It("keeps reserved names out of the branch namespace", func() {
		for _, br := range []string{"latest", "development", "Latest", "1.2.3", "v1.2.3", "sha-0123456789ab", "branch-x-000000000000", "0.0.0-dev.0123456789ab"} {
			tag := branchTag("refs/heads/" + br)
			Expect(tag).To(MatchRegexp(`^branch-[a-z0-9]+(-[a-z0-9]+)*-[0-9a-f]{12}$`), br)
			Expect(tag).NotTo(Equal("latest"))
			Expect(tag).NotTo(Equal("development"))
			Expect(versionRE.MatchString(tag)).To(BeFalse())
			Expect(immutableTagRE.MatchString(tag)).To(BeFalse())
		}
	})
	It("separates refs that normalize to the same label", func() {
		a := branchTag("refs/heads/Feature/X")
		b := branchTag("refs/heads/feature-x")
		c := branchTag("refs/heads/feature/x")
		d := branchTag("refs/heads/feature_x")
		Expect(branchLabel("Feature/X")).To(Equal(branchLabel("feature-x")))
		Expect([]string{a, b, c, d}).To(HaveLen(4))
		seen := map[string]bool{}
		for _, t := range []string{a, b, c, d} {
			Expect(seen).NotTo(HaveKey(t))
			seen[t] = true
		}
		Expect(branchTag("refs/heads/feature/x")).To(Equal(c))
	})
	It("bounds empty, Unicode and very long branch names", func() {
		for _, br := range []string{"日本語", "ünïcode/Ö", "!!!", "-", "a b c", long} {
			tag := branchTag("refs/heads/" + br)
			Expect(len(tag)).To(BeNumerically("<=", 7+maxLabel+1+12), br)
			Expect(tag).To(MatchRegexp(`^branch-[a-z0-9-]+-[0-9a-f]{12}$`))
		}
		Expect(branchTag("refs/heads/日本語")).NotTo(Equal(branchTag("refs/heads/中文")))
		_, err := decide("push", "", sha40, false, false)
		Expect(err).To(HaveOccurred())
		_, err = decide("push", "refs/heads/", sha40, false, false)
		Expect(err).To(HaveOccurred())
		_, err = decide("push", "refs/heads/"+strings.Repeat("a", 5000), sha40, false, false)
		Expect(err).To(HaveOccurred())
		_, err = decide("push", "refs/heads/x\nlatest", sha40, false, false)
		Expect(err).To(HaveOccurred())
	})
	It("lets a tag push prepare the immutable version and never move latest", func() {
		d := decision("push", "refs/tags/v1.2.3")
		Expect(d.Publish).To(BeTrue())
		Expect(d.Immutable).To(Equal("1.2.3"))
		Expect(d.Aliases).To(BeEmpty())
		for _, ref := range []string{"refs/tags/latest", "refs/tags/v1.2", "refs/tags/1.2.3", "refs/tags/development", "refs/tags/v1.2.3+x"} {
			_, err := decide("push", ref, sha40, false, false)
			Expect(err).To(HaveOccurred(), ref)
		}
	})
	It("promotes latest only for a published, stable, non-draft release", func() {
		d, err := decide("release", "refs/tags/v1.2.3", sha40, false, false)
		Expect(err).NotTo(HaveOccurred())
		Expect(d.Aliases).To(Equal([]string{"latest"}))
		Expect(d.Channel).To(Equal("latest"))
		Expect(d.Immutable).To(Equal("1.2.3"))
		d, err = decide("release", "refs/tags/v1.2.3", sha40, false, true)
		Expect(err).NotTo(HaveOccurred())
		Expect(d.Aliases).To(BeEmpty())
		d, err = decide("release", "refs/tags/v1.2.3-rc.1", sha40, false, false)
		Expect(err).NotTo(HaveOccurred())
		Expect(d.Aliases).To(BeEmpty())
		_, err = decide("release", "refs/tags/v1.2.3", sha40, true, false)
		Expect(err).To(HaveOccurred())
		_, err = decide("release", "refs/heads/master", sha40, false, false)
		Expect(err).To(HaveOccurred())
	})
	It("rejects unknown events and a malformed commit", func() {
		for _, ev := range []string{"", "workflow_dispatch", "schedule", "workflow_run"} {
			_, err := decide(ev, "refs/heads/master", sha40, false, false)
			Expect(err).To(HaveOccurred(), ev)
		}
		_, err := decide("push", "refs/heads/master", "abc", false, false)
		Expect(err).To(HaveOccurred())
	})
	It("prints the decision as key=value lines", func() {
		out, err := run("channel", "--event", "push", "--ref", "refs/heads/master", "--sha", sha40)
		Expect(err).NotTo(HaveOccurred())
		Expect(out).To(ContainSubstring("publish=true\n"))
		Expect(out).To(ContainSubstring("aliases=development\n"))
		_, err = run("channel", "--event", "push", "--ref", "refs/heads/x")
		Expect(err).To(HaveOccurred())
	})
})

var _ = Describe("merge and completeness", func() {
	It("joins every enabled platform leg into one verified index", func() {
		p := newProject(both)
		m := p.merged("1.2.3", rev, nil)
		cfg, err := loadPlatforms(p.config)
		Expect(err).NotTo(HaveOccurred())
		got, digest, err := verifyMerged(m, cfg)
		Expect(err).NotTo(HaveOccurred())
		Expect(got.Legs).To(HaveLen(2))
		Expect(digest.String()).To(Equal(got.IndexDigest))
		again := p.merged("1.2.3", rev, nil)
		_, d2, err := verifyMerged(again, cfg)
		Expect(err).NotTo(HaveOccurred())
		Expect(d2).To(Equal(digest))
	})
	It("never creates a partial index when an enabled platform has no leg", func() {
		p := newProject(both)
		leg := p.mustBuild(amd, "1.2.3", "x", "--revision", rev)
		_, err := run("merge", "--config", p.config, "--out", filepath.Join(GinkgoT().TempDir(), "m"), "--version", "1.2.3", leg)
		expectCode(err, "E_PLATFORMS")
	})
	It("refuses duplicate legs, local-check legs, other versions and extra platforms", func() {
		p := newProject(both)
		a := p.mustBuild(amd, "1.2.3", "x")
		b := p.mustBuild(arm, "1.2.3", "x")
		out := func() string { return filepath.Join(GinkgoT().TempDir(), "m") }
		_, err := run("merge", "--config", p.config, "--out", out(), "--version", "1.2.3", a, a, b)
		expectCode(err, "E_PLATFORMS")
		local := p.mustBuild(arm, "1.2.3", "x", "--local-check")
		_, err = run("merge", "--config", p.config, "--out", out(), "--version", "1.2.3", a, local)
		expectCode(err, "E_PLATFORMS")
		_, err = run("merge", "--config", p.config, "--out", out(), "--version", "1.2.4", a, b)
		Expect(err).To(MatchError(ContainSubstring("E_VERSION")))
		onlyAmd := newProject(map[string][]string{amd: nil})
		_, err = run("merge", "--config", onlyAmd.config, "--out", out(), "--version", "1.2.3", a, b)
		expectCode(err, "E_PLATFORMS")
	})
	It("refuses a merged layout that lost a platform after the merge", func() {
		p := newProject(both)
		m := p.merged("1.2.3", rev, nil)
		cfg, _ := loadPlatforms(p.config)
		// Enabling one more platform makes the merged layout partial.
		more := newProject(map[string][]string{amd: nil, arm: nil, "darwin/arm64": nil})
		moreCfg, err := loadPlatforms(more.config)
		Expect(err).NotTo(HaveOccurred())
		_, _, err = verifyMerged(m, moreCfg)
		expectCode(err, "E_PLATFORMS")
		_, _, err = verifyMerged(m, cfg)
		Expect(err).NotTo(HaveOccurred())
	})
})

var _ = Describe("add-on repository", func() {
	It("accepts the add-on namespace and refuses every other go-skynet repository", func() {
		for _, ok := range []string{"quay.io/go-skynet/local-ai-addons", "quay.io/go-skynet/local-ai-addons/tables"} {
			_, err := parseRepository(ok)
			Expect(err).NotTo(HaveOccurred(), ok)
		}
		for _, bad := range []string{"quay.io/go-skynet/local-ai-addons-evil", "quay.io/go-skynet/local-ai-backends", "quay.io/go-skynet/ci-cache", "quay.io/go-skynet/local-ai", "quay.io/other/local-ai-addons"} {
			_, err := parseRepository(bad)
			if bad == "quay.io/other/local-ai-addons" {
				Expect(err).NotTo(HaveOccurred(), bad)
				continue
			}
			Expect(err).To(HaveOccurred(), bad)
		}
	})
})

var _ = Describe("catalog index reader", func() {
	const entry = "- addonId: tables\n  name: Tables\n  version: 1.0.0\n  channel: latest\n  artifact: oci://registry.example/tables:1.0.0\n  digest: sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa\n  os: linux\n  arch: amd64\n"
	It("reads a YAML list and refuses JSON, unknown fields and a second document", func() {
		got, err := readCatalogIndex([]byte(entry))
		Expect(err).NotTo(HaveOccurred())
		Expect(got).To(HaveLen(1))
		Expect(got[0].AddonID).To(Equal("tables"))
		for name, body := range map[string]string{
			"json":            `[{"addonId":"tables"}]`,
			"unknown field":   entry + "  extra: 1\n",
			"second document": entry + "---\n" + entry,
			"mapping":         "addonId: tables\n",
		} {
			_, err := readCatalogIndex([]byte(body))
			Expect(err).To(HaveOccurred(), name)
		}
	})
})

var _ = Describe("publication", func() {
	ctx := context.Background()
	It("refuses missing and backend repositories before any request", func() {
		reg := newRegistry()
		p := newProject(both)
		m := p.merged("1.2.3", rev, nil)
		for _, repo := range []string{"", "quay.io/go-skynet/local-ai-backends", "docker.io/localai/localai", "quay.io/go-skynet/ci-cache", "Not Valid/Repo"} {
			_, err := run("publish", "--config", p.config, "--layout", m, "--repository", repo, "--tag", "1.2.3")
			Expect(err).To(HaveOccurred(), repo)
		}
		Expect(reg.writeCount()).To(BeZero())
	})
	It("pushes platform digests then the index, and a retry is digest-identical", func() {
		reg := newRegistry()
		p := newProject(both)
		m := p.merged("1.2.3", rev, nil)
		out, err := run("publish", "--config", p.config, "--layout", m, "--repository", reg.repo("addons/tables"), "--tag", "1.2.3")
		Expect(err).NotTo(HaveOccurred())
		Expect(out).To(ContainSubstring("digest=sha256:"))
		first := out
		before := reg.writeCount()
		out, err = run("publish", "--config", p.config, "--layout", m, "--repository", reg.repo("addons/tables"), "--tag", "1.2.3")
		Expect(err).NotTo(HaveOccurred())
		Expect(out).To(Equal(first))
		_ = before
		repo, _ := name.NewRepository(reg.repo("addons/tables"))
		d, err := remote.Head(repo.Tag("1.2.3"))
		Expect(err).NotTo(HaveOccurred())
		Expect(first).To(ContainSubstring(d.Digest.String()))
		children, err := childDigests(ctx, repo, d.Digest)
		Expect(err).NotTo(HaveOccurred())
		Expect(children).To(HaveLen(2))
	})
	It("rejects overwriting an immutable version with different content", func() {
		reg := newRegistry()
		p := newProject(both)
		repoName := reg.repo("addons/tables")
		a := p.merged("1.2.3", rev, nil)
		_, err := run("publish", "--config", p.config, "--layout", a, "--repository", repoName, "--tag", "1.2.3")
		Expect(err).NotTo(HaveOccurred())
		b := p.merged("1.2.3", rev, map[string]string{amd: "different bytes"})
		_, err = run("publish", "--config", p.config, "--layout", b, "--repository", repoName, "--tag", "1.2.3")
		expectCode(err, "E_IMMUTABLE")
		repo, _ := name.NewRepository(repoName)
		d, err := remote.Head(repo.Tag("1.2.3"))
		Expect(err).NotTo(HaveOccurred())
		_, da, _ := verifyMerged(a, mustCfg(p))
		Expect(d.Digest).To(Equal(da))
	})
	It("adopts published content only for the same version and commit", func() {
		reg := newRegistry()
		p := newProject(both)
		repoName := reg.repo("addons/tables")
		a := p.merged("1.2.3", rev, nil)
		_, err := run("publish", "--config", p.config, "--layout", a, "--repository", repoName, "--tag", "1.2.3")
		Expect(err).NotTo(HaveOccurred())
		same := p.merged("1.2.3", rev, map[string]string{amd: "rebuilt bytes"})
		out, err := run("publish", "--config", p.config, "--layout", same, "--repository", repoName, "--tag", "1.2.3", "--adopt-existing")
		Expect(err).NotTo(HaveOccurred())
		_, da, _ := verifyMerged(a, mustCfg(p))
		Expect(out).To(ContainSubstring(da.String()))
		other := p.merged("1.2.3", "fedcba9876543210fedcba9876543210fedcba98", map[string]string{amd: "rebuilt bytes"})
		_, err = run("publish", "--config", p.config, "--layout", other, "--repository", repoName, "--tag", "1.2.3", "--adopt-existing")
		expectCode(err, "E_IMMUTABLE")
	})
	It("refuses reserved aliases and mismatched tags as immutable tags", func() {
		reg := newRegistry()
		p := newProject(both)
		m := p.merged("1.2.3", rev, nil)
		for _, tag := range []string{"latest", "development", "branch-x-0123456789ab", "9.9.9", "sha-aaaaaaaaaaaa", ""} {
			_, err := run("publish", "--config", p.config, "--layout", m, "--repository", reg.repo("addons/tables"), "--tag", tag)
			expectCode(err, "E_RESERVED")
		}
		Expect(reg.writeCount()).To(BeZero())
		_, err := run("publish", "--config", p.config, "--layout", m, "--repository", reg.repo("addons/tables"), "--tag", "sha-0123456789ab")
		Expect(err).NotTo(HaveOccurred())
	})
	It("publishes nothing from a partial or local-check layout", func() {
		reg := newRegistry()
		p := newProject(both)
		m := p.merged("1.2.3", rev, nil)
		more := newProject(map[string][]string{amd: nil, arm: nil, "darwin/arm64": nil})
		_, err := run("publish", "--config", more.config, "--layout", m, "--repository", reg.repo("addons/tables"), "--tag", "1.2.3")
		expectCode(err, "E_PLATFORMS")
		Expect(reg.writeCount()).To(BeZero())
	})
})

func mustCfg(p *project) *PlatformsFile {
	GinkgoHelper()
	c, err := loadPlatforms(p.config)
	Expect(err).NotTo(HaveOccurred())
	return c
}

var _ = Describe("alias promotion", func() {
	var (
		reg  *countingRegistry
		p    *project
		repo name.Repository
		ctx  = context.Background()
	)
	publish := func(version, revision, content, tag string) v1.Hash {
		GinkgoHelper()
		m := p.merged(version, revision, map[string]string{amd: content})
		_, err := run("publish", "--config", p.config, "--layout", m, "--repository", reg.repo("addons/tables"), "--tag", tag)
		Expect(err).NotTo(HaveOccurred())
		_, d, err := verifyMerged(m, mustCfg(p))
		Expect(err).NotTo(HaveOccurred())
		return d
	}
	head := func(tag string) v1.Hash {
		GinkgoHelper()
		d, err := remote.Head(repo.Tag(tag))
		Expect(err).NotTo(HaveOccurred())
		return d.Digest
	}
	BeforeEach(func() {
		reg = newRegistry()
		p = newProject(both)
		var err error
		repo, err = name.NewRepository(reg.repo("addons/tables"))
		Expect(err).NotTo(HaveOccurred())
	})

	It("moves development forward and leaves it alone for an older commit", func() {
		gitDir, shas := gitHistory(3)
		old := publish("0.0.0-dev."+shas[0][:12], shas[0], "c0", "sha-"+shas[0][:12])
		mid := publish("0.0.0-dev."+shas[1][:12], shas[1], "c1", "sha-"+shas[1][:12])
		newer := publish("0.0.0-dev."+shas[2][:12], shas[2], "c2", "sha-"+shas[2][:12])
		res, err := promoteAlias(ctx, repo, "development", mid, "", shas[1], gitDir)
		Expect(err).NotTo(HaveOccurred())
		Expect(res).To(Equal("promoted"))
		Expect(head("development")).To(Equal(mid))
		res, err = promoteAlias(ctx, repo, "development", newer, "", shas[2], gitDir)
		Expect(err).NotTo(HaveOccurred())
		Expect(res).To(Equal("promoted"))
		// A slower run of the oldest commit finishes last.
		res, err = promoteAlias(ctx, repo, "development", old, "", shas[0], gitDir)
		Expect(err).NotTo(HaveOccurred())
		Expect(res).To(Equal("stale"))
		Expect(head("development")).To(Equal(newer))
		res, err = promoteAlias(ctx, repo, "development", newer, "", shas[2], gitDir)
		Expect(err).NotTo(HaveOccurred())
		Expect(res).To(Equal("unchanged"))
	})
	It("accepts a diverged branch head and refuses one commit with two contents", func() {
		gitDir, shas := gitHistory(2)
		work := filepath.Dir(gitDir)
		git := func(args ...string) string {
			cmd := exec.Command("git", append([]string{"-C", work, "-c", "user.name=t", "-c", "user.email=t@example.invalid", "-c", "commit.gpgsign=false"}, args...)...)
			cmd.Env = append(os.Environ(), "GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_SYSTEM=/dev/null")
			out, err := cmd.CombinedOutput()
			Expect(err).NotTo(HaveOccurred(), string(out))
			return strings.TrimSpace(string(out))
		}
		git("checkout", "-q", "-b", "rewritten", "HEAD~1")
		git("commit", "-q", "--allow-empty", "-m", "rewritten")
		diverged := git("rev-parse", "HEAD")
		a := publish("0.0.0-dev."+shas[1][:12], shas[1], "x", "sha-"+shas[1][:12])
		res, err := promoteAlias(ctx, repo, "development", a, "", shas[1], gitDir)
		Expect(err).NotTo(HaveOccurred())
		Expect(res).To(Equal("promoted"))
		d := publish("0.0.0-dev."+diverged[:12], diverged, "z", "sha-"+diverged[:12])
		res, err = promoteAlias(ctx, repo, "development", d, "", diverged, gitDir)
		Expect(err).NotTo(HaveOccurred())
		Expect(res).To(Equal("promoted"))
		same := publish("0.0.1", diverged, "y", "0.0.1")
		_, err = promoteAlias(ctx, repo, "development", same, "", diverged, gitDir)
		expectCode(err, "E_IMMUTABLE")
		Expect(head("development")).To(Equal(d))
	})
	Context("when the commit the alias names is not in the checkout", func() {
		const gone = "1111111111111111111111111111111111111111"
		var gitDir, current string
		BeforeEach(func() {
			var shas []string
			gitDir, shas = gitHistory(1)
			current = shas[0]
		})
		It("treats a force-pushed branch as the new truth for a push to the same ref", func() {
			ref := "refs/heads/feature/x"
			tag := branchTag(ref)
			oldD := publish("0.0.0-dev."+gone[:12], gone, "old", "sha-"+gone[:12])
			_, err := promoteAlias(ctx, repo, tag, oldD, "", gone, gitDir)
			Expect(err).NotTo(HaveOccurred())
			newD := publish("0.0.0-dev."+current[:12], current, "new", "sha-"+current[:12])
			res, err := promoteAliasFrom(ctx, repo, tag, newD, "", current, gitDir, promotionSource{Ref: ref, Event: "push"})
			Expect(err).NotTo(HaveOccurred())
			Expect(res).To(Equal("promoted"))
			Expect(head(tag)).To(Equal(newD))
		})
		It("blocks the alias for anything but a push to the channel's own ref", func() {
			ref := "refs/heads/feature/x"
			tag := branchTag(ref)
			oldD := publish("0.0.0-dev."+gone[:12], gone, "old", "sha-"+gone[:12])
			_, err := promoteAlias(ctx, repo, tag, oldD, "", gone, gitDir)
			Expect(err).NotTo(HaveOccurred())
			newD := publish("0.0.0-dev."+current[:12], current, "new", "sha-"+current[:12])
			for _, src := range []promotionSource{
				{},
				{Ref: ref, Event: "release"},
				{Ref: "refs/heads/other", Event: "push"},
				{Ref: "refs/tags/v1.0.0", Event: "push"},
				{Ref: ref, Event: ""},
			} {
				_, err := promoteAliasFrom(ctx, repo, tag, newD, "", current, gitDir, src)
				expectCode(err, "E_UNREACHABLE")
				Expect(head(tag)).To(Equal(oldD))
			}
		})
		It("refuses development when its commit is unreachable, even for a push", func() {
			oldD := publish("0.0.0-dev."+gone[:12], gone, "old", "sha-"+gone[:12])
			_, err := promoteAlias(ctx, repo, "development", oldD, "", gone, gitDir)
			Expect(err).NotTo(HaveOccurred())
			newD := publish("0.0.0-dev."+current[:12], current, "new", "sha-"+current[:12])
			_, err = promoteAliasFrom(ctx, repo, "development", newD, "", current, gitDir, promotionSource{Ref: "refs/heads/master", Event: "push"})
			expectCode(err, "E_UNREACHABLE")
			Expect(head("development")).To(Equal(oldD))
		})
		It("fails on any git error that is not a plain not-ancestor answer", func() {
			bin := GinkgoT().TempDir()
			for name, body := range map[string]string{
				"exit2":   "echo boom >&2; exit 2",
				"exit128": "echo 'fatal: not a git repository' >&2; exit 128",
			} {
				dir := filepath.Join(bin, name)
				Expect(os.MkdirAll(dir, 0o755)).To(Succeed())
				Expect(os.WriteFile(filepath.Join(dir, "git"), []byte("#!/bin/sh\n"+body+"\n"), 0o755)).To(Succeed())
				GinkgoT().Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
				_, err := isAncestor(ctx, gitDir, current, gone)
				Expect(err).To(HaveOccurred(), name)
				Expect(errors.Is(err, errCommitUnreachable)).To(BeFalse(), name)
			}
		})
		It("tells an unreachable commit from a plain not-ancestor", func() {
			_, err := isAncestor(ctx, gitDir, current, gone)
			Expect(errors.Is(err, errCommitUnreachable)).To(BeTrue())
			_, err = isAncestor(ctx, gitDir, gone, current)
			Expect(err).To(HaveOccurred())
			Expect(errors.Is(err, errCommitUnreachable)).To(BeFalse(), "a missing commit of this run is not a rewritten branch")
			ok, err := isAncestor(ctx, gitDir, current, current)
			Expect(err).NotTo(HaveOccurred())
			Expect(ok).To(BeTrue())
		})
	})
	It("moves latest only to a newer stable version", func() {
		v1d := publish("1.0.0", rev, "one", "1.0.0")
		v2d := publish("1.1.0", rev, "two", "1.1.0")
		res, err := promoteAlias(ctx, repo, "latest", v2d, "1.1.0", rev, "")
		Expect(err).NotTo(HaveOccurred())
		Expect(res).To(Equal("promoted"))
		res, err = promoteAlias(ctx, repo, "latest", v1d, "1.0.0", rev, "")
		Expect(err).NotTo(HaveOccurred())
		Expect(res).To(Equal("stale"))
		Expect(head("latest")).To(Equal(v2d))
		_, err = promoteAlias(ctx, repo, "latest", v2d, "1.2.0-rc.1", rev, "")
		expectCode(err, "E_RESERVED")
		other := publish("1.1.0", "fedcba9876543210fedcba9876543210fedcba98", "three", "sha-fedcba987654")
		_, err = promoteAlias(ctx, repo, "latest", other, "1.1.0", rev, "")
		expectCode(err, "E_IMMUTABLE")
		Expect(head("latest")).To(Equal(v2d))
	})
	It("refuses reserved or foreign alias names and unpublished digests, leaving aliases unchanged", func() {
		d := publish("1.0.0", rev, "one", "1.0.0")
		for _, alias := range []string{"1.0.0", "sha-0123456789ab", "main", "branch-", "Latest", ""} {
			_, err := promoteAlias(ctx, repo, alias, d, "1.0.0", rev, "")
			expectCode(err, "E_RESERVED")
		}
		missing, _ := v1.NewHash("sha256:" + strings.Repeat("a", 64))
		before := reg.writeCount()
		_, err := promoteAlias(ctx, repo, "latest", missing, "1.0.0", rev, "")
		Expect(err).To(HaveOccurred())
		Expect(reg.writeCount()).To(Equal(before))
		_, err = remote.Head(repo.Tag("latest"))
		Expect(err).To(HaveOccurred())
	})
	It("promotes a branch channel under its hashed name", func() {
		gitDir, shas := gitHistory(1)
		tag := branchTag("refs/heads/feature/x")
		d := publish("0.0.0-dev."+shas[0][:12], shas[0], "b", "sha-"+shas[0][:12])
		res, err := promoteAlias(ctx, repo, tag, d, "", shas[0], gitDir)
		Expect(err).NotTo(HaveOccurred())
		Expect(res).To(Equal("promoted"))
		Expect(head(tag)).To(Equal(d))
		_, err = promoteAlias(ctx, repo, "branch-feature-x", d, "", shas[0], gitDir)
		expectCode(err, "E_RESERVED")
	})
})

var _ = Describe("catalog template", func() {
	It("renders digest-pinned entries with an explicit add-on signer identity", func() {
		reg := newRegistry()
		p := newProject(both)
		m := p.merged("1.2.3", rev, nil)
		repoName := reg.repo("addons/tables")
		out := filepath.Join(GinkgoT().TempDir(), "catalog.yaml")
		render := func(extra ...string) error {
			args := append([]string{"catalog", "--config", p.config, "--template", "../../../gallery/addons/tables.yaml.tmpl", "--merged", m, "--repository", repoName, "--tag", "1.2.3", "--channel", "latest", "--owner-repo", "example/localai", "--signer-ref", "refs/tags/v1.2.3", "--out", out}, extra...)
			_, err := run(args...)
			return err
		}
		Expect(render()).To(Succeed())
		b, err := os.ReadFile(out)
		Expect(err).NotTo(HaveOccurred())
		Expect(string(b)).To(HavePrefix("- addonId: "))
		entries, err := readCatalogIndex(b)
		Expect(err).NotTo(HaveOccurred())
		Expect(entries).To(HaveLen(2))
		cfg := mustCfg(p)
		_, _, err = verifyMerged(m, cfg)
		Expect(err).NotTo(HaveOccurred())
		for _, e := range entries {
			Expect(e.Digest).To(MatchRegexp(`^sha256:[0-9a-f]{64}$`))
			Expect(e.Artifact).To(Equal("oci://" + repoName + ":1.2.3"))
			Expect(e.Policy.RequireSignature).To(BeTrue())
			Expect(e.Policy.Identity).To(Equal("https://github.com/example/localai/.github/workflows/addons-package.yml@refs/tags/v1.2.3"))
			Expect(e.Policy.Issuer).To(Equal("https://token.actions.githubusercontent.com"))
			Expect(e.Channel).To(Equal("latest"))
			Expect(e.UI.MainPage).To(Equal("databases"))
			Expect(e.Policy.Identity).NotTo(ContainSubstring("backend"))
		}
		Expect(entries[0].Digest).NotTo(Equal(entries[1].Digest))
		Expect(entries[0].Arch).NotTo(Equal(entries[1].Arch))
	})
	It("renders an entry for every first-party add-on from its own template", func() {
		for _, id := range []string{"tables", "voice", "face", "fine-tuning", "quantization", "studio", "talk"} {
			p := newProjectFor(id, both)
			m := p.merged("1.2.3", rev, nil)
			repoName := newRegistry().repo("addons/" + id)
			out := filepath.Join(GinkgoT().TempDir(), "catalog.yaml")
			_, err := run("catalog", "--config", p.config, "--template", "../../../gallery/addons/"+id+".yaml.tmpl", "--merged", m, "--repository", repoName, "--tag", "1.2.3", "--channel", "latest", "--owner-repo", "example/localai", "--signer-ref", "refs/tags/v1.2.3", "--out", out)
			Expect(err).NotTo(HaveOccurred(), id)
			b, err := os.ReadFile(out)
			Expect(err).NotTo(HaveOccurred())
			entries, err := readCatalogIndex(b)
			Expect(err).NotTo(HaveOccurred(), id)
			Expect(entries).To(HaveLen(2), id)
			for _, e := range entries {
				Expect(e.AddonID).To(Equal(id))
				Expect(e.Policy.RequireSignature).To(BeTrue())
			}
		}
	})
	It("refuses missing inputs, backend repositories, bad channels and unknown placeholders", func() {
		reg := newRegistry()
		p := newProject(both)
		m := p.merged("1.2.3", rev, nil)
		out := filepath.Join(GinkgoT().TempDir(), "c.yaml")
		base := func(over map[string]string) error {
			vals := map[string]string{"--config": p.config, "--template": "../../../gallery/addons/tables.yaml.tmpl", "--merged": m, "--repository": reg.repo("addons/tables"), "--tag": "1.2.3", "--channel": "latest", "--owner-repo": "example/localai", "--signer-ref": "refs/tags/v1.2.3", "--out": out}
			for k, v := range over {
				vals[k] = v
			}
			var args []string
			for k, v := range vals {
				args = append(args, k, v)
			}
			_, err := run(append([]string{"catalog"}, args...)...)
			return err
		}
		Expect(base(nil)).To(Succeed())
		Expect(base(map[string]string{"--repository": ""})).To(HaveOccurred())
		Expect(base(map[string]string{"--repository": "quay.io/go-skynet/x"})).To(HaveOccurred())
		Expect(base(map[string]string{"--channel": "stable"})).To(HaveOccurred())
		Expect(base(map[string]string{"--channel": "1.2.3"})).To(HaveOccurred())
		Expect(base(map[string]string{"--owner-repo": ""})).To(HaveOccurred())
		Expect(base(map[string]string{"--signer-ref": "refs/pull/1/merge"})).To(HaveOccurred())
		Expect(base(map[string]string{"--tag": "latest"})).To(HaveOccurred())
		bad := filepath.Join(GinkgoT().TempDir(), "bad.tmpl")
		tmpl, _ := os.ReadFile("../../../gallery/addons/tables.yaml.tmpl")
		Expect(os.WriteFile(bad, bytes.ReplaceAll(tmpl, []byte("{{arch}}"), []byte("{{unknown}}")), 0o644)).To(Succeed())
		Expect(base(map[string]string{"--template": bad})).To(HaveOccurred())
		weak := filepath.Join(GinkgoT().TempDir(), "weak.tmpl")
		Expect(os.WriteFile(weak, bytes.ReplaceAll(tmpl, []byte("requireSignature: true"), []byte("requireSignature: false")), 0o644)).To(Succeed())
		Expect(base(map[string]string{"--template": weak})).To(HaveOccurred())
	})
	It("carries placeholders only: no fabricated URL, registry or namespace", func() {
		b, err := os.ReadFile("../../../gallery/addons/tables.yaml.tmpl")
		Expect(err).NotTo(HaveOccurred())
		text := string(b)
		for _, bad := range []string{"http://", "https://", "quay.io", "docker.io", "ghcr.io", "go-skynet"} {
			Expect(text).NotTo(ContainSubstring(bad))
		}
		for _, need := range []string{"{{repository}}", "{{digest}}", "{{identity}}", "{{issuer}}", "{{channel}}", "{{os}}", "{{arch}}", "{{version}}"} {
			Expect(text).To(ContainSubstring(need))
		}
	})
})

var _ = Describe("workflow policy", func() {
	const file = "../../../.github/workflows/addons-package.yml"
	var base string
	BeforeEach(func() {
		b, err := os.ReadFile(file)
		Expect(err).NotTo(HaveOccurred())
		base = string(b)
	})
	mutate := func(old, new string) []string {
		GinkgoHelper()
		Expect(strings.Count(base, old)).To(BeNumerically(">=", 1), "mutation anchor %q not found", old)
		return checkWorkflow([]byte(strings.Replace(base, old, new, 1)))
	}
	expect := func(problems []string, code string) {
		GinkgoHelper()
		Expect(strings.Join(problems, "\n")).To(ContainSubstring(code + ":"))
	}
	It("accepts the checked-in workflow", func() {
		Expect(checkWorkflow([]byte(base))).To(BeEmpty())
	})
	It("rejects pull_request_target", func() {
		expect(mutate("  release:\n    types: [published]", "  pull_request_target:\n    types: [opened]\n  release:\n    types: [published]"), "P_PR_TARGET")
	})
	It("rejects secrets in a pull request validation job", func() {
		expect(mutate("        run: sh scripts/addons/package_test.sh", "        env:\n          T: ${{ secrets.ADDON_OCI_PASSWORD }}\n        run: sh scripts/addons/package_test.sh"), "P_UNTRUSTED_SECRETS")
	})
	It("rejects id-token write outside the publishing job", func() {
		expect(mutate("    permissions:\n      contents: read\n    strategy:", "    permissions:\n      contents: read\n      id-token: write\n    strategy:"), "P_UNTRUSTED_SECRETS")
	})
	It("rejects a registry login or publish in an untrusted job", func() {
		expect(mutate("      - name: Run the packaging checks\n        if: matrix.addon == 'tables'\n        run: sh scripts/addons/package_test.sh", "      - name: Run the packaging checks\n        run: go run ./scripts/addons/cmd/addonpkg publish --layout x"), "P_UNTRUSTED_SECRETS")
	})
	It("rejects publish that does not wait for every platform leg", func() {
		expect(mutate("    needs: [plan, validate, package]\n    if: needs.plan.outputs.publish == 'true'", "    needs: [plan, validate]\n    if: needs.plan.outputs.publish == 'true'"), "P_NEEDS")
	})
	It("rejects publish that can run after a failed leg", func() {
		expect(mutate("    needs: [plan, validate, package]\n    if: needs.plan.outputs.publish == 'true'", "    needs: [plan, package]\n    if: always() && needs.plan.outputs.publish == 'true'"), "P_TRUSTED_GATE")
	})
	It("rejects publish that does not need validate", func() {
		expect(mutate("    needs: [plan, validate, package]\n    if: needs.plan.outputs.publish == 'true'", "    needs: [plan, package]\n    if: needs.plan.outputs.publish == 'true'"), "P_NEEDS")
	})
	It("rejects a package leg that does not need validate", func() {
		expect(mutate("    needs: [plan, validate]\n    if: needs.plan.outputs.has_platforms == 'true'", "    needs: [plan]\n    if: needs.plan.outputs.has_platforms == 'true'"), "P_NEEDS")
	})
	It("rejects a validate job that cannot stop the release", func() {
		anchor := "    name: validate package rules\n"
		expect(mutate(anchor, anchor+"    continue-on-error: true\n"), "P_VALIDATE_GATE")
		expect(mutate(anchor, anchor+"    continue-on-error: ${{ github.event_name != 'push' }}\n"), "P_VALIDATE_GATE")
		expect(mutate(anchor, anchor+"    if: always()\n"), "P_VALIDATE_GATE")
		expect(mutate(anchor, anchor+"    if: failure() || success()\n"), "P_VALIDATE_GATE")
		expect(mutate("        run: sh scripts/addons/package_test.sh", "        continue-on-error: true\n        run: sh scripts/addons/package_test.sh"), "P_VALIDATE_GATE")
	})
	It("rejects a package or promote job that runs after a failed validate", func() {
		expect(mutate("    if: needs.plan.outputs.has_platforms == 'true'", "    if: always() && needs.plan.outputs.has_platforms == 'true'"), "P_VALIDATE_GATE")
		expect(mutate("    if: needs.plan.outputs.publish == 'true' && needs.plan.outputs.aliases != ''", "    if: always() && needs.plan.outputs.publish == 'true' && needs.plan.outputs.aliases != ''"), "P_VALIDATE_GATE")
	})
	It("keeps validate unprivileged on every event", func() {
		var doc struct {
			Jobs map[string]map[string]any `yaml:"jobs"`
		}
		Expect(yaml.Unmarshal([]byte(base), &doc)).To(Succeed())
		v := doc.Jobs["validate"]
		Expect(v).NotTo(HaveKey("if"))
		Expect(v).NotTo(HaveKey("environment"))
		Expect(fmt.Sprint(v)).NotTo(ContainSubstring("secrets."))
		Expect(fmt.Sprint(v)).NotTo(ContainSubstring("id-token"))
	})
	It("rejects promotion that does not wait for signing", func() {
		expect(mutate("    needs: [plan, validate, publish]", "    needs: [plan, validate]"), "P_NEEDS")
	})
	It("rejects alias promotion before signing", func() {
		expect(mutate("        id: sign\n", "        id: verify\n"), "P_ORDER")
	})
	It("rejects an alias command inside the publishing job", func() {
		expect(mutate("            --layout merged --repository \"$ADDON_OCI_REPOSITORY\" --tag \"$IMMUTABLE\" --adopt-existing | tee push.txt", "            --layout merged --repository \"$ADDON_OCI_REPOSITORY\" --tag \"$IMMUTABLE\" --adopt-existing | tee push.txt\n          go run ./scripts/addons/cmd/addonpkg promote --alias development"), "P_ORDER")
	})
	It("rejects a literal latest tag", func() {
		expect(mutate("            echo \"has_platforms=$has\"", "            echo \"has_platforms=$has\"\n            echo \"aliases=latest\""), "P_LATEST")
	})
	It("rejects backend registries and backend workflows", func() {
		expect(mutate("ADDON_OCI_REPOSITORY: ${{ vars.ADDON_OCI_REPOSITORY }}", "ADDON_OCI_REPOSITORY: quay.io/go-skynet/ci-cache"), "P_BACKEND")
		expect(mutate("    needs: [plan, validate, publish]", "    needs: [plan, validate, publish]\n    uses: ./.github/workflows/backend_merge.yml"), "P_BACKEND")
		expect(mutate("          username: ${{ secrets.ADDON_OCI_USERNAME }}", "          username: ${{ secrets.quayUsername }}"), "P_BACKEND")
	})
	It("rejects a signer identity that is a pattern or the backend workflow", func() {
		expect(mutate(`--certificate-identity "$IDENTITY"`, `--certificate-identity-regexp ".*"`), "P_SIGNER")
		expect(mutate("addons-package.yml@${{ github.ref }}", "backend_merge.yml@${{ github.ref }}"), "P_SIGNER")
	})
	It("rejects signing by tag or without referrers and the new bundle format", func() {
		expect(mutate(`"${ADDON_OCI_REPOSITORY}@${DIGEST}"`, `"${ADDON_OCI_REPOSITORY}:1.2.3"`), "P_SIGNER")
		expect(mutate("            --registry-referrers-mode=oci-1-1 \\\n", ""), "P_SIGNER")
	})
	It("rejects missing serialization per channel", func() {
		expect(mutate("      group: addons-promote-${{ matrix.addon }}-${{ needs.plan.outputs.channel }}\n      cancel-in-progress: false", "      group: addons-promote-${{ matrix.addon }}-${{ needs.plan.outputs.channel }}\n      cancel-in-progress: true"), "P_CONCURRENCY")
		expect(mutate("addons-promote-${{ matrix.addon }}-${{ needs.plan.outputs.channel }}", "addons-promote-shared"), "P_CONCURRENCY")
	})
	It("rejects publication that is shared between add-ons", func() {
		expect(mutate("addons-promote-${{ matrix.addon }}-", "addons-promote-"), "P_CONCURRENCY")
		expect(mutate("addons-publish-${{ matrix.addon }}-", "addons-publish-"), "P_CONCURRENCY")
		expect(mutate("        addon: ${{ fromJSON(needs.plan.outputs.addons) }}\n    env:\n      ADDON_OCI_BASE: ${{ vars.ADDON_OCI_REPOSITORY }}\n      ADDON: ${{ matrix.addon }}\n      #", "      #"), "P_MATRIX")
		expect(mutate("repo=\"${ADDON_OCI_BASE%/*}/$ADDON\"", "repo=\"$ADDON_OCI_BASE\""), "P_REPOSITORY")
		expect(mutate("      COSIGN_EXPERIMENTAL: '1'\n", "      COSIGN_EXPERIMENTAL: '1'\n    outputs:\n      digest: x\n"), "P_ORDER")
	})
	It("rejects a workflow that names one add-on by path or skips an add-on in the matrix", func() {
		expect(mutate("node \"addons/$ADDON/ui/build.mjs\"\n      - name: Fill", "node addons/tables/ui/build.mjs\n      - name: Fill"), "P_ADDON_PATH")
		expect(mutate("        addon: [tables, voice, face, fine-tuning, quantization, studio, talk]\n", "        platform: [x]\n"), "P_MATRIX")
	})
	It("rejects a plan that does not refuse an add-on without an enabled platform", func() {
		expect(mutate("has no enabled platform; refusing to publish", "has no platform"), "P_FAIL_CLOSED")
	})
	It("rejects a plan that does not exit 1 for an add-on without an enabled platform on a publishing event", func() {
		expect(mutate("                echo \"add-on $id has no enabled platform; refusing to publish\" >&2\n                exit 1", "                echo \"add-on $id has no enabled platform; refusing to publish\" >&2\n                continue"), "P_FAIL_CLOSED")
		expect(mutate("              if [ \"$publish\" = \"true\" ]; then\n                echo \"add-on $id", "              if [ \"$publish\" = \"never\" ]; then\n                echo \"add-on $id"), "P_FAIL_CLOSED")
	})
	It("rejects a plan that treats any matrix error as no legs", func() {
		expect(mutate("            elif grep -q 'no enabled platform' \"$err\"; then", "            elif true; then"), "P_FAIL_CLOSED")
		expect(mutate("              echo \"cannot read the platform matrix of add-on $id\" >&2\n              cat \"$err\" >&2\n              exit 1", "              continue"), "P_FAIL_CLOSED")
	})
	It("rejects a repository rule that does not keep the configured repository for tables", func() {
		expect(mutate("          if [ \"$ADDON\" = tables ]; then\n            repo=\"$ADDON_OCI_BASE\"", "          if [ \"$ADDON\" = never ]; then\n            repo=\"$ADDON_OCI_BASE\""), "P_REPOSITORY")
		expect(mutate("            repo=\"$ADDON_OCI_BASE\"\n          else", "            repo=\"${ADDON_OCI_BASE%/*}/$ADDON\"\n          else"), "P_REPOSITORY")
	})
	It("rejects a publish or promote matrix that stops the other add-ons on a failure", func() {
		expect(mutate("      # A leg that is pushing must finish, so one failure never cancels another.\n      fail-fast: false", "      fail-fast: true"), "P_FAIL_FAST")
		expect(mutate("    permissions:\n      contents: read\n    strategy:\n      fail-fast: false\n      matrix:\n        addon: ${{ fromJSON(needs.plan.outputs.addons) }}\n    env:\n      ADDON_OCI_BASE: ${{ vars.ADDON_OCI_REPOSITORY }}\n      ADDON: ${{ matrix.addon }}\n    steps:", "    permissions:\n      contents: read\n    strategy:\n      fail-fast: true\n      matrix:\n        addon: ${{ fromJSON(needs.plan.outputs.addons) }}\n    env:\n      ADDON_OCI_BASE: ${{ vars.ADDON_OCI_REPOSITORY }}\n      ADDON: ${{ matrix.addon }}\n    steps:"), "P_FAIL_FAST")
	})
	It("rejects a publish job that downloads the layouts of other add-ons", func() {
		expect(mutate("pattern: layout-${{ matrix.addon }}-*", "pattern: layout-*"), "P_ARTIFACTS")
		expect(mutate("pattern: layout-${{ matrix.addon }}-*", "pattern: layout-${{ matrix.addon }}*"), "P_ARTIFACTS")
	})
	It("rejects a promote job that does not check the digest format", func() {
		expect(mutate("            sha256:????????????????????????????????????????????????????????????????) ;;", "            sha256:*) ;;"), "P_DIGEST")
		expect(mutate("the digest of $ADDON is missing or malformed", "the digest of $ADDON is wrong"), "P_DIGEST")
	})
	It("rejects a plan that does not fail closed on missing configuration", func() {
		expect(mutate(`if [ -z "$ADDON_OCI_REPOSITORY" ]; then`, `if [ -n "$OTHER" ]; then`), "P_FAIL_CLOSED")
	})
	It("rejects a checkout of the pull request head or a persisted token", func() {
		expect(mutate("persist-credentials: false", "persist-credentials: true"), "P_CHECKOUT")
		expect(mutate("      - uses: actions/checkout@v7\n        with:\n          persist-credentials: false", "      - uses: actions/checkout@v7\n        with:\n          ref: ${{ github.event.pull_request.head.sha }}\n          persist-credentials: false"), "P_CHECKOUT")
	})
	It("rejects write permission at workflow level and a literal platform matrix", func() {
		expect(mutate("permissions:\n  contents: read\n\njobs:", "permissions:\n  contents: write\n\njobs:"), "P_PERMISSIONS")
		expect(mutate("      matrix: ${{ fromJSON(needs.plan.outputs.matrix) }}", "      matrix:\n        include:\n          - platform: linux/amd64\n            runner: ubuntu-24.04\n            slug: linux-amd64"), "P_MATRIX")
	})
	It("rejects a publishing job without an environment or a gate", func() {
		expect(mutate("    environment: addons-release\n    concurrency:\n      group: addons-publish", "    concurrency:\n      group: addons-publish"), "P_TRUSTED_GATE")
		expect(mutate("    if: needs.plan.outputs.publish == 'true' && needs.plan.outputs.aliases != ''", "    if: needs.plan.outputs.aliases != ''"), "P_TRUSTED_GATE")
	})
	It("does not trust the backend workflow identity in the documented verification", func() {
		Expect(strings.Contains(base, "backend_merge")).To(BeFalse())
	})
})

// firstPartyAddons are the add-ons the release workflow packages. The list is
// written out so a new add-on is a deliberate change here and in the workflow.
var firstPartyAddons = []string{"tables", "voice", "face", "fine-tuning", "quantization", "studio", "talk"}

func readWorkflow(t *testing.T) string {
	t.Helper()
	b, err := os.ReadFile("../../../.github/workflows/addons-package.yml")
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestTheWorkflowHasNoHardCodedAddonPath(t *testing.T) {
	text := readWorkflow(t)
	for i, line := range strings.Split(text, "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "#") {
			continue
		}
		if strings.Contains(line, "addons/tables/") {
			t.Errorf("line %d names addons/tables/ outside the add-on list: %s", i+1, trimmed)
		}
	}
}

func TestTheWorkflowRunsEveryFirstPartyAddon(t *testing.T) {
	var doc struct {
		Jobs map[string]struct {
			Strategy struct {
				Matrix any `yaml:"matrix"`
			} `yaml:"strategy"`
			Steps []map[string]any `yaml:"steps"`
		} `yaml:"jobs"`
	}
	if err := yaml.Unmarshal([]byte(readWorkflow(t)), &doc); err != nil {
		t.Fatal(err)
	}
	// validate lists the add-ons literally. The other jobs take the add-ons
	// that have an enabled platform from the plan.
	list := func(v any) []string {
		var out []string
		items, _ := v.([]any)
		for _, x := range items {
			out = append(out, fmt.Sprint(x))
		}
		return out
	}
	matrixOf := func(job string) map[string]any { m, _ := doc.Jobs[job].Strategy.Matrix.(map[string]any); return m }
	got := list(matrixOf("validate")["addon"])
	if strings.Join(got, ",") != strings.Join(firstPartyAddons, ",") {
		t.Fatalf("validate matrix lists %v, want %v", got, firstPartyAddons)
	}
	for _, job := range []string{"publish", "promote"} {
		if _, ok := matrixOf(job)["addon"]; !ok {
			t.Errorf("%s has no addon matrix", job)
		}
	}
	if !strings.Contains(fmt.Sprint(doc.Jobs["package"].Strategy.Matrix), "needs.plan.outputs.matrix") {
		t.Error("package does not take its legs from the plan")
	}
	// The plan loops over the same list.
	plan := readWorkflow(t)
	if !strings.Contains(plan, "for id in "+strings.Join(firstPartyAddons, " ")+"; do") {
		t.Errorf("plan does not loop over exactly %v", firstPartyAddons)
	}
	for _, id := range firstPartyAddons {
		file := "../../../addons/" + id + "/package/platforms.yaml"
		cfg, err := loadPlatforms(file)
		if err != nil {
			t.Errorf("%s: %v", id, err)
			continue
		}
		if cfg.Addon != id || cfg.Manifest != "addons/"+id+"/manifest.json" {
			t.Errorf("%s: platforms file names add-on %q and manifest %q", id, cfg.Addon, cfg.Manifest)
		}
		if cfg.UI != "addons/"+id+"/ui/dist" {
			t.Errorf("%s: ui is %q", id, cfg.UI)
		}
		if _, err := os.Stat("../../../addons/" + id + "/cmd/" + cfg.Executable + "/main.go"); err != nil {
			t.Errorf("%s: no executable source for %q: %v", id, cfg.Executable, err)
		}
	}
}

// documentedAddonRepository is the value the documentation gives for the
// ADDON_OCI_REPOSITORY variable: the Tables repository in the add-on namespace.
const documentedAddonRepository = "quay.io/go-skynet/local-ai-addons/tables"

// workflowStepScript returns the shell script and env of a named step of a job.
func workflowStepScript(workflow, job, step string) (script string) {
	GinkgoHelper()
	var doc struct {
		Jobs map[string]struct {
			Steps []struct {
				Name string `yaml:"name"`
				ID   string `yaml:"id"`
				Run  string `yaml:"run"`
			} `yaml:"steps"`
		} `yaml:"jobs"`
	}
	Expect(yaml.Unmarshal([]byte(workflow), &doc)).To(Succeed())
	for _, st := range doc.Jobs[job].Steps {
		if st.Name == step || st.ID == step {
			return st.Run
		}
	}
	Fail(fmt.Sprintf("job %s has no step %q", job, step))
	return ""
}

var _ = Describe("workflow behavior", func() {
	var workflow string
	BeforeEach(func() {
		b, err := os.ReadFile("../../../.github/workflows/addons-package.yml")
		Expect(err).NotTo(HaveOccurred())
		workflow = string(b)
	})

	It("derives a repository that the add-on tools accept for every add-on from the documented variable", func() {
		docs, err := os.ReadFile("../../../docs/content/features/addons.md")
		Expect(err).NotTo(HaveOccurred())
		Expect(string(docs)).To(ContainSubstring("`" + documentedAddonRepository + "`"))
		for _, job := range []string{"publish", "promote"} {
			script := workflowStepScript(workflow, job, "Choose the repository of this add-on")
			for _, id := range firstPartyAddons {
				env := filepath.Join(GinkgoT().TempDir(), "github-env")
				Expect(os.WriteFile(env, nil, 0o644)).To(Succeed())
				cmd := exec.Command("sh", "-c", script)
				cmd.Env = append(os.Environ(), "ADDON_OCI_BASE="+documentedAddonRepository, "ADDON="+id, "GITHUB_ENV="+env)
				out, err := cmd.CombinedOutput()
				Expect(err).NotTo(HaveOccurred(), string(out))
				written, err := os.ReadFile(env)
				Expect(err).NotTo(HaveOccurred())
				repo := strings.TrimSpace(strings.TrimPrefix(string(written), "ADDON_OCI_REPOSITORY="))
				if id == "tables" {
					Expect(repo).To(Equal(documentedAddonRepository), "%s keeps the configured repository", job)
				} else {
					Expect(repo).To(Equal("quay.io/go-skynet/local-ai-addons/"+id), job)
				}
				_, err = parseRepository(repo)
				Expect(err).NotTo(HaveOccurred(), "%s: %s for %s", job, repo, id)
			}
		}
	})

	Describe("the plan step", func() {
		const platforms = "schema: 1\naddon: %s\nexecutable: %s\nmanifest: addons/%s/manifest.json\nui: addons/%s/ui/dist\nplatforms:\n  - platform: linux/amd64\n    enabled: %t\n    runner: ubuntu-24.04\n    native_check: executable-start\n"
		var tool string
		BeforeEach(func() {
			tool = filepath.Join(GinkgoT().TempDir(), "addonpkg")
			cmd := exec.Command("go", "build", "-o", tool, "./scripts/addons/cmd/addonpkg")
			cmd.Dir = "../../.."
			cmd.Env = append(os.Environ(), "CGO_ENABLED=0")
			out, err := cmd.CombinedOutput()
			Expect(err).NotTo(HaveOccurred(), string(out))
		})
		// plan runs the plan step in a scratch checkout. enabled names the
		// add-ons with an enabled platform; broken maps an add-on to the
		// content of its platform file ("" removes the file).
		plan := func(event, ref string, enabled []string, broken map[string]string) (string, string, error) {
			GinkgoHelper()
			root, err := filepath.Abs("../../..")
			Expect(err).NotTo(HaveOccurred())
			dir := GinkgoT().TempDir()
			Expect(os.Symlink(filepath.Join(root, "scripts"), filepath.Join(dir, "scripts"))).To(Succeed())
			for _, id := range firstPartyAddons {
				Expect(os.MkdirAll(filepath.Join(dir, "addons", id, "package"), 0o755)).To(Succeed())
				content := fmt.Sprintf(platforms, id, id, id, id, contains(enabled, id))
				if b, ok := broken[id]; ok {
					content = b
				}
				if content != "" {
					Expect(os.WriteFile(filepath.Join(dir, "addons", id, "package", "platforms.yaml"), []byte(content), 0o644)).To(Succeed())
				}
			}
			bin := filepath.Join(dir, "bin")
			Expect(os.MkdirAll(bin, 0o755)).To(Succeed())
			stub := "#!/bin/sh\n[ \"$1\" = run ] || exit 99\nshift 2\nexec \"" + tool + "\" \"$@\"\n"
			Expect(os.WriteFile(filepath.Join(bin, "go"), []byte(stub), 0o755)).To(Succeed())
			output := filepath.Join(dir, "github-output")
			Expect(os.WriteFile(output, nil, 0o644)).To(Succeed())
			cmd := exec.Command("sh", "-c", workflowStepScript(workflow, "plan", "plan"))
			cmd.Dir = dir
			cmd.Env = append(os.Environ(), "PATH="+bin+string(os.PathListSeparator)+os.Getenv("PATH"),
				"EVENT="+event, "REF="+ref, "SHA="+rev, "DRAFT=false", "PRERELEASE=false",
				"ADDON_OCI_REPOSITORY="+documentedAddonRepository, "GITHUB_OUTPUT="+output)
			out, runErr := cmd.CombinedOutput()
			written, err := os.ReadFile(output)
			Expect(err).NotTo(HaveOccurred())
			return string(written), string(out), runErr
		}
		all := firstPartyAddons

		It("fails on a publishing event when an add-on has no enabled platform", func() {
			_, out, err := plan("push", "refs/heads/master", []string{"tables", "face"}, nil)
			Expect(err).To(HaveOccurred())
			Expect(out).To(ContainSubstring("add-on voice has no enabled platform; refusing to publish"))
		})
		It("publishes when every add-on has an enabled platform", func() {
			written, out, err := plan("push", "refs/heads/master", all, nil)
			Expect(err).NotTo(HaveOccurred(), out)
			Expect(written).To(ContainSubstring("publish=true"))
			Expect(written).To(ContainSubstring("has_platforms=true"))
			for _, id := range all {
				Expect(written).To(ContainSubstring(`"addon":"` + id + `"`))
			}
		})
		It("gives an add-on without an enabled platform no legs on an event that publishes nothing", func() {
			written, out, err := plan("pull_request", "refs/pull/1/merge", []string{"tables"}, nil)
			Expect(err).NotTo(HaveOccurred(), out)
			Expect(written).To(ContainSubstring("publish=false"))
			Expect(written).To(ContainSubstring(`addons=["tables"]`))
			written, out, err = plan("pull_request", "refs/pull/1/merge", nil, nil)
			Expect(err).NotTo(HaveOccurred(), out)
			Expect(written).To(ContainSubstring("has_platforms=false"))
		})
		It("fails on a missing or malformed platform file on every event", func() {
			for _, c := range []struct{ event, ref string }{{"pull_request", "refs/pull/1/merge"}, {"push", "refs/heads/feature"}, {"push", "refs/heads/master"}} {
				for name, content := range map[string]string{"missing": "", "malformed": "schema: [", "unsupported": "schema: 9\naddon: voice\n"} {
					_, out, err := plan(c.event, c.ref, all, map[string]string{"voice": content})
					Expect(err).To(HaveOccurred(), "%s file on %s %s", name, c.event, c.ref)
					Expect(out).To(ContainSubstring("cannot read the platform matrix of add-on voice"))
				}
			}
		})
	})
})

func contains(list []string, s string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}
