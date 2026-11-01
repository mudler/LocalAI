package collections

import (
	"context"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/mudler/LocalAI/core/config"
)

const (
	userA = "11111111-1111-4111-8111-111111111111"
	userB = "22222222-2222-4222-8222-222222222222"
)

func newService() (*Service, string) {
	dir := GinkgoT().TempDir()
	cfg := config.NewApplicationConfig()
	cfg.DataPath = dir
	cfg.APIAddress = "127.0.0.1:8080"
	s, err := New(cfg, nil)
	Expect(err).ToNot(HaveOccurred())
	return s, dir
}

// roundTripFunc is an http.RoundTripper that answers in memory.
type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

// embeddingClient answers every request with one embedding.
func embeddingClient() *http.Client {
	return &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		body := `{"data":[{"embedding":[1,0,0],"index":0}],"model":"embed","usage":{}}`
		return &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": {"application/json"}},
			Body: io.NopCloser(strings.NewReader(body)), Request: r}, nil
	})}
}

var _ = Describe("the per-user service", func() {
	It("gives each user collections of their own", func() {
		s, _ := newService()
		Expect(s.CreateForUser(userA, "notes")).To(Succeed())
		Expect(s.CreateForUser(userB, "other")).To(Succeed())
		a, _ := s.ListForUser(userA)
		b, _ := s.ListForUser(userB)
		Expect(a).To(Equal([]string{"notes"}), "user A has %v, user B has %v", a, b)
		Expect(b).To(Equal([]string{"other"}), "user A has %v, user B has %v", a, b)
		root, err := s.ListForUser("")
		Expect(err).ToNot(HaveOccurred())
		Expect(root).To(BeEmpty())
	})

	It("keeps the root and the users in their own folders", func() {
		s, dir := newService()
		_ = s.CreateForUser(userA, "notes")
		_ = s.CreateForUser("", "shared")
		for _, p := range []string{
			filepath.Join(dir, "users", userA, "collections"),
			filepath.Join(dir, "users", userA, "assets"),
			filepath.Join(dir, "collections"),
		} {
			st, err := os.Stat(p)
			Expect(err).ToNot(HaveOccurred(), p)
			Expect(st.IsDir()).To(BeTrue(), p)
		}
		ids, err := s.UserIDs()
		Expect(err).ToNot(HaveOccurred())
		Expect(ids).To(Equal([]string{userA}), "user dirs")
	})

	It("refuses a user ID that is not a UUID", func() {
		s, _ := newService()
		for _, id := range []string{"../x", "a/b", "not-a-uuid", "..", `a\b`} {
			_, err := s.Backend(id)
			Expect(err).To(HaveOccurred(), "user ID %q was accepted", id)
		}
		Expect(ValidateUserID("")).To(Succeed(), "the root user ID")
	})

	It("makes the backend of a user once", func() {
		s, _ := newService()
		one, _ := s.Backend(userA)
		two, _ := s.Backend(userA)
		Expect(one).To(BeIdenticalTo(two), "a second call made another backend")
	})

	It("sends embeddings through the client the host gives", func() {
		cfg := config.NewApplicationConfig()
		cfg.DataPath = GinkgoT().TempDir()
		// Nothing listens here: a call that leaves the process cannot succeed.
		cfg.APIAddress = "127.0.0.1:1"
		cfg.Collections.VectorEngine = "chromem"
		cfg.Collections.EmbeddingModel = "embed"

		var calls atomic.Int32
		var credential atomic.Value
		client := &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
			calls.Add(1)
			if got := r.Header.Get("Authorization"); got != "" {
				credential.Store(got)
			}
			body := `{"data":[{"embedding":[1,0,0],"index":0}],"model":"embed","usage":{}}`
			return &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": {"application/json"}},
				Body: io.NopCloser(strings.NewReader(body)), Request: r}, nil
		})}
		s, err := New(cfg, nil, WithHTTPClient(client))
		Expect(err).ToNot(HaveOccurred())
		Expect(s.CreateForUser(userA, "notes")).To(Succeed())
		_, err = s.UploadForUser(context.Background(), userA, "notes", "a.txt", strings.NewReader("hello there"))
		Expect(err).ToNot(HaveOccurred(), "upload")
		Expect(credential.Load()).To(BeNil(), "the in-process call carries a credential")
		Expect(calls.Load()).ToNot(BeZero(), "the embedding call did not use the client of the host")
	})

	It("keeps the network path when a collections key is configured", func() {
		cfg := config.NewApplicationConfig()
		cfg.DataPath = GinkgoT().TempDir()
		cfg.Collections.APIKey = "operator-key"
		s, err := New(cfg, nil, WithHTTPClient(&http.Client{}))
		Expect(err).ToNot(HaveOccurred())
		Expect(s.httpClient).To(BeNil(), "an operator key was set, but the service still uses the in-process client")
	})

	It("resets a collection to empty and keeps it", func() {
		cfg := config.NewApplicationConfig()
		cfg.DataPath = GinkgoT().TempDir()
		cfg.Collections.VectorEngine = "chromem"
		cfg.Collections.EmbeddingModel = "embed"
		s, err := New(cfg, nil, WithHTTPClient(embeddingClient()))
		Expect(err).ToNot(HaveOccurred())
		Expect(s.CreateForUser(userA, "notes")).To(Succeed())
		_, err = s.UploadForUser(context.Background(), userA, "notes", "a.txt", strings.NewReader("hello there"))
		Expect(err).ToNot(HaveOccurred())
		Expect(s.ResetForUser(userA, "notes")).To(Succeed(), "reset")
		names, err := s.ListForUser(userA)
		Expect(err).ToNot(HaveOccurred())
		Expect(names).To(Equal([]string{"notes"}), "collections after the reset")
		entries, err := s.ListEntriesForUser(userA, "notes")
		Expect(err).ToNot(HaveOccurred())
		Expect(entries).To(BeEmpty(), "entries after the reset")
		// It takes uploads again.
		_, err = s.UploadForUser(context.Background(), userA, "notes", "b.txt", strings.NewReader("again"))
		Expect(err).ToNot(HaveOccurred(), "upload after the reset")
	})

	It("lists an empty list, not nil", func() {
		s, _ := newService()
		names, err := s.ListForUser(userA)
		Expect(err).ToNot(HaveOccurred())
		Expect(names).ToNot(BeNil(), "collections of a user with none")
		Expect(names).To(BeEmpty())
	})

	It("keeps a collection that had a source on reset", func() {
		cfg := config.NewApplicationConfig()
		cfg.DataPath = GinkgoT().TempDir()
		cfg.Collections.VectorEngine = "chromem"
		cfg.Collections.EmbeddingModel = "embed"
		s, err := New(cfg, nil, WithHTTPClient(embeddingClient()))
		Expect(err).ToNot(HaveOccurred())
		Expect(s.CreateForUser(userA, "notes")).To(Succeed())
		// The fetch fails, but the collection has a source history from now on.
		_ = s.AddSourceForUser(userA, "notes", "https://example.invalid/feed", 60)
		_ = s.RemoveSourceForUser(userA, "notes", "https://example.invalid/feed")
		_, err = s.UploadForUser(context.Background(), userA, "notes", "a.txt", strings.NewReader("hello there"))
		Expect(err).ToNot(HaveOccurred())
		Expect(s.ResetForUser(userA, "notes")).To(Succeed(), "reset")
		entries, err := s.ListEntriesForUser(userA, "notes")
		Expect(err).ToNot(HaveOccurred())
		Expect(entries).To(BeEmpty(), "entries after the reset")
	})
})
