package downloader_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/mudler/LocalAI/pkg/downloader"
)

var _ = Describe("DownloadFilesWithContext", func() {
	It("runs the post-download hook after fetching each file", func() {
		payload := []byte("downloaded-bytes")
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			_, _ = w.Write(payload)
		}))
		DeferCleanup(server.Close)

		dest := filepath.Join(GinkgoT().TempDir(), "model.bin")
		hookCalled := false

		err := downloader.DownloadFilesWithContext(context.Background(), []downloader.FileTask{{
			URI:         downloader.URI(server.URL),
			Destination: dest,
			FileIndex:   0,
			TotalFiles:  1,
			AfterDownload: func(path string) error {
				hookCalled = true
				got, err := os.ReadFile(path)
				Expect(err).NotTo(HaveOccurred())
				Expect(string(got)).To(Equal(string(payload)))
				return nil
			},
		}}, nil)

		Expect(err).NotTo(HaveOccurred())
		Expect(hookCalled).To(BeTrue())
	})
})
