package config_test

import (
	"encoding/json"
	"os"
	"path/filepath"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/mudler/LocalAI/core/config"
)

func strPtr(s string) *string { return &s }

var _ = Describe("RuntimeSettings persistence helpers", func() {
	var (
		dir string
		cfg *config.ApplicationConfig
	)

	BeforeEach(func() {
		dir = GinkgoT().TempDir()
		cfg = &config.ApplicationConfig{DynamicConfigsDir: dir}
	})

	// ReadPersistedSettings + WritePersistedSettings is the round-trip the
	// /api/branding/asset/:kind upload handler relies on: the upload writes
	// the basename to runtime_settings.json via these helpers, and the next
	// reader (loadRuntimeSettingsFromFile, the file watcher, or the next
	// upload) must observe that basename. A regression here would break
	// asset persistence.
	Describe("BrandingFiles round trip", func() {
		It("preserves instance_name, tagline, and basenames across read/write", func() {
			tagline := "Private inference"
			logo := "logo.png"
			settings := config.RuntimeSettings{
				InstanceName:    strPtr("Acme AI"),
				InstanceTagline: &tagline,
				LogoFile:        &logo,
			}
			Expect(cfg.WritePersistedSettings(settings)).To(Succeed())

			got, err := cfg.ReadPersistedSettings()
			Expect(err).ToNot(HaveOccurred())

			Expect(got.InstanceName).ToNot(BeNil())
			Expect(*got.InstanceName).To(Equal("Acme AI"))
			Expect(got.LogoFile).ToNot(BeNil())
			Expect(*got.LogoFile).To(Equal("logo.png"))
		})
	})

	// PreserveOnSaveDoesNotClobberAssets reproduces the user-reported
	// regression: an admin uploads a logo, then clicks Save on the
	// Settings page. The Save body still has the stale pre-upload
	// logo_file (empty string) because the React state was loaded
	// before the upload. UpdateSettingsEndpoint must protect the
	// on-disk basename — branding asset filenames are owned by the
	// /api/branding/asset/:kind endpoints, not by /api/settings.
	//
	// This spec exercises what UpdateSettingsEndpoint does: read the
	// existing persisted settings, override the asset filename fields
	// from disk, then write the merged settings. The fix lives in
	// core/http/endpoints/localai/settings.go; this spec pins the
	// contract that ReadPersistedSettings exposes the basenames so the
	// handler can preserve them.
	Describe("Save preservation prevents asset clobber", func() {
		It("keeps the on-disk logo basename when /api/settings posts an empty string", func() {
			existing := "logo.png"
			Expect(cfg.WritePersistedSettings(config.RuntimeSettings{LogoFile: &existing})).To(Succeed())

			// Simulate the body the React Settings page POSTs on Save:
			// stale empty-string logo_file, plus an unrelated user change
			// (instance_name).
			emptyLogo := ""
			newName := "Acme AI"
			body := config.RuntimeSettings{
				InstanceName: &newName,
				LogoFile:     &emptyLogo,
			}

			// Apply the same preservation step UpdateSettingsEndpoint performs.
			persisted, err := cfg.ReadPersistedSettings()
			Expect(err).ToNot(HaveOccurred())
			body.LogoFile = persisted.LogoFile
			body.LogoHorizontalFile = persisted.LogoHorizontalFile
			body.FaviconFile = persisted.FaviconFile

			Expect(cfg.WritePersistedSettings(body)).To(Succeed())

			// On-disk runtime_settings.json must still have the uploaded
			// basename, AND the unrelated change must have landed.
			raw, err := os.ReadFile(filepath.Join(dir, "runtime_settings.json"))
			Expect(err).ToNot(HaveOccurred())
			var ondisk config.RuntimeSettings
			Expect(json.Unmarshal(raw, &ondisk)).To(Succeed())

			Expect(ondisk.LogoFile).ToNot(BeNil(), "logo_file pointer was dropped")
			Expect(*ondisk.LogoFile).To(Equal("logo.png"), "logo_file was clobbered by Save")
			Expect(ondisk.InstanceName).ToNot(BeNil())
			Expect(*ondisk.InstanceName).To(Equal("Acme AI"))
		})
	})
})
