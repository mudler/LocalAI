package worker

import (
	"encoding/json"
	"os"
	"path/filepath"
	"time"

	"github.com/mudler/LocalAI/core/services/messaging"
	"github.com/mudler/LocalAI/core/services/storage"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

type releaseSubscription struct{}

func (releaseSubscription) Unsubscribe() error { return nil }

type releaseMessagingClient struct {
	subject string
	handler func([]byte, func([]byte))
}

func (m *releaseMessagingClient) Publish(string, any) error { return nil }
func (m *releaseMessagingClient) Subscribe(string, func([]byte)) (messaging.Subscription, error) {
	return releaseSubscription{}, nil
}
func (m *releaseMessagingClient) QueueSubscribe(string, string, func([]byte)) (messaging.Subscription, error) {
	return releaseSubscription{}, nil
}
func (m *releaseMessagingClient) QueueSubscribeReply(string, string, func([]byte, func([]byte))) (messaging.Subscription, error) {
	return releaseSubscription{}, nil
}
func (m *releaseMessagingClient) SubscribeReply(subject string, handler func([]byte, func([]byte))) (messaging.Subscription, error) {
	m.subject = subject
	m.handler = handler
	return releaseSubscription{}, nil
}
func (m *releaseMessagingClient) Request(string, []byte, time.Duration) ([]byte, error) {
	return nil, nil
}
func (m *releaseMessagingClient) IsConnected() bool { return true }
func (m *releaseMessagingClient) Close()            {}

var _ = Describe("Worker exact-key staging release", func() {
	It("removes only the exact cache file and upload sidecars", func() {
		cacheDir := GinkgoT().TempDir()
		categoryDir := filepath.Join(cacheDir, "ephemeral", "request-id", "audio")
		Expect(os.MkdirAll(categoryDir, 0750)).To(Succeed())
		target := filepath.Join(categoryDir, "input.wav")
		sibling := filepath.Join(categoryDir, "keep.wav")
		for _, path := range []string{target, target + ".sha256", target + ".sha256.target", sibling} {
			Expect(os.WriteFile(path, []byte("data"), 0640)).To(Succeed())
		}

		Expect(releaseEphemeralCacheKey(cacheDir, "ephemeral/request-id/audio/input.wav")).To(Succeed())
		Expect(target).NotTo(BeAnExistingFile())
		Expect(target + ".sha256").NotTo(BeAnExistingFile())
		Expect(target + ".sha256.target").NotTo(BeAnExistingFile())
		Expect(sibling).To(BeAnExistingFile())
		Expect(categoryDir).To(BeADirectory())
	})

	It("succeeds for a missing file and prunes empty category and request directories", func() {
		cacheDir := GinkgoT().TempDir()
		categoryDir := filepath.Join(cacheDir, "ephemeral", "request-id", "audio")
		Expect(os.MkdirAll(categoryDir, 0750)).To(Succeed())

		for range 2 {
			Expect(releaseEphemeralCacheKey(cacheDir, "ephemeral/request-id/audio/missing.wav")).To(Succeed())
		}
		Expect(categoryDir).NotTo(BeADirectory())
		Expect(filepath.Dir(categoryDir)).NotTo(BeADirectory())
		Expect(filepath.Join(cacheDir, "ephemeral")).To(BeADirectory())
	})

	It("rejects traversal and symlink escapes", func() {
		cacheDir := GinkgoT().TempDir()
		outsideDir := GinkgoT().TempDir()
		outsidePath := filepath.Join(outsideDir, "input.wav")
		Expect(os.WriteFile(outsidePath, []byte("keep"), 0640)).To(Succeed())
		requestDir := filepath.Join(cacheDir, "ephemeral", "request-id")
		Expect(os.MkdirAll(requestDir, 0750)).To(Succeed())
		Expect(os.Symlink(outsideDir, filepath.Join(requestDir, "audio"))).To(Succeed())

		for _, key := range []string{
			"models/model.gguf",
			"ephemeral/../models/model.gguf",
			"ephemeral/request-id/audio/../../model.gguf",
			"ephemeral/request-id/audio/input.wav",
		} {
			Expect(releaseEphemeralCacheKey(cacheDir, key)).NotTo(Succeed(), key)
		}
		Expect(outsidePath).To(BeAnExistingFile())
	})

	It("registers an exact release handler", func() {
		cacheDir := GinkgoT().TempDir()
		path := filepath.Join(cacheDir, "ephemeral", "request-id", "audio", "input.wav")
		Expect(os.MkdirAll(filepath.Dir(path), 0750)).To(Succeed())
		Expect(os.WriteFile(path, []byte("data"), 0640)).To(Succeed())
		fm, err := storage.NewFileManager(nil, cacheDir)
		Expect(err).NotTo(HaveOccurred())
		client := &releaseMessagingClient{}

		Expect(subscribeFileRelease(client, "node.one", fm, cacheDir)).To(Succeed())
		Expect(client.subject).To(Equal(messaging.SubjectNodeFilesRelease("node.one")))
		request, err := json.Marshal(map[string]string{"key": "ephemeral/request-id/audio/input.wav"})
		Expect(err).NotTo(HaveOccurred())
		var response []byte
		client.handler(request, func(data []byte) { response = append([]byte(nil), data...) })

		var reply map[string]string
		Expect(json.Unmarshal(response, &reply)).To(Succeed())
		Expect(reply["error"]).To(BeEmpty())
		Expect(path).NotTo(BeAnExistingFile())
	})

	It("returns validation errors through the release handler", func() {
		cacheDir := GinkgoT().TempDir()
		fm, err := storage.NewFileManager(nil, cacheDir)
		Expect(err).NotTo(HaveOccurred())
		client := &releaseMessagingClient{}
		Expect(subscribeFileRelease(client, "node-1", fm, cacheDir)).To(Succeed())

		request, err := json.Marshal(map[string]string{"key": "models/model.gguf"})
		Expect(err).NotTo(HaveOccurred())
		var response []byte
		client.handler(request, func(data []byte) { response = append([]byte(nil), data...) })

		var reply map[string]string
		Expect(json.Unmarshal(response, &reply)).To(Succeed())
		Expect(reply["error"]).NotTo(BeEmpty())
	})
})
