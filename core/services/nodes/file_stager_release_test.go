package nodes

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/mudler/LocalAI/core/services/storage"
	"github.com/mudler/LocalAI/core/services/workerctl"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"io"
)

type releaseTestControl struct {
	subject       string
	payload       []byte
	onRequest     func()
	requestCalled bool
	requestCount  int
	timeout       time.Duration
	replies       [][]byte
}

func (m *releaseTestControl) RoundTrip(req *http.Request) (*http.Response, error) {
	if err := req.Context().Err(); err != nil {
		return nil, err
	}
	m.subject = req.URL.Path
	m.payload, _ = io.ReadAll(req.Body)
	m.requestCalled = true
	m.requestCount++
	if deadline, ok := req.Context().Deadline(); ok {
		m.timeout = time.Until(deadline)
	}
	if m.onRequest != nil {
		m.onRequest()
	}
	reply := []byte(`{}`)
	if m.requestCount <= len(m.replies) {
		reply = m.replies[m.requestCount-1]
	}
	return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(string(reply))), Request: req}, nil
}
func (m *releaseTestControl) control() *ControlClient {
	c := NewControlClient(directNetDialerFor, "")
	c.clients["node.one"] = &nodeHTTPClient{client: &http.Client{Transport: m}}
	return c
}

var _ = Describe("File stager exact-key release", func() {
	startReleaseServer := func(stagingDir, token string) (*HTTPFileStager, func()) {
		listener, err := net.Listen("tcp", "127.0.0.1:0")
		Expect(err).NotTo(HaveOccurred())
		server, err := StartFileTransferServerWithListener(
			listener,
			stagingDir,
			GinkgoT().TempDir(),
			GinkgoT().TempDir(),
			token,
			0,
		)
		Expect(err).NotTo(HaveOccurred())
		return NewHTTPFileStager(func(string) (string, error) {
				return listener.Addr().String(), nil
			}, token, directNetDialerFor), func() {
				Expect(server.Shutdown(context.Background())).To(Succeed())
			}
	}

	It("transmits URL metacharacters as the exact key", func() {
		stagingDir := GinkgoT().TempDir()
		categoryDir := filepath.Join(stagingDir, "ephemeral", "request-id", "audio")
		Expect(os.MkdirAll(categoryDir, 0750)).To(Succeed())

		key := "ephemeral/request-id/audio/name ?#%2F.wav"
		exactPath := filepath.Join(categoryDir, "name ?#%2F.wav")
		wrongPath := filepath.Join(categoryDir, "name ")
		for _, path := range []string{exactPath, exactPath + hashSidecarSuffix, exactPath + targetSidecarSuffix, wrongPath} {
			Expect(os.WriteFile(path, []byte("data"), 0640)).To(Succeed())
		}

		stager, stop := startReleaseServer(stagingDir, "release-token")
		DeferCleanup(stop)
		Expect(stager.ReleaseRemote(context.Background(), "node-1", key)).To(Succeed())

		Expect(exactPath).NotTo(BeAnExistingFile())
		Expect(exactPath + hashSidecarSuffix).NotTo(BeAnExistingFile())
		Expect(exactPath + targetSidecarSuffix).NotTo(BeAnExistingFile())
		Expect(wrongPath).To(BeAnExistingFile())
	})

	It("is idempotent and prunes empty category and request directories", func() {
		stagingDir := GinkgoT().TempDir()
		path := filepath.Join(stagingDir, "ephemeral", "request-id", "audio", "input.wav")
		Expect(os.MkdirAll(filepath.Dir(path), 0750)).To(Succeed())
		Expect(os.WriteFile(path, []byte("data"), 0640)).To(Succeed())

		stager, stop := startReleaseServer(stagingDir, "release-token")
		DeferCleanup(stop)
		for range 2 {
			Expect(stager.ReleaseRemote(context.Background(), "node-1", "ephemeral/request-id/audio/input.wav")).To(Succeed())
		}

		Expect(filepath.Join(stagingDir, "ephemeral", "request-id", "audio")).NotTo(BeADirectory())
		Expect(filepath.Join(stagingDir, "ephemeral", "request-id")).NotTo(BeADirectory())
		Expect(filepath.Join(stagingDir, "ephemeral")).To(BeADirectory())
	})

	It("releases one request's HTTP inputs in one batch", func() {
		stagingDir := GinkgoT().TempDir()
		keys := []string{
			"ephemeral/audio/request-id/input.wav",
			"ephemeral/images/request-id/frame.jpg",
		}
		for _, key := range keys {
			path := filepath.Join(stagingDir, filepath.FromSlash(key))
			Expect(os.MkdirAll(filepath.Dir(path), 0750)).To(Succeed())
			Expect(os.WriteFile(path, []byte("data"), 0640)).To(Succeed())
		}

		stager, stop := startReleaseServer(stagingDir, "release-token")
		DeferCleanup(stop)
		Expect(stager.ReleaseRemoteRequest(context.Background(), "node-1", "request-id", keys)).To(Succeed())

		for _, key := range keys {
			Expect(filepath.Join(stagingDir, filepath.FromSlash(key))).NotTo(BeAnExistingFile())
		}
	})

	It("falls back to exact HTTP releases for an older worker", func() {
		batchCalls := 0
		exactCalls := 0
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path == "/v1/files-release" {
				batchCalls++
				http.NotFound(w, r)
				return
			}
			if strings.HasPrefix(r.URL.Path, "/v1/files/") && r.Method == http.MethodDelete {
				exactCalls++
				w.WriteHeader(http.StatusNoContent)
				return
			}
			http.Error(w, "unexpected request", http.StatusInternalServerError)
		}))
		DeferCleanup(server.Close)
		stager := NewHTTPFileStager(func(string) (string, error) {
			return strings.TrimPrefix(server.URL, "http://"), nil
		}, "", directNetDialerFor)
		keys := []string{
			"ephemeral/audio/request-id/input.wav",
			"ephemeral/images/request-id/frame.jpg",
		}

		Expect(stager.ReleaseRemoteRequest(context.Background(), "node-1", "request-id", keys)).To(Succeed())

		Expect(batchCalls).To(Equal(1))
		Expect(exactCalls).To(Equal(2))
	})

	It("rejects non-ephemeral and traversing keys before making a request", func() {
		resolved := false
		stager := NewHTTPFileStager(func(string) (string, error) {
			resolved = true
			return "127.0.0.1:1", nil
		}, "token", directNetDialerFor)

		for _, key := range []string{
			"models/model.gguf",
			"ephemeral/../models/model.gguf",
			"ephemeral/request-id/../../model.gguf",
			"/ephemeral/request-id/audio/input.wav",
		} {
			Expect(stager.ReleaseRemote(context.Background(), "node-1", key)).NotTo(Succeed(), key)
		}
		Expect(resolved).To(BeFalse())
	})

	It("rejects symlink escapes", func() {
		stagingDir := GinkgoT().TempDir()
		outsideDir := GinkgoT().TempDir()
		outsidePath := filepath.Join(outsideDir, "input.wav")
		Expect(os.WriteFile(outsidePath, []byte("keep"), 0640)).To(Succeed())
		requestDir := filepath.Join(stagingDir, "ephemeral", "request-id")
		Expect(os.MkdirAll(requestDir, 0750)).To(Succeed())
		Expect(os.Symlink(outsideDir, filepath.Join(requestDir, "audio"))).To(Succeed())

		stager, stop := startReleaseServer(stagingDir, "release-token")
		DeferCleanup(stop)
		Expect(stager.ReleaseRemote(context.Background(), "node-1", "ephemeral/request-id/audio/input.wav")).NotTo(Succeed())
		Expect(outsidePath).To(BeAnExistingFile())
	})

	It("rejects symlinked files and sidecars without deleting their targets", func() {
		for _, linkedName := range []string{"input.wav", "input.wav" + hashSidecarSuffix, "input.wav" + targetSidecarSuffix} {
			stagingDir := GinkgoT().TempDir()
			categoryDir := filepath.Join(stagingDir, "ephemeral", "request-id", "audio")
			Expect(os.MkdirAll(categoryDir, 0750)).To(Succeed())
			target := filepath.Join(categoryDir, "input.wav")
			if linkedName != "input.wav" {
				Expect(os.WriteFile(target, []byte("input"), 0640)).To(Succeed())
			}
			preserved := filepath.Join(stagingDir, "ephemeral", "preserved-"+linkedName)
			Expect(os.WriteFile(preserved, []byte("keep"), 0640)).To(Succeed())
			Expect(os.Symlink(preserved, filepath.Join(categoryDir, linkedName))).To(Succeed())

			stager, stop := startReleaseServer(stagingDir, "release-token")
			Expect(stager.ReleaseRemote(context.Background(), "node-1", "ephemeral/request-id/audio/input.wav")).NotTo(Succeed(), linkedName)
			stop()
			Expect(preserved).To(BeAnExistingFile(), linkedName)
		}
	})

	It("evicts the worker cache before deleting the shared object", func() {
		storeRoot := GinkgoT().TempDir()
		cacheRoot := GinkgoT().TempDir()
		store, err := storage.NewFilesystemStore(storeRoot)
		Expect(err).NotTo(HaveOccurred())
		fm, err := storage.NewFileManager(store, cacheRoot)
		Expect(err).NotTo(HaveOccurred())
		key := "ephemeral/request-id/audio/input.wav"
		Expect(store.Put(context.Background(), key, strings.NewReader("shared"))).To(Succeed())

		client := &releaseTestControl{}
		client.onRequest = func() {
			exists, existsErr := store.Exists(context.Background(), key)
			Expect(existsErr).NotTo(HaveOccurred())
			Expect(exists).To(BeTrue())
		}
		stager := NewS3FileStager(fm, client.control())
		Expect(stager.ReleaseRemote(context.Background(), "node.one", key)).To(Succeed())

		Expect(client.requestCalled).To(BeTrue())
		Expect(client.subject).To(Equal(workerctl.PathFilesRelease))
		var payload fileReleaseRequest
		Expect(json.Unmarshal(client.payload, &payload)).To(Succeed())
		Expect(payload.Key).To(Equal(key))
		exists, err := store.Exists(context.Background(), key)
		Expect(err).NotTo(HaveOccurred())
		Expect(exists).To(BeFalse())
	})

	It("evicts a request's S3 inputs with one control round trip", func() {
		storeRoot := GinkgoT().TempDir()
		store, err := storage.NewFilesystemStore(storeRoot)
		Expect(err).NotTo(HaveOccurred())
		fm, err := storage.NewFileManager(store, GinkgoT().TempDir())
		Expect(err).NotTo(HaveOccurred())
		keys := []string{
			"ephemeral/audio/request-id/input.wav",
			"ephemeral/images/request-id/frame.jpg",
		}
		for _, key := range keys {
			Expect(store.Put(context.Background(), key, strings.NewReader("shared"))).To(Succeed())
		}
		client := &releaseTestControl{}
		stager := NewS3FileStager(fm, client.control())

		Expect(stager.ReleaseRemoteRequest(context.Background(), "node.one", "request-id", keys)).To(Succeed())

		Expect(client.requestCount).To(Equal(1))
		var payload fileReleaseRequest
		Expect(json.Unmarshal(client.payload, &payload)).To(Succeed())
		Expect(payload.Key).To(BeEmpty())
		Expect(payload.RequestID).To(Equal("request-id"))
		for _, key := range keys {
			exists, existsErr := store.Exists(context.Background(), key)
			Expect(existsErr).NotTo(HaveOccurred())
			Expect(exists).To(BeFalse())
		}
	})

	It("keeps worker coordination fixed-size for large requests", func() {
		store, err := storage.NewFilesystemStore(GinkgoT().TempDir())
		Expect(err).NotTo(HaveOccurred())
		fm, err := storage.NewFileManager(store, GinkgoT().TempDir())
		Expect(err).NotTo(HaveOccurred())
		keys := make([]string, 2048)
		for i := range keys {
			keys[i] = fmt.Sprintf("ephemeral/inputs/request-id/input-%d.bin", i)
		}
		client := &releaseTestControl{}
		stager := NewS3FileStager(fm, client.control())

		Expect(stager.ReleaseRemoteRequest(context.Background(), "node.one", "request-id", keys)).To(Succeed())

		Expect(client.requestCount).To(Equal(1))
		Expect(len(client.payload)).To(BeNumerically("<", 128))
		var payload fileReleaseRequest
		Expect(json.Unmarshal(client.payload, &payload)).To(Succeed())
		Expect(payload.RequestID).To(Equal("request-id"))
	})

	It("falls back to exact control releases for an older worker", func() {
		store, err := storage.NewFilesystemStore(GinkgoT().TempDir())
		Expect(err).NotTo(HaveOccurred())
		fm, err := storage.NewFileManager(store, GinkgoT().TempDir())
		Expect(err).NotTo(HaveOccurred())
		keys := []string{
			"ephemeral/audio/request-id/input.wav",
			"ephemeral/images/request-id/frame.jpg",
		}
		for _, key := range keys {
			Expect(store.Put(context.Background(), key, strings.NewReader("shared"))).To(Succeed())
		}
		client := &releaseTestControl{replies: [][]byte{
			[]byte(`{"error":"batch payload unsupported"}`),
			[]byte(`{}`),
			[]byte(`{}`),
		}}
		stager := NewS3FileStager(fm, client.control())

		Expect(stager.ReleaseRemoteRequest(context.Background(), "node.one", "request-id", keys)).To(Succeed())

		Expect(client.requestCount).To(Equal(3))
		for _, key := range keys {
			exists, existsErr := store.Exists(context.Background(), key)
			Expect(existsErr).NotTo(HaveOccurred())
			Expect(exists).To(BeFalse())
		}
	})

	It("rejects release batches that mix request IDs", func() {
		keys := []string{
			"ephemeral/audio/request-one/input.wav",
			"ephemeral/images/request-two/frame.jpg",
		}
		Expect(validateEphemeralRequestRelease("request-one", keys)).To(MatchError(ContainSubstring("mixes request IDs")))
	})

	It("does not send a release request after cleanup is canceled", func() {
		store, err := storage.NewFilesystemStore(GinkgoT().TempDir())
		Expect(err).NotTo(HaveOccurred())
		fm, err := storage.NewFileManager(store, GinkgoT().TempDir())
		Expect(err).NotTo(HaveOccurred())
		client := &releaseTestControl{}
		stager := NewS3FileStager(fm, client.control())
		ctx, cancel := context.WithCancel(context.Background())
		cancel()

		Expect(stager.ReleaseRemote(ctx, "node.one", "ephemeral/request-id/audio/input.wav")).To(MatchError(context.Canceled))
		Expect(client.requestCalled).To(BeFalse())
	})

	It("bounds the control release wait by the remaining cleanup deadline", func() {
		store, err := storage.NewFilesystemStore(GinkgoT().TempDir())
		Expect(err).NotTo(HaveOccurred())
		fm, err := storage.NewFileManager(store, GinkgoT().TempDir())
		Expect(err).NotTo(HaveOccurred())
		client := &releaseTestControl{}
		stager := NewS3FileStager(fm, client.control())
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()

		Expect(stager.ReleaseRemote(ctx, "node.one", "ephemeral/request-id/audio/input.wav")).To(Succeed())
		Expect(client.timeout).To(BeNumerically(">", time.Second))
		Expect(client.timeout).To(BeNumerically("<=", 2*time.Second))
	})
})
