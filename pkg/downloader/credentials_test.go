package downloader_test

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync/atomic"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/mudler/LocalAI/pkg/credentials"
	"github.com/mudler/LocalAI/pkg/downloader"
)

var _ = Describe("downloads with a credentials store", Serial, func() {
	useStore := func(doc string) {
		s, err := credentials.Parse([]byte(doc), func(string) (string, bool) { return "", false })
		Expect(err).NotTo(HaveOccurred())
		prev := credentials.SetDefault(s)
		DeferCleanup(func() { credentials.SetDefault(prev) })
	}

	requireBearer := func(token string, requests *atomic.Int32) *httptest.Server {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			requests.Add(1)
			if r.Header.Get("Authorization") != "Bearer "+token {
				w.WriteHeader(http.StatusUnauthorized)
				return
			}
			_, _ = w.Write([]byte("- name: private-model\n"))
		}))
		DeferCleanup(srv.Close)
		return srv
	}

	readGallery := func(rawURL string) (string, error) {
		var body string
		err := downloader.URI(rawURL).ReadWithAuthorizationAndCallback(context.Background(), GinkgoT().TempDir(), "",
			func(_ string, b []byte) error {
				body = string(b)
				return nil
			})
		return body, err
	}

	It("authenticates a gallery index read", func() {
		var requests atomic.Int32
		srv := requireBearer("gallery-token", &requests)
		useStore(fmt.Sprintf("- match: %s\n  bearer: gallery-token\n  allow_insecure: true\n", srv.URL))

		body, err := readGallery(srv.URL + "/index.yaml")
		Expect(err).NotTo(HaveOccurred())
		Expect(body).To(ContainSubstring("private-model"))
	})

	It("authenticates a file download", func() {
		var requests atomic.Int32
		srv := requireBearer("file-token", &requests)
		useStore(fmt.Sprintf("- match: %s/models\n  bearer: file-token\n  allow_insecure: true\n", srv.URL))

		target := filepath.Join(GinkgoT().TempDir(), "model.yaml")
		Expect(downloader.URI(srv.URL+"/models/model.yaml").DownloadFileWithContext(context.Background(), target, "", 0, 1, nil)).To(Succeed())
		data, err := os.ReadFile(target)
		Expect(err).NotTo(HaveOccurred())
		Expect(string(data)).To(ContainSubstring("private-model"))
	})

	It("reports a missing rule on 401 without retrying", func() {
		var requests atomic.Int32
		srv := requireBearer("file-token", &requests)
		useStore("")

		target := filepath.Join(GinkgoT().TempDir(), "model.yaml")
		err := downloader.URI(srv.URL+"/models/model.yaml").DownloadFileWithContext(context.Background(), target, "", 0, 1, nil)
		var authErr *credentials.AuthError
		Expect(errors.As(err, &authErr)).To(BeTrue(), "got %v", err)
		Expect(authErr.Match).To(BeEmpty())
		Expect(authErr.Status).To(Equal(http.StatusUnauthorized))
		Expect(requests.Load()).To(BeEquivalentTo(1))
	})

	It("names the rule a gallery server rejected", func() {
		var requests atomic.Int32
		srv := requireBearer("right-token", &requests)
		useStore(fmt.Sprintf("- match: %s\n  bearer: wrong-token\n  allow_insecure: true\n", srv.URL))

		_, err := readGallery(srv.URL + "/index.yaml")
		var authErr *credentials.AuthError
		Expect(errors.As(err, &authErr)).To(BeTrue(), "got %v", err)
		Expect(authErr.Match).To(Equal(srv.URL))
		Expect(err.Error()).NotTo(ContainSubstring("wrong-token"))
	})

	It("does not treat an unreadable secret as a retryable network error", func() {
		var requests atomic.Int32
		srv := requireBearer("file-token", &requests)
		useStore(fmt.Sprintf("- match: %s\n  bearer_env: MISSING_TOKEN\n  allow_insecure: true\n", srv.URL))

		target := filepath.Join(GinkgoT().TempDir(), "model.yaml")
		err := downloader.URI(srv.URL+"/models/model.yaml").DownloadFileWithContext(context.Background(), target, "", 0, 1, nil)
		Expect(err).To(MatchError(ContainSubstring("MISSING_TOKEN")))
		Expect(errors.Is(err, credentials.ErrUnresolvedSecret)).To(BeTrue())
		Expect(downloader.IsRetryable(context.Background(), err)).To(BeFalse())
		Expect(requests.Load()).To(BeEquivalentTo(0))
	})

	It("keeps anonymous downloads anonymous when no rule matches", func() {
		var seen string
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			seen = r.Header.Get("Authorization")
			_, _ = w.Write([]byte("- name: public\n"))
		}))
		DeferCleanup(srv.Close)
		useStore("- match: https://elsewhere.example.com\n  bearer: unrelated\n")

		_, err := readGallery(srv.URL + "/index.yaml")
		Expect(err).NotTo(HaveOccurred())
		Expect(seen).To(BeEmpty())
	})
})
