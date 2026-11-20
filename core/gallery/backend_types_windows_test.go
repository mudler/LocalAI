package gallery_test

import (
	"os"

	"github.com/mudler/LocalAI/core/gallery"
	"github.com/mudler/LocalAI/pkg/system"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("backend compatibility on a Windows host", func() {
	BeforeEach(func() {
		previous := os.Getenv("LOCALAI_FORCE_META_BACKEND_CAPABILITY")
		Expect(os.Setenv("LOCALAI_FORCE_META_BACKEND_CAPABILITY", "windows")).To(Succeed())
		DeferCleanup(func() {
			if previous == "" {
				Expect(os.Unsetenv("LOCALAI_FORCE_META_BACKEND_CAPABILITY")).To(Succeed())
			} else {
				Expect(os.Setenv("LOCALAI_FORCE_META_BACKEND_CAPABILITY", previous)).To(Succeed())
			}
		})
	})

	It("hides a meta backend that has no Windows target", func() {
		meta := &gallery.GalleryBackend{Metadata: gallery.Metadata{Name: "whisper"}, CapabilitiesMap: map[string]string{
			"default": "cpu-whisper", "nvidia": "cuda12-whisper",
		}}
		Expect(meta.IsCompatibleWith(&system.SystemState{})).To(BeFalse())
	})

	It("shows a meta backend that has a Windows target", func() {
		meta := &gallery.GalleryBackend{Metadata: gallery.Metadata{Name: "llama-cpp"}, CapabilitiesMap: map[string]string{
			"default": "cpu-llama-cpp", "windows": "windows-llama-cpp",
		}}
		Expect(meta.IsCompatibleWith(&system.SystemState{})).To(BeTrue())
	})

	It("does not resolve a meta backend without a Windows target to a Linux build", func() {
		meta := &gallery.GalleryBackend{Metadata: gallery.Metadata{Name: "whisper"}, CapabilitiesMap: map[string]string{
			"default": "cpu-whisper",
		}}
		all := gallery.GalleryElements[*gallery.GalleryBackend]{
			&gallery.GalleryBackend{Metadata: gallery.Metadata{Name: "cpu-whisper"}, URI: "quay.io/x:latest-cpu-whisper"},
		}
		Expect(meta.FindBestBackendFromMeta(&system.SystemState{}, all)).To(BeNil())
	})
})
