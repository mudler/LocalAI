package gallery_test

import (
	"os"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/core/gallery"
	"github.com/mudler/LocalAI/pkg/system"
)

// The gallery generation counter is what every VRAM estimate cache keys on, so
// how often it moves decides whether those caches are worth having. Refreshing
// on every call kept them permanently cold: one page of the model gallery asks
// for a VRAM estimate per row, and each of those requests re-read the gallery,
// triggering a refresh that invalidated the estimate the previous row had just
// paid a network round trip for.
var _ = Describe("Gallery refresh throttling", func() {
	var (
		tmp          *system.SystemState
		galleries    []config.Gallery
		origInterval time.Duration
	)

	BeforeEach(func() {
		dir, err := os.MkdirTemp("", "gallery-throttle")
		Expect(err).ToNot(HaveOccurred())
		DeferCleanup(func() { os.RemoveAll(dir) })

		tmp, err = system.GetSystemState(system.WithModelPath(dir))
		Expect(err).ToNot(HaveOccurred())

		// No upstream: the list comes back empty, which is all this needs. What
		// is under test is how often a refresh is started, not what it returns.
		galleries = []config.Gallery{}
		origInterval = gallery.GalleryRefreshInterval
		gallery.ResetGalleryModelCache()
	})

	AfterEach(func() {
		gallery.GalleryRefreshInterval = origInterval
		gallery.ResetGalleryModelCache()
	})

	It("does not bump the generation once per call", func() {
		gallery.GalleryRefreshInterval = time.Hour

		_, err := gallery.AvailableGalleryModelsCached(galleries, tmp)
		Expect(err).ToNot(HaveOccurred())
		start := gallery.GalleryGeneration()

		// Stands in for one page view: many callers in quick succession.
		for i := 0; i < 30; i++ {
			_, err := gallery.AvailableGalleryModelsCached(galleries, tmp)
			Expect(err).ToNot(HaveOccurred())
		}
		// Let any refresh that did start finish, so this cannot pass by racing.
		Eventually(func() uint64 { return gallery.GalleryGeneration() }, "2s", "50ms").
			Should(Equal(start))
	})

	It("still refreshes once the interval has passed", func() {
		gallery.GalleryRefreshInterval = time.Millisecond

		_, err := gallery.AvailableGalleryModelsCached(galleries, tmp)
		Expect(err).ToNot(HaveOccurred())

		time.Sleep(5 * time.Millisecond)
		_, err = gallery.AvailableGalleryModelsCached(galleries, tmp)
		Expect(err).ToNot(HaveOccurred())

		// An empty gallery refreshing to an empty gallery is unchanged, so the
		// generation must hold: only a real change may invalidate the caches.
		Consistently(func() uint64 { return gallery.GalleryGeneration() }, "300ms", "50ms").
			Should(Equal(gallery.GalleryGeneration()))
	})
})
