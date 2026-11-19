package gallery_test

import (
	"context"
	"encoding/base64"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"gopkg.in/yaml.v3"

	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/core/gallery"
	"github.com/mudler/LocalAI/pkg/system"
)

// The specs of this file read and write the version 2 gallery file. They stay
// off the network: the gallery, the base config and the weights are local
// files.
var _ = Describe("Gallery file version 2", func() {
	var (
		dir         string
		srcDir      string
		systemState *system.SystemState
		galleries   []config.Gallery
	)
	revision := 0

	writeGallery := func(entries ...gallery.GalleryModel) {
		out, err := yaml.Marshal(entries)
		Expect(err).ToNot(HaveOccurred())
		revision++
		name := fmt.Sprintf("sidecar-%d", revision)
		path := filepath.Join(srcDir, name+".yaml")
		Expect(os.WriteFile(path, out, 0600)).To(Succeed())
		galleries = []config.Gallery{{Name: name, URL: "file://" + path}}
	}

	weights := func(name, content string) gallery.File {
		path := filepath.Join(srcDir, name)
		Expect(os.WriteFile(path, []byte(content), 0600)).To(Succeed())
		return gallery.File{Filename: name, URI: "file://" + path, SHA256: gallery.SHA256Hex([]byte(content))}
	}

	install := func(id string, req gallery.GalleryModel) error {
		return gallery.InstallModelFromGallery(context.TODO(), galleries, nil, systemState, nil, id, req,
			func(string, string, string, float64) {}, false, false, false)
	}

	BeforeEach(func() {
		var err error
		dir, err = os.MkdirTemp("", "sidecar-v2")
		Expect(err).ToNot(HaveOccurred())
		DeferCleanup(func() { Expect(os.RemoveAll(dir)).To(Succeed()) })
		srcDir = filepath.Join(dir, "src")
		Expect(os.MkdirAll(srcDir, 0750)).To(Succeed())
		systemState, err = system.GetSystemState(system.WithModelPath(dir))
		Expect(err).ToNot(HaveOccurred())
	})

	It("records the origin, the config hash and the managed files of a gallery install", func() {
		w := weights("tiny.gguf", "weights v1")
		writeGallery(gallery.GalleryModel{
			Metadata:  gallery.Metadata{Name: "tiny", Description: "a model", AdditionalFiles: []gallery.File{w}},
			Overrides: map[string]any{"backend": "llama-cpp", "parameters": map[string]any{"model": "tiny.gguf"}},
		})
		Expect(install(galleries[0].Name+"@tiny", gallery.GalleryModel{Overrides: map[string]any{"context_size": 4096}})).To(Succeed())

		sc, err := gallery.ReadSidecar(dir, "tiny")
		Expect(err).ToNot(HaveOccurred())
		Expect(sc.IsV2()).To(BeTrue())
		Expect(sc.Origin).ToNot(BeNil())
		Expect(sc.Origin.Gallery).To(Equal(galleries[0].Name))
		Expect(sc.Origin.EntryName).To(Equal("tiny"))
		Expect(sc.Origin.OverridesSHA256).ToNot(BeEmpty())
		Expect(sc.Origin.RequestOverrides).To(HaveKeyWithValue("context_size", 4096))
		Expect(sc.Origin.InstalledAt).ToNot(BeZero())
		Expect(sc.Upgrade).To(BeNil(), "the install journal is gone once the install is written")

		configYAML, err := os.ReadFile(filepath.Join(dir, "tiny.yaml"))
		Expect(err).ToNot(HaveOccurred())
		Expect(sc.ConfigSHA256).To(Equal(gallery.SHA256Hex(configYAML)))
		Expect(sc.Origin.ConfigYAML).To(Equal(string(configYAML)))
		Expect(sc.Managed).To(ConsistOf(gallery.ManagedFile{Path: "tiny.gguf", SHA256: w.SHA256, URI: w.URI, Role: gallery.RoleWeights}))
	})

	It("reads a gallery file of an older release as version 0 and leaves it alone", func() {
		old := []byte("name: old\nentry_name: old\nfiles:\n- filename: old.gguf\n  sha256: abc\n  uri: https://example.com/old.gguf\n")
		Expect(os.WriteFile(gallery.SidecarPath(dir, "old"), old, 0600)).To(Succeed())

		sc, err := gallery.ReadSidecar(dir, "old")
		Expect(err).ToNot(HaveOccurred())
		Expect(sc.IsV2()).To(BeFalse())
		Expect(sc.Origin).To(BeNil())

		recovered, err := gallery.RecoverInstallJournals(dir, nil)
		Expect(err).ToNot(HaveOccurred())
		Expect(recovered).To(BeEmpty())
		after, err := os.ReadFile(gallery.SidecarPath(dir, "old"))
		Expect(err).ToNot(HaveOccurred())
		Expect(after).To(Equal(old))
	})

	It("reports a missing gallery file", func() {
		_, err := gallery.ReadSidecar(dir, "none")
		Expect(err).To(MatchError(gallery.ErrNoSidecar))
	})

	It("round-trips every version 2 field", func() {
		in := &gallery.ModelConfig{
			// The fields of older releases have no omitempty, so they come back
			// as empty lists.
			URLs: []string{}, Files: []gallery.File{}, PromptTemplates: []gallery.PromptTemplate{},
			Name: "rt", Version: gallery.SidecarVersion, Hold: true, ConfigSHA256: "c",
			Origin:  &gallery.Origin{Gallery: "g", EntryName: "e", ConfigURL: "u", EntryDigest: "sha256:d", BaseConfigSHA256: "b", OverridesSHA256: "o", ConfigYAML: "name: rt\n", InstalledAt: time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)},
			Managed: []gallery.ManagedFile{{Path: "rt.gguf", SHA256: "s", URI: "u", Role: gallery.RoleWeights}},
			Upgrade: &gallery.Journal{Kind: gallery.JournalUpgrade, Phase: gallery.PhaseConfirmed, RetainedFiles: []string{"rt.gguf.prev-12345678"}, Attempts: 1},
		}
		Expect(gallery.WriteSidecar(dir, "rt", in)).To(Succeed())
		out, err := gallery.ReadSidecar(dir, "rt")
		Expect(err).ToNot(HaveOccurred())
		Expect(out).To(Equal(in))
	})

	It("keeps the hold flag over a reinstall", func() {
		writeGallery(gallery.GalleryModel{Metadata: gallery.Metadata{Name: "held"}, Overrides: map[string]any{"backend": "llama-cpp"}})
		id := galleries[0].Name + "@held"
		Expect(install(id, gallery.GalleryModel{})).To(Succeed())
		sc, err := gallery.ReadSidecar(dir, "held")
		Expect(err).ToNot(HaveOccurred())
		sc.Hold = true
		Expect(gallery.WriteSidecar(dir, "held", sc)).To(Succeed())

		Expect(install(id, gallery.GalleryModel{})).To(Succeed())
		sc, err = gallery.ReadSidecar(dir, "held")
		Expect(err).ToNot(HaveOccurred())
		Expect(sc.Hold).To(BeTrue())
	})

	It("leaves the old gallery file intact when the atomic write cannot complete", func() {
		if runtime.GOOS == "windows" {
			Skip("directory permission bits do not stop file creation on Windows")
		}
		if os.Geteuid() == 0 {
			Skip("root ignores directory permissions")
		}
		Expect(gallery.WriteSidecar(dir, "ro", &gallery.ModelConfig{Name: "ro", Version: 2, ConfigSHA256: "old"})).To(Succeed())
		Expect(os.Chmod(dir, 0500)).To(Succeed())
		DeferCleanup(func() { Expect(os.Chmod(dir, 0750)).To(Succeed()) })

		Expect(gallery.WriteSidecar(dir, "ro", &gallery.ModelConfig{Name: "ro", Version: 2, ConfigSHA256: "new"})).ToNot(Succeed())
		sc, err := gallery.ReadSidecar(dir, "ro")
		Expect(err).ToNot(HaveOccurred())
		Expect(sc.ConfigSHA256).To(Equal("old"))
	})

	Describe("a crash between the two writes of an install", func() {
		// The state a crash leaves after commitInstall wrote the journaled
		// gallery file: the new config may or may not have reached the disk.
		journaled := func(name string, newConfig []byte, previous *gallery.Previous) {
			sc := &gallery.ModelConfig{
				Name: name, Version: gallery.SidecarVersion, ConfigSHA256: gallery.SHA256Hex(newConfig),
				Origin: &gallery.Origin{EntryName: name},
				Upgrade: &gallery.Journal{
					Kind: gallery.JournalInstall, Phase: gallery.PhaseCommitting, Instance: "a",
					ToConfigSHA256: gallery.SHA256Hex(newConfig), Previous: previous, UpdatedAt: time.Now(),
				},
			}
			Expect(gallery.WriteSidecar(dir, name, sc)).To(Succeed())
		}

		It("finishes the install when the new config was written", func() {
			newConfig := []byte("name: done\nbackend: llama-cpp\n")
			journaled("done", newConfig, &gallery.Previous{})
			Expect(os.WriteFile(filepath.Join(dir, "done.yaml"), newConfig, 0600)).To(Succeed())

			recovered, err := gallery.RecoverInstallJournals(dir, nil)
			Expect(err).ToNot(HaveOccurred())
			Expect(recovered).To(ConsistOf("done"))
			sc, err := gallery.ReadSidecar(dir, "done")
			Expect(err).ToNot(HaveOccurred())
			Expect(sc.Upgrade).To(BeNil())
			Expect(sc.Origin.EntryName).To(Equal("done"))
		})

		It("puts the previous config and gallery file back on a reinstall", func() {
			oldConfig := []byte("name: re\nbackend: old\n")
			oldSidecar := []byte("name: re\nentry_name: re\n")
			Expect(os.WriteFile(filepath.Join(dir, "re.yaml"), oldConfig, 0600)).To(Succeed())
			journaled("re", []byte("name: re\nbackend: new\n"), &gallery.Previous{
				HadConfig: true, ConfigYAML: base64.StdEncoding.EncodeToString(oldConfig),
				HadSidecar: true, Sidecar: base64.StdEncoding.EncodeToString(oldSidecar),
			})

			recovered, err := gallery.RecoverInstallJournals(dir, nil)
			Expect(err).ToNot(HaveOccurred())
			Expect(recovered).To(ConsistOf("re"))
			Expect(os.ReadFile(filepath.Join(dir, "re.yaml"))).To(Equal(oldConfig))
			Expect(os.ReadFile(gallery.SidecarPath(dir, "re"))).To(Equal(oldSidecar))
		})

		It("removes the gallery file of a first install that never wrote its config", func() {
			journaled("fresh", []byte("name: fresh\n"), &gallery.Previous{})

			_, err := gallery.RecoverInstallJournals(dir, nil)
			Expect(err).ToNot(HaveOccurred())
			_, err = gallery.ReadSidecar(dir, "fresh")
			Expect(err).To(MatchError(gallery.ErrNoSidecar))
			Expect(filepath.Join(dir, "fresh.yaml")).ToNot(BeAnExistingFile())
		})

		It("leaves a journal alone when the policy says another frontend owns it", func() {
			journaled("theirs", []byte("name: theirs\n"), &gallery.Previous{})
			recovered, err := gallery.RecoverInstallJournals(dir, func(j *gallery.Journal) bool { return j.Instance == "b" })
			Expect(err).ToNot(HaveOccurred())
			Expect(recovered).To(BeEmpty())
			sc, err := gallery.ReadSidecar(dir, "theirs")
			Expect(err).ToNot(HaveOccurred())
			Expect(sc.Upgrade).ToNot(BeNil())
		})
	})

	It("tells a stale running journal from a fresh or finished one", func() {
		now := time.Now()
		running := &gallery.Journal{Phase: gallery.PhaseCommitting, UpdatedAt: now.Add(-time.Hour)}
		Expect(running.Stale(now, 15*time.Minute)).To(BeTrue())
		Expect(running.Stale(now, 2*time.Hour)).To(BeFalse())
		done := &gallery.Journal{Phase: gallery.PhaseConfirmed, UpdatedAt: now.Add(-time.Hour)}
		Expect(done.Stale(now, time.Minute)).To(BeFalse())
		Expect(done.Terminal()).To(BeTrue())
	})

	It("classifies managed files by name", func() {
		Expect(gallery.FileRole("m.gguf")).To(Equal(gallery.RoleWeights))
		Expect(gallery.FileRole("mmproj-m.gguf")).To(Equal(gallery.RoleMMProj))
		Expect(gallery.FileRole("chat.tmpl")).To(Equal(gallery.RoleTemplate))
		Expect(gallery.FileRole("tokenizer.json")).To(Equal(gallery.RoleArtifact))
	})
})
