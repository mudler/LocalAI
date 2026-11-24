package gallery

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/pkg/model"
	"github.com/mudler/LocalAI/pkg/system"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"gopkg.in/yaml.v3"
)

var _ = Describe("Typed failures of a backend upgrade", func() {
	var (
		dir         string
		systemState *system.SystemState
		galleries   []config.Gallery
		galleryYAML string
	)

	install := func(name string) {
		backendDir := filepath.Join(dir, name)
		Expect(os.MkdirAll(backendDir, 0750)).To(Succeed())
		Expect(os.WriteFile(filepath.Join(backendDir, "run.sh"), []byte("#!/bin/sh\necho v1"), 0755)).To(Succeed())
		data, err := json.Marshal(BackendMetadata{Name: name, Version: "1.0.0", InstalledAt: time.Now().Format(time.RFC3339)})
		Expect(err).NotTo(HaveOccurred())
		Expect(os.WriteFile(filepath.Join(backendDir, "metadata.json"), data, 0644)).To(Succeed())
	}
	publish := func(b GalleryBackend) {
		data, err := yaml.Marshal([]GalleryBackend{b})
		Expect(err).NotTo(HaveOccurred())
		Expect(os.WriteFile(galleryYAML, data, 0644)).To(Succeed())
	}
	upgrade := func(name string, strict bool) error {
		return UpgradeBackend(context.Background(), systemState, model.NewModelLoader(systemState), galleries, name, nil, strict)
	}
	installedRunFile := func(name string) string {
		content, err := os.ReadFile(filepath.Join(dir, name, "run.sh"))
		Expect(err).NotTo(HaveOccurred())
		return string(content)
	}

	BeforeEach(func() {
		var err error
		dir, err = os.MkdirTemp("", "upgrade-codes-*")
		Expect(err).NotTo(HaveOccurred())
		galleryYAML = filepath.Join(dir, "gallery.yaml")
		publish(GalleryBackend{Metadata: Metadata{Name: "other"}, URI: dir})
		galleries = []config.Gallery{{Name: "test-gallery", URL: "file://" + galleryYAML}}
		systemState, err = system.GetSystemState(system.WithBackendPath(dir))
		Expect(err).NotTo(HaveOccurred())
	})
	AfterEach(func() {
		renameBackend = os.Rename
		Expect(os.RemoveAll(dir)).To(Succeed())
	})

	It("tags an upgrade of a backend that is not installed", func() {
		err := upgrade("absent", false)
		Expect(UpgradeCodeOf(err)).To(Equal(UpgradeFailureNotInstalled))
		Expect(errors.Is(err, ErrUpgradeNotInstalled)).To(BeTrue())
		Expect(errors.Is(err, ErrBackendNotFound)).To(BeTrue(), "the cause stays visible")
	})

	It("tags a backend that the gallery no longer lists as having no update", func() {
		install("my-backend")
		err := upgrade("my-backend", false)
		Expect(UpgradeCodeOf(err)).To(Equal(UpgradeFailureNoUpdate))
	})

	It("tags a refused integrity policy as verify", func() {
		install("my-backend")
		publish(GalleryBackend{Metadata: Metadata{Name: "my-backend"}, URI: "oci://example.invalid/missing:never-fetched", Version: "2.0.0"})
		err := upgrade("my-backend", true)
		Expect(errors.Is(err, ErrUpgradeVerify)).To(BeTrue())
		Expect(err.Error()).To(ContainSubstring("strict integrity"))
	})

	It("tags a checksum mismatch as verify", func() {
		install("my-backend")
		payload := filepath.Join(dir, "payload.bin")
		Expect(os.WriteFile(payload, []byte("not the bytes the gallery promised"), 0644)).To(Succeed())
		wrong := fmt.Sprintf("%x", sha256.Sum256([]byte("other")))
		publish(GalleryBackend{Metadata: Metadata{Name: "my-backend"}, URI: "file://" + payload, SHA256: wrong, Version: "2.0.0"})
		err := upgrade("my-backend", false)
		Expect(UpgradeCodeOf(err)).To(Equal(UpgradeFailureVerify))
		Expect(installedRunFile("my-backend")).To(Equal("#!/bin/sh\necho v1"))
	})

	It("tags a new build without run.sh as verify", func() {
		install("my-backend")
		src := filepath.Join(dir, "src-no-run")
		Expect(os.MkdirAll(src, 0750)).To(Succeed())
		Expect(os.WriteFile(filepath.Join(src, "readme"), []byte("x"), 0644)).To(Succeed())
		publish(GalleryBackend{Metadata: Metadata{Name: "my-backend"}, URI: src, Version: "2.0.0"})
		err := upgrade("my-backend", false)
		Expect(UpgradeCodeOf(err)).To(Equal(UpgradeFailureVerify))
		Expect(installedRunFile("my-backend")).To(Equal("#!/bin/sh\necho v1"))
	})

	It("tags a source that cannot be fetched as download", func() {
		install("my-backend")
		publish(GalleryBackend{Metadata: Metadata{Name: "my-backend"}, URI: filepath.Join(dir, "does-not-exist"), Version: "2.0.0"})
		err := upgrade("my-backend", false)
		Expect(UpgradeCodeOf(err)).To(Equal(UpgradeFailureDownload))
		Expect(installedRunFile("my-backend")).To(Equal("#!/bin/sh\necho v1"))
	})

	It("tags a swap that cannot move the build into place as swap", func() {
		install("my-backend")
		src := filepath.Join(dir, "src-v2")
		Expect(os.MkdirAll(src, 0750)).To(Succeed())
		Expect(os.WriteFile(filepath.Join(src, "run.sh"), []byte("#!/bin/sh\necho v2"), 0755)).To(Succeed())
		publish(GalleryBackend{Metadata: Metadata{Name: "my-backend"}, URI: src, Version: "2.0.0"})
		renameBackend = func(string, string) error { return errors.New("disk refused the rename") }
		err := upgrade("my-backend", false)
		Expect(errors.Is(err, ErrUpgradeSwap)).To(BeTrue())
		Expect(installedRunFile("my-backend")).To(Equal("#!/bin/sh\necho v1"))
	})

	It("keeps the code of the inner step when it is tagged again", func() {
		inner := WithUpgradeCode(UpgradeFailureVerify, errors.New("bad"))
		Expect(UpgradeCodeOf(WithUpgradeCode(UpgradeFailureUnknown, inner))).To(Equal(UpgradeFailureVerify))
		Expect(UpgradeCodeOf(fmt.Errorf("wrapped: %w", inner))).To(Equal(UpgradeFailureVerify))
		Expect(WithUpgradeCode(UpgradeFailureUnknown, nil)).To(BeNil())
		Expect(UpgradeCodeOf(errors.New("plain"))).To(BeEmpty())
	})

	It("does not tag a cancelled upgrade as a download failure", func() {
		err := downloadUpgradeError(canceledContext(), context.Canceled)
		Expect(UpgradeCodeOf(err)).To(BeEmpty())
		Expect(errors.Is(err, context.Canceled)).To(BeTrue())
	})
})

func canceledContext() context.Context {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	return ctx
}
