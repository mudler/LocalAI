package nodes

import (
	"context"
	"encoding/json"
	"net"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/mudler/LocalAI/core/services/messaging"
	"github.com/mudler/LocalAI/core/services/storage"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

type releaseTestSubscription struct{}

func (releaseTestSubscription) Unsubscribe() error { return nil }

type releaseTestMessaging struct {
	subject       string
	payload       []byte
	onRequest     func()
	requestCalled bool
}

func (m *releaseTestMessaging) Publish(string, any) error { return nil }
func (m *releaseTestMessaging) Subscribe(string, func([]byte)) (messaging.Subscription, error) {
	return releaseTestSubscription{}, nil
}
func (m *releaseTestMessaging) QueueSubscribe(string, string, func([]byte)) (messaging.Subscription, error) {
	return releaseTestSubscription{}, nil
}
func (m *releaseTestMessaging) QueueSubscribeReply(string, string, func([]byte, func([]byte))) (messaging.Subscription, error) {
	return releaseTestSubscription{}, nil
}
func (m *releaseTestMessaging) SubscribeReply(string, func([]byte, func([]byte))) (messaging.Subscription, error) {
	return releaseTestSubscription{}, nil
}
func (m *releaseTestMessaging) Request(subject string, data []byte, _ time.Duration) ([]byte, error) {
	m.subject = subject
	m.payload = append([]byte(nil), data...)
	m.requestCalled = true
	if m.onRequest != nil {
		m.onRequest()
	}
	return []byte(`{}`), nil
}
func (m *releaseTestMessaging) IsConnected() bool { return true }
func (m *releaseTestMessaging) Close()            {}

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
			}, token), func() {
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

	It("rejects non-ephemeral and traversing keys before making a request", func() {
		resolved := false
		stager := NewHTTPFileStager(func(string) (string, error) {
			resolved = true
			return "127.0.0.1:1", nil
		}, "token")

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

		client := &releaseTestMessaging{}
		client.onRequest = func() {
			exists, existsErr := store.Exists(context.Background(), key)
			Expect(existsErr).NotTo(HaveOccurred())
			Expect(exists).To(BeTrue())
		}
		stager := NewS3NATSFileStager(fm, client)
		Expect(stager.ReleaseRemote(context.Background(), "node.one", key)).To(Succeed())

		Expect(client.requestCalled).To(BeTrue())
		Expect(client.subject).To(Equal(messaging.SubjectNodeFilesRelease("node.one")))
		var payload fileReleaseRequest
		Expect(json.Unmarshal(client.payload, &payload)).To(Succeed())
		Expect(payload.Key).To(Equal(key))
		exists, err := store.Exists(context.Background(), key)
		Expect(err).NotTo(HaveOccurred())
		Expect(exists).To(BeFalse())
	})
})
