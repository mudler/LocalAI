package downloader_test

import (
	"crypto/rand"
	"crypto/sha256"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"

	. "github.com/mudler/LocalAI/pkg/downloader"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("Gallery API tests", func() {
	Context("URI", func() {
		It("parses github with a branch", func() {
			uri := URI("github:go-skynet/model-gallery/gpt4all-j.yaml")
			Expect(
				uri.DownloadWithCallback("", func(url string, i []byte) error {
					Expect(url).To(Equal("https://raw.githubusercontent.com/go-skynet/model-gallery/main/gpt4all-j.yaml"))
					return nil
				}),
			).ToNot(HaveOccurred())
		})
		It("parses github without a branch", func() {
			uri := URI("github:go-skynet/model-gallery/gpt4all-j.yaml@main")

			Expect(
				uri.DownloadWithCallback("", func(url string, i []byte) error {
					Expect(url).To(Equal("https://raw.githubusercontent.com/go-skynet/model-gallery/main/gpt4all-j.yaml"))
					return nil
				}),
			).ToNot(HaveOccurred())
		})
		It("parses github with urls", func() {
			uri := URI("https://raw.githubusercontent.com/go-skynet/model-gallery/main/gpt4all-j.yaml")
			Expect(
				uri.DownloadWithCallback("", func(url string, i []byte) error {
					Expect(url).To(Equal("https://raw.githubusercontent.com/go-skynet/model-gallery/main/gpt4all-j.yaml"))
					return nil
				}),
			).ToNot(HaveOccurred())
		})
	})
})

var _ = Describe("Download Test", func() {
	Context("URI DownloadFile", func() {
		It("fetches files from mock server", func() {
			mockData := make([]byte, 20000)
			_, err := rand.Read(mockData)
			Expect(err).ToNot(HaveOccurred())

			mockDataSha := sha256.New()
			mockDataSha.Write(mockData)

			mockServer := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(http.StatusOK)
				w.Write(mockData)
			}))
			mockServer.EnableHTTP2 = true
			mockServer.Start()
			dir, err := os.Getwd()
			filePath := dir + "/my_supercool_model"
			Expect(err).NotTo(HaveOccurred())
			uri := URI(mockServer.URL)
			err = uri.DownloadFile(filePath, fmt.Sprintf("%x", mockDataSha.Sum(nil)), 1, 1, func(s1, s2, s3 string, f float64) {})
			Expect(err).ToNot(HaveOccurred())
			err = os.Remove(filePath) // cleanup, also checks existance of filePath`
			Expect(err).ToNot(HaveOccurred())
		})
		// It("resumes partially downloaded files")
		// It("it accurately updates progress")
		// It("deletes partial file if after completion hash of downloaded file doesn't match hash of the file in the server")
	})
})
