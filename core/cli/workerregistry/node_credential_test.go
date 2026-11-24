package workerregistry

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("The own credential of a node", func() {
	// frontend answers registrations like a frontend: a node credential in the
	// answer to a join, nothing when the worker registers with its credential.
	var (
		seen   []map[string]any
		auths  []string
		srv    *httptest.Server
		answer map[string]any
	)

	BeforeEach(func() {
		seen, auths = nil, nil
		answer = map[string]any{"id": "n1", "node_token": "own-secret", "worker_token": "data-token"}
		srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			auths = append(auths, r.Method+" "+r.URL.Path+" "+r.Header.Get("Authorization"))
			if r.URL.Path == "/api/node/register" {
				var body map[string]any
				Expect(json.NewDecoder(r.Body).Decode(&body)).To(Succeed())
				seen = append(seen, body)
				w.WriteHeader(http.StatusCreated)
				_ = json.NewEncoder(w).Encode(answer)
				return
			}
			w.WriteHeader(http.StatusOK)
		}))
		DeferCleanup(srv.Close)
	})

	It("sends the join token and the registration token in registration only, and the credential everywhere else", func() {
		c := &RegistrationClient{FrontendURL: srv.URL, RegistrationToken: "shared", JoinToken: "ljt_x.y"}
		_, err := c.RegisterFull(context.Background(), map[string]any{"name": "w", "token": "stale"})
		Expect(err).ToNot(HaveOccurred())
		Expect(seen[0]).To(HaveKeyWithValue("join_token", "ljt_x.y"))
		Expect(seen[0]).To(HaveKeyWithValue("token", "shared"))
		Expect(auths[0]).To(Equal("POST /api/node/register "), "no credential yet")
		Expect(c.WorkerToken()).To(Equal("data-token"))

		Expect(c.SendHeartbeat(context.Background(), "n1", map[string]any{})).To(Succeed())
		Expect(c.Drain(context.Background(), "n1")).To(Succeed())
		Expect(c.Deregister(context.Background(), "n1")).To(Succeed())
		for _, a := range auths[1:] {
			Expect(a).To(HaveSuffix(" Bearer own-secret"), a)
			Expect(a).ToNot(ContainSubstring("shared"))
			Expect(a).ToNot(ContainSubstring("ljt_x"))
		}
	})

	It("registers again with the credential, which the frontend tries before the join token", func() {
		c := &RegistrationClient{FrontendURL: srv.URL, JoinToken: "ljt_x.y"}
		_, err := c.RegisterFull(context.Background(), map[string]any{"name": "w"})
		Expect(err).ToNot(HaveOccurred())
		answer = map[string]any{"id": "n1"}
		_, err = c.RegisterFull(context.Background(), map[string]any{"name": "w"})
		Expect(err).ToNot(HaveOccurred())
		Expect(auths[1]).To(HaveSuffix(" Bearer own-secret"))
		Expect(c.NodeToken()).To(Equal("own-secret"), "an answer with no credential keeps the one it has")
	})

	It("keeps the credential in the state directory, so a restart needs no join token", func() {
		dir := filepath.Join(GinkgoT().TempDir(), "data")
		store := &FileCredential{Dir: dir, FrontendURL: srv.URL + "/", NodeName: "w"}

		first := &RegistrationClient{FrontendURL: srv.URL, JoinToken: "ljt_x.y", Credential: store}
		_, err := first.RegisterFull(context.Background(), map[string]any{"name": "w"})
		Expect(err).ToNot(HaveOccurred())

		answer = map[string]any{"id": "n1"}
		restarted := &RegistrationClient{FrontendURL: srv.URL, Credential: &FileCredential{Dir: dir, FrontendURL: srv.URL, NodeName: "w"}}
		_, err = restarted.RegisterFull(context.Background(), map[string]any{"name": "w"})
		Expect(err).ToNot(HaveOccurred())
		Expect(seen[1]).ToNot(HaveKey("join_token"))
		Expect(auths[1]).To(HaveSuffix(" Bearer own-secret"))

		if runtime.GOOS != "windows" {
			info, err := os.Stat(filepath.Join(dir, CredentialFileName))
			Expect(err).ToNot(HaveOccurred())
			Expect(info.Mode().Perm()).To(Equal(os.FileMode(0o600)))
			dinfo, err := os.Stat(dir)
			Expect(err).ToNot(HaveOccurred())
			Expect(dinfo.Mode().Perm()).To(Equal(os.FileMode(0o700)))
		}
	})

	It("does not present the credential of another frontend or another name", func() {
		dir := GinkgoT().TempDir()
		Expect((&FileCredential{Dir: dir, FrontendURL: "http://a:8080", NodeName: "w"}).Save("secret")).To(Succeed())

		got, err := (&FileCredential{Dir: dir, FrontendURL: "http://a:8080", NodeName: "w"}).Load()
		Expect(err).ToNot(HaveOccurred())
		Expect(got).To(Equal("secret"))
		for _, other := range []*FileCredential{
			{Dir: dir, FrontendURL: "http://b:8080", NodeName: "w"},
			{Dir: dir, FrontendURL: "http://a:8080", NodeName: "x"},
			{Dir: filepath.Join(dir, "none"), FrontendURL: "http://a:8080", NodeName: "w"},
		} {
			got, err := other.Load()
			Expect(err).ToNot(HaveOccurred())
			Expect(got).To(BeEmpty())
		}
	})
})
