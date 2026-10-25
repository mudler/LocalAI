package gallery_test

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/core/gallery"
	"github.com/mudler/LocalAI/pkg/system"
)

// The models directory is often network storage (SMB, NFS), where every
// filesystem call is a round trip. The cached listing is read by the gallery
// page and by one VRAM estimate per row, so whatever it costs is paid dozens
// of times per page view.
var _ = Describe("Gallery cache installed status", func() {
	const index = `
- name: plain
  backend: llama-cpp
- name: linked
  backend: llama-cpp
- name: dangling
  backend: llama-cpp
- name: later
  backend: llama-cpp
- name: absent
  backend: llama-cpp
`

	var (
		modelsDir string
		state     *system.SystemState
		galleries []config.Gallery
		hits      atomic.Int32
		delay     time.Duration
	)

	BeforeEach(func() {
		var err error
		modelsDir, err = os.MkdirTemp("", "gallery-installed")
		Expect(err).ToNot(HaveOccurred())
		DeferCleanup(func() { _ = os.RemoveAll(modelsDir) })
		state, err = system.GetSystemState(system.WithModelPath(modelsDir))
		Expect(err).ToNot(HaveOccurred())

		hits.Store(0)
		delay = 0
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			hits.Add(1)
			time.Sleep(delay)
			_, _ = w.Write([]byte(index))
		}))
		DeferCleanup(server.Close)
		galleries = []config.Gallery{{Name: "test", URL: server.URL + "/index.yaml"}}

		gallery.ResetGalleryModelCache()
		DeferCleanup(gallery.ResetGalleryModelCache)
	})

	installed := func(models gallery.GalleryElements[*gallery.GalleryModel]) map[string]bool {
		out := map[string]bool{}
		for _, m := range models {
			out[m.Name] = m.Installed
		}
		return out
	}

	It("reports what os.Stat would, for files, symlinks and dangling symlinks", func() {
		Expect(os.WriteFile(filepath.Join(modelsDir, "plain.yaml"), []byte("name: plain\n"), 0o644)).To(Succeed())
		target := filepath.Join(modelsDir, "target.txt")
		Expect(os.WriteFile(target, []byte("name: linked\n"), 0o644)).To(Succeed())
		Expect(os.Symlink(target, filepath.Join(modelsDir, "linked.yaml"))).To(Succeed())
		Expect(os.Symlink(filepath.Join(modelsDir, "missing"), filepath.Join(modelsDir, "dangling.yaml"))).To(Succeed())

		// Both the blocking first load and the cached path set the flag, and
		// they must agree.
		for range 2 {
			models, err := gallery.AvailableGalleryModelsCached(galleries, state)
			Expect(err).ToNot(HaveOccurred())
			Expect(installed(models)).To(Equal(map[string]bool{
				"plain":    true,
				"linked":   true,
				"dangling": false,
				"later":    false,
				"absent":   false,
			}))
		}
	})

	It("picks up a config written after the gallery was cached", func() {
		_, err := gallery.AvailableGalleryModelsCached(galleries, state)
		Expect(err).ToNot(HaveOccurred())

		Expect(os.WriteFile(filepath.Join(modelsDir, "later.yaml"), []byte("name: later\n"), 0o644)).To(Succeed())

		models, err := gallery.AvailableGalleryModelsCached(galleries, state)
		Expect(err).ToNot(HaveOccurred())
		Expect(installed(models)).To(HaveKeyWithValue("later", true))
	})

	It("reports nothing installed when the models directory is gone", func() {
		_, err := gallery.AvailableGalleryModelsCached(galleries, state)
		Expect(err).ToNot(HaveOccurred())
		Expect(os.RemoveAll(modelsDir)).To(Succeed())

		models, err := gallery.AvailableGalleryModelsCached(galleries, state)
		Expect(err).ToNot(HaveOccurred())
		Expect(installed(models)).To(HaveEach(BeFalse()))
	})

	It("shares one upstream load between concurrent callers on a cold cache", func() {
		// Slow enough that every caller arrives while the first load is still
		// in flight, which is what a page view does to a freshly started
		// server: the listing and every row's estimate at once.
		delay = 300 * time.Millisecond

		var wg sync.WaitGroup
		for range 8 {
			wg.Go(func() {
				defer GinkgoRecover()
				models, err := gallery.AvailableGalleryModelsCached(galleries, state)
				Expect(err).ToNot(HaveOccurred())
				Expect(models).To(HaveLen(5))
			})
		}
		wg.Wait()

		Expect(hits.Load()).To(Equal(int32(1)))
	})
})
