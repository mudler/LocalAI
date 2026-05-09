package gallery_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"time"

	"github.com/mudler/LocalAI/core/config"
	. "github.com/mudler/LocalAI/core/gallery"
	"github.com/mudler/LocalAI/pkg/model"
	"github.com/mudler/LocalAI/pkg/system"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"gopkg.in/yaml.v3"
)

var _ = Describe("Upgrade Detection and Execution", func() {
	var (
		tempDir      string
		backendsPath string
		galleryPath  string
		systemState  *system.SystemState
		galleries    []config.Gallery
	)

	// installBackendWithVersion creates a fake installed backend directory with
	// the given name, version, and optional run.sh content.
	installBackendWithVersion := func(name, version string, runContent ...string) {
		dir := filepath.Join(backendsPath, name)
		Expect(os.MkdirAll(dir, 0750)).To(Succeed())

		content := "#!/bin/sh\necho ok"
		if len(runContent) > 0 {
			content = runContent[0]
		}
		Expect(os.WriteFile(filepath.Join(dir, "run.sh"), []byte(content), 0755)).To(Succeed())

		metadata := BackendMetadata{
			Name:        name,
			Version:     version,
			InstalledAt: time.Now().Format(time.RFC3339),
		}
		data, err := json.MarshalIndent(metadata, "", "  ")
		Expect(err).NotTo(HaveOccurred())
		Expect(os.WriteFile(filepath.Join(dir, "metadata.json"), data, 0644)).To(Succeed())
	}

	// writeGalleryYAML writes a gallery YAML file with the given backends.
	writeGalleryYAML := func(backends []GalleryBackend) {
		data, err := yaml.Marshal(backends)
		Expect(err).NotTo(HaveOccurred())
		Expect(os.WriteFile(galleryPath, data, 0644)).To(Succeed())
	}

	BeforeEach(func() {
		var err error
		tempDir, err = os.MkdirTemp("", "upgrade-test-*")
		Expect(err).NotTo(HaveOccurred())

		backendsPath = tempDir

		galleryPath = filepath.Join(tempDir, "gallery.yaml")

		// Write a default empty gallery
		writeGalleryYAML([]GalleryBackend{})

		galleries = []config.Gallery{
			{
				Name: "test-gallery",
				URL:  "file://" + galleryPath,
			},
		}

		systemState, err = system.GetSystemState(
			system.WithBackendPath(backendsPath),
		)
		Expect(err).NotTo(HaveOccurred())
	})

	AfterEach(func() {
		os.RemoveAll(tempDir)
	})

	Describe("CheckBackendUpgrades", func() {
		It("should detect upgrade when gallery version differs from installed version", func() {
			// Install a backend at v1.0.0
			installBackendWithVersion("my-backend", "1.0.0")

			// Gallery advertises v2.0.0
			writeGalleryYAML([]GalleryBackend{
				{
					Metadata: Metadata{
						Name: "my-backend",
					},
					URI:     filepath.Join(tempDir, "some-source"),
					Version: "2.0.0",
				},
			})

			upgrades, err := CheckBackendUpgrades(context.Background(), galleries, systemState)
			Expect(err).NotTo(HaveOccurred())
			Expect(upgrades).To(HaveKey("my-backend"))
			Expect(upgrades["my-backend"].InstalledVersion).To(Equal("1.0.0"))
			Expect(upgrades["my-backend"].AvailableVersion).To(Equal("2.0.0"))
		})

		It("should NOT flag upgrade when versions match", func() {
			installBackendWithVersion("my-backend", "2.0.0")

			writeGalleryYAML([]GalleryBackend{
				{
					Metadata: Metadata{
						Name: "my-backend",
					},
					URI:     filepath.Join(tempDir, "some-source"),
					Version: "2.0.0",
				},
			})

			upgrades, err := CheckBackendUpgrades(context.Background(), galleries, systemState)
			Expect(err).NotTo(HaveOccurred())
			Expect(upgrades).To(BeEmpty())
		})

		It("should skip backends without version info and without OCI digest", func() {
			// Install without version
			installBackendWithVersion("my-backend", "")

			// Gallery also without version
			writeGalleryYAML([]GalleryBackend{
				{
					Metadata: Metadata{
						Name: "my-backend",
					},
					URI: filepath.Join(tempDir, "some-source"),
				},
			})

			upgrades, err := CheckBackendUpgrades(context.Background(), galleries, systemState)
			Expect(err).NotTo(HaveOccurred())
			Expect(upgrades).To(BeEmpty())
		})
	})

	// CheckUpgradesAgainst is the entry point used by DistributedBackendManager.
	// It takes installed backends directly — typically aggregated from workers —
	// instead of reading the frontend filesystem. These tests exercise drift
	// detection, which is the feature the distributed path relies on.
	Describe("CheckUpgradesAgainst (distributed)", func() {
		It("flags upgrade when cluster nodes disagree on version, even if gallery matches majority", func() {
			writeGalleryYAML([]GalleryBackend{
				{
					Metadata: Metadata{Name: "my-backend"},
					URI:      filepath.Join(tempDir, "some-source"),
					Version:  "2.0.0",
				},
			})

			installed := SystemBackends{
				"my-backend": SystemBackend{
					Name:     "my-backend",
					Metadata: &BackendMetadata{Name: "my-backend", Version: "2.0.0"},
					Nodes: []NodeBackendRef{
						{NodeID: "a", NodeName: "worker-1", Version: "2.0.0"},
						{NodeID: "b", NodeName: "worker-2", Version: "2.0.0"},
						{NodeID: "c", NodeName: "worker-3", Version: "1.0.0"}, // drift
					},
				},
			}

			upgrades, err := CheckUpgradesAgainst(context.Background(), galleries, systemState, installed)
			Expect(err).NotTo(HaveOccurred())
			Expect(upgrades).To(HaveKey("my-backend"))
			info := upgrades["my-backend"]
			Expect(info.AvailableVersion).To(Equal("2.0.0"))
			Expect(info.NodeDrift).To(HaveLen(1))
			Expect(info.NodeDrift[0].NodeName).To(Equal("worker-3"))
			Expect(info.NodeDrift[0].Version).To(Equal("1.0.0"))
		})

		It("does not flag upgrade when all nodes agree and match gallery", func() {
			writeGalleryYAML([]GalleryBackend{
				{
					Metadata: Metadata{Name: "my-backend"},
					URI:      filepath.Join(tempDir, "some-source"),
					Version:  "2.0.0",
				},
			})

			installed := SystemBackends{
				"my-backend": SystemBackend{
					Name:     "my-backend",
					Metadata: &BackendMetadata{Name: "my-backend", Version: "2.0.0"},
					Nodes: []NodeBackendRef{
						{NodeID: "a", NodeName: "worker-1", Version: "2.0.0"},
						{NodeID: "b", NodeName: "worker-2", Version: "2.0.0"},
					},
				},
			}

			upgrades, err := CheckUpgradesAgainst(context.Background(), galleries, systemState, installed)
			Expect(err).NotTo(HaveOccurred())
			Expect(upgrades).To(BeEmpty())
		})

		It("surfaces empty-installed-version path the old distributed code silently missed", func() {
			// Simulates the real-world bug: worker has a backend, its version
			// is empty (pre-tracking or OCI-pinned-to-latest), gallery has a
			// version. Pre-fix CheckUpgrades returned nothing; now it surfaces.
			writeGalleryYAML([]GalleryBackend{
				{
					Metadata: Metadata{Name: "my-backend"},
					URI:      filepath.Join(tempDir, "some-source"),
					Version:  "2.0.0",
				},
			})

			installed := SystemBackends{
				"my-backend": SystemBackend{
					Name:     "my-backend",
					Metadata: &BackendMetadata{Name: "my-backend"},
					Nodes: []NodeBackendRef{
						{NodeID: "a", NodeName: "worker-1"},
					},
				},
			}

			upgrades, err := CheckUpgradesAgainst(context.Background(), galleries, systemState, installed)
			Expect(err).NotTo(HaveOccurred())
			Expect(upgrades).To(HaveKey("my-backend"))
			Expect(upgrades["my-backend"].InstalledVersion).To(BeEmpty())
			Expect(upgrades["my-backend"].AvailableVersion).To(Equal("2.0.0"))
		})

		// Dev-aware suppression: when `<X>-development` is installed it
		// stands in for the stable `<X>` via alias resolution. Auto-upgrade
		// must never reintroduce the stable variant alongside the dev one,
		// because the install would land on disk and (depending on
		// preference tokens) either shadow the dev pick or sit unused next
		// to it. These tests fix CheckUpgradesAgainst to honor that.
		// Names are kept generic ("my-backend") so the capability filter
		// in AvailableBackends doesn't drop them on a CPU-only test host.
		It("suppresses non-dev candidate when its -development counterpart is installed", func() {
			writeGalleryYAML([]GalleryBackend{
				{
					Metadata: Metadata{Name: "my-backend"},
					URI:      filepath.Join(tempDir, "stable"),
					Version:  "2.0.0",
				},
				{
					Metadata: Metadata{Name: "my-backend-development"},
					URI:      filepath.Join(tempDir, "dev"),
					Version:  "2.0.0",
				},
			})

			installed := SystemBackends{
				"my-backend-development": SystemBackend{
					Name: "my-backend-development",
					Metadata: &BackendMetadata{
						Name:    "my-backend-development",
						Version: "1.0.0",
					},
				},
			}

			upgrades, err := CheckUpgradesAgainst(context.Background(), galleries, systemState, installed)
			Expect(err).NotTo(HaveOccurred())
			Expect(upgrades).To(HaveKey("my-backend-development"))
			Expect(upgrades).NotTo(HaveKey("my-backend"))
		})

		It("dev variant wins even when non-dev is also present (vestigial state)", func() {
			// Either via legacy state, manual install, or a worker still
			// emitting synthetic aliases, the non-dev row may be present
			// alongside the dev one. Auto-upgrade must still keep its
			// hands off the non-dev — installing the stable variant on
			// top of the user's explicit dev pick is exactly what the
			// alias drop-in promise forbids. Users who genuinely want
			// the non-dev upgraded can trigger it manually via
			// /api/backends/upgrade/<name>.
			writeGalleryYAML([]GalleryBackend{
				{
					Metadata: Metadata{Name: "my-backend"},
					URI:      filepath.Join(tempDir, "stable"),
					Version:  "2.0.0",
				},
				{
					Metadata: Metadata{Name: "my-backend-development"},
					URI:      filepath.Join(tempDir, "dev"),
					Version:  "2.0.0",
				},
			})

			installed := SystemBackends{
				"my-backend": SystemBackend{
					Name: "my-backend",
					Metadata: &BackendMetadata{
						Name:    "my-backend",
						Version: "1.0.0",
					},
				},
				"my-backend-development": SystemBackend{
					Name: "my-backend-development",
					Metadata: &BackendMetadata{
						Name:    "my-backend-development",
						Version: "1.0.0",
					},
				},
			}

			upgrades, err := CheckUpgradesAgainst(context.Background(), galleries, systemState, installed)
			Expect(err).NotTo(HaveOccurred())
			Expect(upgrades).To(HaveKey("my-backend-development"))
			Expect(upgrades).NotTo(HaveKey("my-backend"))
		})

		It("ignores synthetic alias rows whose key differs from Metadata.Name", func() {
			// ListSystemBackends emits an extra row keyed by the alias name
			// that re-uses the chosen concrete's metadata pointer. Pre-fix
			// this row caused a duplicate gallery lookup in single-node
			// (harmless by accident) and a phantom upgrade in distributed
			// mode (real bug — the wire-reconstructed row carries
			// Metadata.Name = alias and resolves against an unrelated entry).
			writeGalleryYAML([]GalleryBackend{
				{
					Metadata: Metadata{Name: "my-alias"},
					URI:      filepath.Join(tempDir, "stable-meta"),
					Version:  "2.0.0",
				},
				{
					Metadata: Metadata{Name: "my-backend-development"},
					URI:      filepath.Join(tempDir, "dev"),
					Version:  "2.0.0",
				},
			})

			devMeta := &BackendMetadata{
				Name:    "my-backend-development",
				Version: "1.0.0",
				Alias:   "my-alias",
			}
			installed := SystemBackends{
				"my-backend-development": SystemBackend{
					Name:     "my-backend-development",
					Metadata: devMeta,
				},
				// Synthetic alias row: key != Metadata.Name.
				"my-alias": SystemBackend{
					Name:     "my-alias",
					Metadata: devMeta,
				},
			}

			upgrades, err := CheckUpgradesAgainst(context.Background(), galleries, systemState, installed)
			Expect(err).NotTo(HaveOccurred())
			Expect(upgrades).To(HaveKey("my-backend-development"))
			Expect(upgrades).NotTo(HaveKey("my-alias"))
		})
	})

	Describe("UpgradeBackend", func() {
		It("should replace backend directory and update metadata", func() {
			// Install v1
			installBackendWithVersion("my-backend", "1.0.0", "#!/bin/sh\necho v1")

			// Create a source directory with v2 content
			srcDir := filepath.Join(tempDir, "v2-source")
			Expect(os.MkdirAll(srcDir, 0750)).To(Succeed())
			Expect(os.WriteFile(filepath.Join(srcDir, "run.sh"), []byte("#!/bin/sh\necho v2"), 0755)).To(Succeed())

			// Gallery points to the v2 source dir
			writeGalleryYAML([]GalleryBackend{
				{
					Metadata: Metadata{
						Name: "my-backend",
					},
					URI:     srcDir,
					Version: "2.0.0",
				},
			})

			ml := model.NewModelLoader(systemState)
			err := UpgradeBackend(context.Background(), systemState, ml, galleries, "my-backend", nil)
			Expect(err).NotTo(HaveOccurred())

			// Verify run.sh was updated
			content, err := os.ReadFile(filepath.Join(backendsPath, "my-backend", "run.sh"))
			Expect(err).NotTo(HaveOccurred())
			Expect(string(content)).To(Equal("#!/bin/sh\necho v2"))

			// Verify metadata was updated
			metaData, err := os.ReadFile(filepath.Join(backendsPath, "my-backend", "metadata.json"))
			Expect(err).NotTo(HaveOccurred())
			var meta BackendMetadata
			Expect(json.Unmarshal(metaData, &meta)).To(Succeed())
			Expect(meta.Version).To(Equal("2.0.0"))
			Expect(meta.Name).To(Equal("my-backend"))
		})

		It("should restore backup on failure", func() {
			// Install v1
			installBackendWithVersion("my-backend", "1.0.0", "#!/bin/sh\necho v1")

			// Gallery points to a nonexistent path (no run.sh will be found)
			nonExistentDir := filepath.Join(tempDir, "does-not-exist")
			writeGalleryYAML([]GalleryBackend{
				{
					Metadata: Metadata{
						Name: "my-backend",
					},
					URI:     nonExistentDir,
					Version: "2.0.0",
				},
			})

			ml := model.NewModelLoader(systemState)
			err := UpgradeBackend(context.Background(), systemState, ml, galleries, "my-backend", nil)
			Expect(err).To(HaveOccurred())

			// Verify v1 is still intact
			content, err := os.ReadFile(filepath.Join(backendsPath, "my-backend", "run.sh"))
			Expect(err).NotTo(HaveOccurred())
			Expect(string(content)).To(Equal("#!/bin/sh\necho v1"))

			// Verify metadata still says v1
			metaData, err := os.ReadFile(filepath.Join(backendsPath, "my-backend", "metadata.json"))
			Expect(err).NotTo(HaveOccurred())
			var meta BackendMetadata
			Expect(json.Unmarshal(metaData, &meta)).To(Succeed())
			Expect(meta.Version).To(Equal("1.0.0"))
		})
	})
})
