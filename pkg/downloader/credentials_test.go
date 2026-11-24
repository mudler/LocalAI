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

	It("does not retry an unreadable secret met while probing a resume", func() {
		var requests atomic.Int32
		srv := requireBearer("file-token", &requests)
		useStore(fmt.Sprintf("- match: %s\n  bearer_env: MISSING_TOKEN\n  allow_insecure: true\n", srv.URL))

		target := filepath.Join(GinkgoT().TempDir(), "model.yaml")
		Expect(os.WriteFile(target+".partial", []byte("- name: priv"), 0o600)).To(Succeed())
		err := downloader.URI(srv.URL+"/models/model.yaml").DownloadFileWithContext(context.Background(), target, "", 0, 1, nil)
		Expect(errors.Is(err, credentials.ErrUnresolvedSecret)).To(BeTrue(), "got %v", err)
		Expect(downloader.IsRetryable(context.Background(), err)).To(BeFalse())
		Expect(requests.Load()).To(BeEquivalentTo(0))
	})

	It("lets an explicit bearer token win over a matching rule", func() {
		var seen string
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			seen = r.Header.Get("Authorization")
			_, _ = w.Write([]byte("- name: private-model\n"))
		}))
		DeferCleanup(srv.Close)
		useStore(fmt.Sprintf("- match: %s\n  bearer: store-token\n  allow_insecure: true\n", srv.URL))

		target := filepath.Join(GinkgoT().TempDir(), "model.yaml")
		Expect(downloader.URI(srv.URL+"/models/model.yaml").DownloadFileWithContext(context.Background(), target, "", 0, 1, nil,
			downloader.WithBearerToken("explicit-token"))).To(Succeed())
		Expect(seen).To(Equal("Bearer explicit-token"))
	})

	It("blames the provided bearer token, not the store, when it is rejected", func() {
		var requests atomic.Int32
		srv := requireBearer("right-token", &requests)
		rule := srv.URL + "/models"
		useStore(fmt.Sprintf("- match: %s\n  bearer: store-token\n  allow_insecure: true\n", rule))

		target := filepath.Join(GinkgoT().TempDir(), "model.yaml")
		err := downloader.URI(srv.URL+"/models/model.yaml").DownloadFileWithContext(context.Background(), target, "", 0, 1, nil,
			downloader.WithBearerToken("wrong-token"))
		Expect(err).To(MatchError(ContainSubstring("provided credential was rejected")))
		Expect(err).To(MatchError(ContainSubstring("status 401")))
		Expect(err.Error()).NotTo(ContainSubstring(fmt.Sprintf("credential %q", rule)))
		Expect(err.Error()).NotTo(ContainSubstring("credentials rule"))
		Expect(err.Error()).NotTo(ContainSubstring("wrong-token"))
		var authErr *credentials.AuthError
		Expect(errors.As(err, &authErr)).To(BeTrue(), "got %v", err)
		Expect(authErr.Match).To(BeEmpty())
	})

	It("blames the provided authorization, not the store, when a gallery rejects it", func() {
		var requests atomic.Int32
		srv := requireBearer("right-token", &requests)
		useStore(fmt.Sprintf("- match: %s\n  bearer: store-token\n  allow_insecure: true\n", srv.URL))

		err := downloader.URI(srv.URL+"/index.yaml").ReadWithAuthorizationAndCallback(context.Background(), GinkgoT().TempDir(), "Bearer wrong-token",
			func(string, []byte) error { return nil })
		Expect(err).To(MatchError(ContainSubstring("provided credential was rejected")))
		Expect(err.Error()).NotTo(ContainSubstring("no credentials rule"))
		Expect(err.Error()).NotTo(ContainSubstring("wrong-token"))
	})

	It("keeps signed query strings out of auth errors", func() {
		var requests atomic.Int32
		srv := requireBearer("right-token", &requests)
		useStore("")

		target := filepath.Join(GinkgoT().TempDir(), "model.yaml")
		err := downloader.URI(srv.URL+"/models/model.yaml?X-Amz-Signature=topsecret").DownloadFileWithContext(context.Background(), target, "", 0, 1, nil)
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).NotTo(ContainSubstring("topsecret"))
	})

	It("behaves as before with no store installed", func() {
		var seen []string
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			seen = append(seen, r.Header.Get("Authorization"))
			_, _ = w.Write([]byte("- name: public\n"))
		}))
		DeferCleanup(srv.Close)
		prev := credentials.SetDefault(nil)
		DeferCleanup(func() { credentials.SetDefault(prev) })

		_, err := readGallery(srv.URL + "/index.yaml")
		Expect(err).NotTo(HaveOccurred())
		target := filepath.Join(GinkgoT().TempDir(), "model.yaml")
		Expect(downloader.URI(srv.URL+"/models/model.yaml").DownloadFileWithContext(context.Background(), target, "", 0, 1, nil,
			downloader.WithBearerToken("explicit-token"))).To(Succeed())
		Expect(seen).To(Equal([]string{"", "Bearer explicit-token"}))
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
