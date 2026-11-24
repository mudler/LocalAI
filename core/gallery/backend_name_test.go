package gallery

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"time"

	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/pkg/model"
	"github.com/mudler/LocalAI/pkg/system"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"gopkg.in/yaml.v3"
)

var _ = Describe("The backend name used to build a path", func() {
	DescribeTable("ValidateBackendName",
		func(name string, ok bool) {
			err := ValidateBackendName(name)
			if ok {
				Expect(err).NotTo(HaveOccurred())
			} else {
				Expect(errors.Is(err, ErrInvalidBackendName)).To(BeTrue(), "%q", name)
			}
		},
		Entry("plain", "llama-cpp", true),
		Entry("with dots inside", "cuda12-llama-cpp.v2", true),
		Entry("empty", "", false),
		Entry("dot", ".", false),
		Entry("dot dot", "..", false),
		Entry("parent path", "../etc", false),
		Entry("dot dot inside", "a..b", false),
		Entry("slash", "a/b", false),
		Entry("backslash", `a\b`, false),
		Entry("leading dot", ".hidden", false),
		Entry("absolute", "/etc", false),
		Entry("nul", "a\x00b", false),
		// A name that ends like a directory of a swap would be read as one.
		Entry("backup suffix", "llama-cpp.backup", false),
		Entry("install backup suffix", "llama-cpp.install-backup", false),
		Entry("upgrade staging suffix", "llama-cpp.upgrade-tmp", false),
		Entry("install staging suffix", "llama-cpp.install-tmp", false),
		Entry("failed suffix", "llama-cpp.failed", false),
		Entry("kept previous build", "llama-cpp.backup.kept-20260101T000000", false),
	)

	Describe("in an upgrade", func() {
		var (
			dir, backends string
			systemState   *system.SystemState
			ml            *model.ModelLoader
		)

		writeBuild := func(path, version string, meta BackendMetadata) {
			Expect(os.MkdirAll(path, 0o750)).To(Succeed())
			Expect(os.WriteFile(filepath.Join(path, "run.sh"), []byte("#!/bin/sh\necho "+version+"\n"), 0o700)).To(Succeed())
			Expect(os.WriteFile(filepath.Join(path, "version"), []byte(version), 0o600)).To(Succeed())
			if meta.Name != "" {
				data, err := json.Marshal(meta)
				Expect(err).NotTo(HaveOccurred())
				Expect(os.WriteFile(filepath.Join(path, "metadata.json"), data, 0o600)).To(Succeed())
			}
		}
		readVersion := func(path string) string {
			b, err := os.ReadFile(filepath.Join(path, "version"))
			Expect(err).NotTo(HaveOccurred())
			return string(b)
		}
		gallery := func(entries ...GalleryBackend) []config.Gallery {
			data, err := yaml.Marshal(entries)
			Expect(err).NotTo(HaveOccurred())
			file := filepath.Join(backends, "gallery.yaml")
			Expect(os.WriteFile(file, data, 0o600)).To(Succeed())
			return []config.Gallery{{Name: "test", URL: "file://" + file}}
		}

		BeforeEach(func() {
			if runtime.GOOS == "windows" {
				Skip("the fixture backends are shell scripts")
			}
			dir = GinkgoT().TempDir()
			backends = filepath.Join(dir, "backends")
			Expect(os.MkdirAll(backends, 0o750)).To(Succeed())
			var err error
			systemState, err = system.GetSystemState(system.WithBackendPath(backends))
			Expect(err).NotTo(HaveOccurred())
			ml = model.NewModelLoader(systemState)
			passStartCheck()
		})

		DescribeTable("refuses a name that is not one path element before it touches the disk",
			func(name string) {
				outside := filepath.Join(dir, "outside")
				writeBuild(outside, "untouched", BackendMetadata{})
				err := UpgradeBackend(context.Background(), systemState, ml, nil, name, nil, false)
				Expect(errors.Is(err, ErrInvalidBackendName)).To(BeTrue(), "%v", err)
				Expect(UpgradeCodeOf(err)).To(Equal(UpgradeFailureUnknown))
				Expect(readVersion(outside)).To(Equal("untouched"))
				for _, suffix := range []string{".backup", ".upgrade-tmp", ".failed"} {
					Expect(outside + suffix).NotTo(BeAnExistingFile())
				}
			},
			Entry("a parent path", "../outside"),
			Entry("a nested path", "x/../../outside"),
			Entry("an absolute path", "/etc"),
			Entry("empty", ""),
		)

		It("upgrades the directory an alias resolves to, never a directory named like the alias", func() {
			writeBuild(filepath.Join(backends, "cpu-foo"), "v1", BackendMetadata{Name: "cpu-foo", Alias: "foo", Version: "1.0.0", InstalledAt: time.Now().Format(time.RFC3339)})
			// An unrelated backend whose directory has the name of the alias.
			writeBuild(filepath.Join(backends, "foo"), "unrelated", BackendMetadata{Name: "foo", Version: "9.0.0"})
			src := filepath.Join(dir, "v2-source")
			writeBuild(src, "v2", BackendMetadata{})
			other := filepath.Join(dir, "other-source")
			writeBuild(other, "other", BackendMetadata{})
			galleries := gallery(
				GalleryBackend{Metadata: Metadata{Name: "cpu-foo"}, URI: src, Version: "2.0.0"},
				GalleryBackend{Metadata: Metadata{Name: "foo"}, URI: other, Version: "10.0.0"},
			)
			installed, err := ListSystemBackends(systemState)
			Expect(err).NotTo(HaveOccurred())
			Expect(installed["foo"].RunFile).To(Equal(filepath.Join(backends, "cpu-foo", "run.sh")), "the alias wins over the directory")

			Expect(UpgradeBackend(context.Background(), systemState, ml, galleries, "foo", nil, false)).To(Succeed())
			Expect(readVersion(filepath.Join(backends, "foo"))).To(Equal("unrelated"))
			Expect(readVersion(filepath.Join(backends, "cpu-foo"))).To(Equal("v2"))
			meta, err := readBackendMetadata(filepath.Join(backends, "cpu-foo"))
			Expect(err).NotTo(HaveOccurred())
			Expect(meta.Name).To(Equal("cpu-foo"))
			Expect(meta.Alias).To(Equal("foo"))
		})
	})

	Describe("in a restore", func() {
		var root string

		BeforeEach(func() {
			root = GinkgoT().TempDir()
		})

		It("moves and removes a symlinked build as a link, never what it points to", func() {
			target := filepath.Join(root, "elsewhere")
			Expect(os.MkdirAll(target, 0o750)).To(Succeed())
			Expect(os.WriteFile(filepath.Join(target, "keep"), []byte("x"), 0o600)).To(Succeed())
			backendPath := filepath.Join(root, "b")
			Expect(os.Symlink(target, backendPath)).To(Succeed())
			backupPath := backendPath + ".backup"
			Expect(os.MkdirAll(backupPath, 0o750)).To(Succeed())

			Expect(restoreBackup(backendPath, backupPath)).To(Succeed())
			Expect(filepath.Join(target, "keep")).To(BeAnExistingFile(), "the target of the link is not followed")
			fi, err := os.Lstat(backendPath)
			Expect(err).NotTo(HaveOccurred())
			Expect(fi.Mode() & os.ModeSymlink).To(BeZero())
			Expect(backendPath + failedSuffix).NotTo(BeAnExistingFile())
		})

		DescribeTable("refuses a path that is not a backend directory and its swap directory",
			func(backendPath, backupPath func() string) {
				victim := filepath.Join(root, "victim"+failedSuffix)
				Expect(os.MkdirAll(victim, 0o750)).To(Succeed())
				err := restoreBackup(backendPath(), backupPath())
				Expect(errors.Is(err, ErrInvalidBackendName)).To(BeTrue(), "%v", err)
				Expect(victim).To(BeADirectory(), "nothing was removed")
			},
			Entry("a name with a parent element",
				func() string { return root + "/x/../victim" }, func() string { return root + "/x/../victim.backup" }),
			Entry("a hidden name",
				func() string { return filepath.Join(root, ".victim") }, func() string { return filepath.Join(root, ".victim.backup") }),
			Entry("a backup that is not next to the build",
				func() string { return filepath.Join(root, "victim") }, func() string { return filepath.Join(root, "other") }),
		)
	})
})
