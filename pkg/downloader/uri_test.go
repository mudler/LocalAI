package downloader_test

import (
	"crypto/rand"
	"crypto/sha256"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"regexp"
	"strconv"

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
	var mockData []byte
	var mockDataSha string
	var filePath string

	var getMockServer = func() *httptest.Server {
		mockServer := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			var respData []byte
			rangeString := r.Header.Get("Range")
			if rangeString != "" {
				regex := regexp.MustCompile(`^bytes=(\d+)-(\d+|)$`)
				matches := regex.FindStringSubmatch(rangeString)
				if matches == nil {
					w.WriteHeader(http.StatusBadRequest)
					return
				}
				startPos := 0
				endPos := len(mockData)
				var err error
				if matches[1] != "" {
					startPos, err = strconv.Atoi(matches[1])
					Expect(err).ToNot(HaveOccurred())
				}
				if matches[2] != "" {
					endPos, err = strconv.Atoi(matches[2])
					Expect(err).ToNot(HaveOccurred())
					endPos += 1
				}
				if startPos < 0 || startPos >= len(mockData) || endPos < 0 || endPos > len(mockData) || startPos > endPos {
					w.WriteHeader(http.StatusBadRequest)
					return
				}
				respData = mockData[startPos:endPos]
			} else {
				respData = mockData
			}
			w.WriteHeader(http.StatusOK)
			w.Write(respData)
		}))
		mockServer.EnableHTTP2 = true
		mockServer.Start()
		return mockServer
	}

	BeforeEach(func() {
		mockData = make([]byte, 20000)
		_, err := rand.Read(mockData)
		Expect(err).ToNot(HaveOccurred())
		_mockDataSha := sha256.New()
		_, err = _mockDataSha.Write(mockData)
		Expect(err).ToNot(HaveOccurred())
		mockDataSha = fmt.Sprintf("%x", _mockDataSha.Sum(nil))
		dir, err := os.Getwd()
		filePath = dir + "/my_supercool_model"
		Expect(err).NotTo(HaveOccurred())
	})

	Context("URI DownloadFile", func() {
		It("fetches files from mock server", func() {
			mockServer := getMockServer()
			defer mockServer.Close()
			uri := URI(mockServer.URL)
			err := uri.DownloadFile(filePath, mockDataSha, 1, 1, func(s1, s2, s3 string, f float64) {})
			Expect(err).ToNot(HaveOccurred())
			err = os.Remove(filePath) // cleanup, also checks existance of filePath`
			Expect(err).ToNot(HaveOccurred())
		})

		It("resumes partially downloaded files", func() {
			mockServer := getMockServer()
			defer mockServer.Close()
			uri := URI(mockServer.URL)
			// Create a partial file
			tmpFilePath := filePath + ".partial"
			file, err := os.OpenFile(tmpFilePath, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0644)
			Expect(err).ToNot(HaveOccurred())
			_, err = file.Write(mockData[0:10000])
			Expect(err).ToNot(HaveOccurred())
			err = uri.DownloadFile(filePath, mockDataSha, 1, 1, func(s1, s2, s3 string, f float64) {})
			Expect(err).ToNot(HaveOccurred())
			err = os.Remove(filePath) // cleanup, also checks existance of filePath`
			Expect(err).ToNot(HaveOccurred())
		})
		// It("it accurately updates progress")
		// It("deletes partial file if after completion hash of downloaded file doesn't match hash of the file in the server")
	})
})
