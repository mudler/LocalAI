package worker

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/mudler/LocalAI/core/services/messaging"
	"github.com/mudler/LocalAI/core/services/storage"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

type stagingObjectStore struct {
	payload  []byte
	getCalls int
	getErr   error
}

func (*stagingObjectStore) Put(context.Context, string, io.Reader) error { return nil }
func (s *stagingObjectStore) Get(context.Context, string) (io.ReadCloser, error) {
	s.getCalls++
	if s.getErr != nil {
		return nil, s.getErr
	}
	return io.NopCloser(strings.NewReader(string(s.payload))), nil
}
func (s *stagingObjectStore) Head(_ context.Context, key string) (*storage.ObjectMeta, error) {
	return &storage.ObjectMeta{Key: key, Size: int64(len(s.payload))}, nil
}
func (*stagingObjectStore) Exists(context.Context, string) (bool, error) { return true, nil }
func (*stagingObjectStore) Delete(context.Context, string) error         { return nil }
func (*stagingObjectStore) List(context.Context, string) ([]string, error) {
	return nil, nil
}

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
	It("reserves S3 object size before download and releases it with the exact key", func() {
		cacheDir := GinkgoT().TempDir()
		root := filepath.Join(cacheDir, "ephemeral")
		store := &stagingObjectStore{payload: []byte("data")}
		fm, err := storage.NewFileManager(store, cacheDir)
		Expect(err).NotTo(HaveOccurred())
		guard, err := NewEphemeralCapacityGuard([]string{root}, 4, 0)
		Expect(err).NotTo(HaveOccurred())
		key := "ephemeral/audio/request-id/input.wav"

		localPath, err := ensureWorkerFile(context.Background(), fm, guard, key)
		Expect(err).NotTo(HaveOccurred())
		Expect(localPath).To(BeAnExistingFile())
		Expect(store.getCalls).To(Equal(1))
		Expect(guard.Reserve(filepath.Join(root, "audio", "other", "input.wav"), 1)).NotTo(Succeed())

		Expect(releaseEphemeralCachePathWithCapacity(cacheDir, key, localPath, guard)).To(Succeed())
		Expect(guard.Reserve(filepath.Join(root, "audio", "other", "input.wav"), 4)).To(Succeed())
	})

	It("rejects an oversized S3 object before starting its download", func() {
		cacheDir := GinkgoT().TempDir()
		store := &stagingObjectStore{payload: []byte("oversized")}
		fm, err := storage.NewFileManager(store, cacheDir)
		Expect(err).NotTo(HaveOccurred())
		guard, err := NewEphemeralCapacityGuard([]string{filepath.Join(cacheDir, "ephemeral")}, 4, 0)
		Expect(err).NotTo(HaveOccurred())

		_, err = ensureWorkerFile(context.Background(), fm, guard, "ephemeral/audio/request-id/input.wav")
		Expect(err).To(HaveOccurred())
		Expect(store.getCalls).To(BeZero())
	})

	It("rolls back an S3 reservation when the download fails", func() {
		cacheDir := GinkgoT().TempDir()
		root := filepath.Join(cacheDir, "ephemeral")
		store := &stagingObjectStore{payload: []byte("data"), getErr: errors.New("download failed")}
		fm, err := storage.NewFileManager(store, cacheDir)
		Expect(err).NotTo(HaveOccurred())
		guard, err := NewEphemeralCapacityGuard([]string{root}, 4, 0)
		Expect(err).NotTo(HaveOccurred())

		_, err = ensureWorkerFile(context.Background(), fm, guard, "ephemeral/audio/request-id/input.wav")
		Expect(err).To(MatchError(ContainSubstring("download failed")))
		Expect(guard.Reserve(filepath.Join(root, "audio", "replacement", "input.wav"), 4)).To(Succeed())
	})

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
