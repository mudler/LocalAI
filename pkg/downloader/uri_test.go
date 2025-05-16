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

type RangeHeaderError struct {
	msg string
}

func (e *RangeHeaderError) Error() string { return e.msg }

var _ = Describe("Download Test", func() {
	var mockData []byte
	var mockDataSha string
	var filePath string

	extractRangeHeader := func(rangeString string) (int, int, error) {
		regex := regexp.MustCompile(`^bytes=(\d+)-(\d+|)$`)
		matches := regex.FindStringSubmatch(rangeString)
		rangeErr := RangeHeaderError{msg: "invalid / ill-formatted range"}
		if matches == nil {
			return -1, -1, &rangeErr
		}
		startPos, err := strconv.Atoi(matches[1])
		if err != nil {
			return -1, -1, err
		}

		endPos := -1
		if matches[2] != "" {
			endPos, err = strconv.Atoi(matches[2])
			if err != nil {
				return -1, -1, err
			}
			endPos += 1 // because range is inclusive in rangeString
		}
		return startPos, endPos, nil
	}

	getMockServer := func(supportsRangeHeader bool) *httptest.Server {
		mockServer := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Method != "HEAD" && r.Method != "GET" {
				w.WriteHeader(http.StatusNotFound)
				return
			}
			if r.Method == "HEAD" {
				if supportsRangeHeader {
					w.Header().Add("Accept-Ranges", "bytes")
				}
				w.WriteHeader(http.StatusOK)
				return
			}
			// GET method
			startPos := 0
			endPos := len(mockData)
			var err error
			var respData []byte
			rangeString := r.Header.Get("Range")
			if rangeString != "" {
				startPos, endPos, err = extractRangeHeader(rangeString)
				if err != nil {
					if _, ok := err.(*RangeHeaderError); ok {
						w.WriteHeader(http.StatusBadRequest)
						return
					}
					Expect(err).ToNot(HaveOccurred())
				}
				if endPos == -1 {
					endPos = len(mockData)
				}
				if startPos < 0 || startPos >= len(mockData) || endPos < 0 || endPos > len(mockData) || startPos > endPos {
					w.WriteHeader(http.StatusBadRequest)
					return
				}
			}
			respData = mockData[startPos:endPos]
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
			mockServer := getMockServer(true)
			defer mockServer.Close()
			uri := URI(mockServer.URL)
			err := uri.DownloadFile(filePath, mockDataSha, 1, 1, func(s1, s2, s3 string, f float64) {})
			Expect(err).ToNot(HaveOccurred())
		})

		It("resumes partially downloaded files", func() {
			mockServer := getMockServer(true)
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
		})

		It("restarts download from 0 if server doesn't support Range header", func() {
			mockServer := getMockServer(false)
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
		})
	})

	AfterEach(func() {
		os.Remove(filePath) // cleanup, also checks existence of filePath`
		os.Remove(filePath + ".partial")
	})
})
