package worker

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/mudler/LocalAI/core/services/messaging"
	"github.com/mudler/LocalAI/core/services/nodes"
	"github.com/mudler/LocalAI/core/services/storage"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

type stagingObjectStore struct {
	payload  []byte
	getCalls int
	getErr   error
}

type disappearingStagingCapacity struct{}

func (*disappearingStagingCapacity) Reserve(string, int64) error { return nil }
func (*disappearingStagingCapacity) Commit(string) error         { return nil }
func (*disappearingStagingCapacity) Release(string) error        { return nil }
func (*disappearingStagingCapacity) Claim(path string) error {
	if err := os.Remove(path); err != nil {
		return err
	}
	return fmt.Errorf("claim raced recovery: %w", os.ErrNotExist)
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
	It("protects a startup-accounted HTTP cache hit through authenticated repeated probes", func() {
		stagingDir := GinkgoT().TempDir()
		root := filepath.Join(stagingDir, "ephemeral")
		key := "ephemeral/audio/request-id/input.wav"
		remotePath := filepath.Join(stagingDir, filepath.FromSlash(key))
		content := []byte("data")
		Expect(os.MkdirAll(filepath.Dir(remotePath), 0o750)).To(Succeed())
		Expect(os.WriteFile(remotePath, content, 0o600)).To(Succeed())
		hash := sha256.Sum256(content)
		Expect(os.WriteFile(remotePath+".sha256", []byte(fmt.Sprintf("%x", hash)), 0o600)).To(Succeed())
		old := time.Now().Add(-2 * time.Hour)
		for _, path := range []string{remotePath, remotePath + ".sha256", filepath.Dir(remotePath)} {
			Expect(os.Chtimes(path, old, old)).To(Succeed())
		}
		guard, err := NewEphemeralCapacityGuard([]string{root}, int64(len(content)+sha256.Size*2), 0)
		Expect(err).NotTo(HaveOccurred())

		listener, err := net.Listen("tcp", "127.0.0.1:0")
		Expect(err).NotTo(HaveOccurred())
		addr := listener.Addr().String()
		Expect(listener.Close()).To(Succeed())
		server, err := nodes.StartFileTransferServerWithCapacity(addr, stagingDir, GinkgoT().TempDir(), GinkgoT().TempDir(), "secret", 0, nil, guard)
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(nodes.ShutdownFileTransferServer, server)

		localPath := filepath.Join(GinkgoT().TempDir(), "input.wav")
		Expect(os.WriteFile(localPath, content, 0o600)).To(Succeed())
		stager := nodes.NewHTTPFileStager(func(string) (string, error) { return addr, nil }, "secret")
		for range 2 {
			path, ensureErr := stager.EnsureRemote(context.Background(), "worker", localPath, key)
			Expect(ensureErr).NotTo(HaveOccurred())
			Expect(path).To(Equal(remotePath))
		}

		CleanEphemeralRoots([]string{root}, time.Hour, guard)
		Expect(remotePath).To(BeAnExistingFile())

		Expect(stager.ReleaseRemote(context.Background(), "worker", key)).To(Succeed())
		CleanEphemeralRoots([]string{root}, time.Hour, guard)
		Expect(remotePath).NotTo(BeAnExistingFile())
	})

	It("claims a startup-scanned cache hit against stale recovery until release", func() {
		cacheDir := GinkgoT().TempDir()
		root := filepath.Join(cacheDir, "ephemeral")
		key := "ephemeral/audio/request-id/input.wav"
		cachePath := filepath.Join(cacheDir, filepath.FromSlash(key))
		Expect(os.MkdirAll(filepath.Dir(cachePath), 0o750)).To(Succeed())
		Expect(os.WriteFile(cachePath, []byte("data"), 0o600)).To(Succeed())
		old := time.Now().Add(-2 * time.Hour)
		Expect(os.Chtimes(cachePath, old, old)).To(Succeed())
		Expect(os.Chtimes(filepath.Dir(cachePath), old, old)).To(Succeed())

		guard, err := NewEphemeralCapacityGuard([]string{root}, 4, 0)
		Expect(err).NotTo(HaveOccurred())
		store := &stagingObjectStore{payload: []byte("unused")}
		fm, err := storage.NewFileManager(store, cacheDir)
		Expect(err).NotTo(HaveOccurred())

		localPath, err := ensureWorkerFile(context.Background(), fm, guard, key)
		Expect(err).NotTo(HaveOccurred())
		Expect(localPath).To(Equal(cachePath))
		Expect(store.getCalls).To(BeZero())
		CleanEphemeralRoots([]string{root}, time.Hour, guard)
		Expect(cachePath).To(BeAnExistingFile())

		Expect(guard.Release(cachePath)).To(Succeed())
		CleanEphemeralRoots([]string{root}, time.Hour, guard)
		Expect(cachePath).NotTo(BeAnExistingFile())
	})

	It("downloads again when a cache file disappears while being claimed", func() {
		cacheDir := GinkgoT().TempDir()
		key := "ephemeral/audio/request-id/input.wav"
		cachePath := filepath.Join(cacheDir, filepath.FromSlash(key))
		Expect(os.MkdirAll(filepath.Dir(cachePath), 0o750)).To(Succeed())
		Expect(os.WriteFile(cachePath, []byte("stale"), 0o600)).To(Succeed())
		store := &stagingObjectStore{payload: []byte("fresh")}
		fm, err := storage.NewFileManager(store, cacheDir)
		Expect(err).NotTo(HaveOccurred())

		localPath, err := ensureWorkerFileWithCapacity(context.Background(), fm, &disappearingStagingCapacity{}, key)

		Expect(err).NotTo(HaveOccurred())
		Expect(localPath).To(Equal(cachePath))
		Expect(os.ReadFile(localPath)).To(Equal([]byte("fresh")))
		Expect(store.getCalls).To(Equal(1))
	})

	It("makes repeated cache-hit claims idempotent", func() {
		cacheDir := GinkgoT().TempDir()
		root := filepath.Join(cacheDir, "ephemeral")
		key := "ephemeral/audio/request-id/input.wav"
		cachePath := filepath.Join(cacheDir, filepath.FromSlash(key))
		Expect(os.MkdirAll(filepath.Dir(cachePath), 0o750)).To(Succeed())
		Expect(os.WriteFile(cachePath, []byte("data"), 0o600)).To(Succeed())
		guard, err := NewEphemeralCapacityGuard([]string{root}, 4, 0)
		Expect(err).NotTo(HaveOccurred())
		fm, err := storage.NewFileManager(&stagingObjectStore{}, cacheDir)
		Expect(err).NotTo(HaveOccurred())

		for range 2 {
			localPath, ensureErr := ensureWorkerFile(context.Background(), fm, guard, key)
			Expect(ensureErr).NotTo(HaveOccurred())
			Expect(localPath).To(Equal(cachePath))
		}
		err = guard.Reserve(filepath.Join(root, "other", "request-id", "input.wav"), 1)
		var capacityErr *EphemeralCapacityError
		Expect(errors.As(err, &capacityErr)).To(BeTrue())
		Expect(capacityErr.UsageBytes).To(Equal(int64(4)))
	})

	It("capacity-checks growth of a startup-scanned cache file", func() {
		cacheDir := GinkgoT().TempDir()
		root := filepath.Join(cacheDir, "ephemeral")
		key := "ephemeral/audio/request-id/input.wav"
		cachePath := filepath.Join(cacheDir, filepath.FromSlash(key))
		Expect(os.MkdirAll(filepath.Dir(cachePath), 0o750)).To(Succeed())
		Expect(os.WriteFile(cachePath, []byte("12"), 0o600)).To(Succeed())
		guard, err := NewEphemeralCapacityGuard([]string{root}, 4, 0)
		Expect(err).NotTo(HaveOccurred())
		Expect(os.WriteFile(cachePath, []byte("12345"), 0o600)).To(Succeed())
		fm, err := storage.NewFileManager(&stagingObjectStore{}, cacheDir)
		Expect(err).NotTo(HaveOccurred())

		_, err = ensureWorkerFile(context.Background(), fm, guard, key)
		var capacityErr *EphemeralCapacityError
		Expect(errors.As(err, &capacityErr)).To(BeTrue())
		Expect(capacityErr.RequestedBytes).To(Equal(int64(3)))
		Expect(capacityErr.UsageBytes).To(Equal(int64(2)))
		Expect(guard.HasActiveReservation(cachePath)).To(BeFalse())
	})

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

	It("rejects symlinked files and sidecars without deleting their targets", func() {
		for _, linkedName := range []string{"input.wav", "input.wav.sha256", "input.wav.sha256.target"} {
			cacheDir := GinkgoT().TempDir()
			categoryDir := filepath.Join(cacheDir, "ephemeral", "request-id", "audio")
			Expect(os.MkdirAll(categoryDir, 0750)).To(Succeed())
			target := filepath.Join(categoryDir, "input.wav")
			if linkedName != "input.wav" {
				Expect(os.WriteFile(target, []byte("input"), 0640)).To(Succeed())
			}
			preserved := filepath.Join(cacheDir, "ephemeral", "preserved-"+linkedName)
			Expect(os.WriteFile(preserved, []byte("keep"), 0640)).To(Succeed())
			Expect(os.Symlink(preserved, filepath.Join(categoryDir, linkedName))).To(Succeed())

			Expect(releaseEphemeralCacheKey(cacheDir, "ephemeral/request-id/audio/input.wav")).NotTo(Succeed(), linkedName)
			Expect(preserved).To(BeAnExistingFile(), linkedName)
		}
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

	It("releases a request batch through one worker message", func() {
		cacheDir := GinkgoT().TempDir()
		keys := []string{
			"ephemeral/audio/request-id/input.wav",
			"ephemeral/images/request-id/frame.jpg",
		}
		for _, key := range keys {
			path := filepath.Join(cacheDir, filepath.FromSlash(key))
			Expect(os.MkdirAll(filepath.Dir(path), 0750)).To(Succeed())
			Expect(os.WriteFile(path, []byte("data"), 0640)).To(Succeed())
		}
		fm, err := storage.NewFileManager(nil, cacheDir)
		Expect(err).NotTo(HaveOccurred())
		client := &releaseMessagingClient{}
		Expect(subscribeFileRelease(client, "node.one", fm, cacheDir)).To(Succeed())
		request, err := json.Marshal(map[string]any{"request_id": "request-id"})
		Expect(err).NotTo(HaveOccurred())
		var response []byte

		client.handler(request, func(data []byte) { response = append([]byte(nil), data...) })

		var reply map[string]string
		Expect(json.Unmarshal(response, &reply)).To(Succeed())
		Expect(reply["error"]).To(BeEmpty())
		for _, key := range keys {
			Expect(filepath.Join(cacheDir, filepath.FromSlash(key))).NotTo(BeAnExistingFile())
		}
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
