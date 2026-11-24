package modelartifacts_test

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"sync"
	"syscall"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	hfapi "github.com/mudler/LocalAI/pkg/huggingface-api"
	"github.com/mudler/LocalAI/pkg/modelartifacts"
)

// lockAttempt is one scripted outcome of Locker.TryLock. Attempts beyond the
// script acquire the lock.
type lockAttempt struct {
	locked bool
	err    error
}

type scriptedLocker struct {
	mu        sync.Mutex
	script    []lockAttempt
	attempts  int
	unlocks   int
	onAttempt func(attempt int)
}

func (s *scriptedLocker) TryLock() (bool, error) {
	s.mu.Lock()
	s.attempts++
	attempt := s.attempts
	var outcome lockAttempt
	if attempt <= len(s.script) {
		outcome = s.script[attempt-1]
	} else {
		outcome = lockAttempt{locked: true}
	}
	callback := s.onAttempt
	s.mu.Unlock()
	if callback != nil {
		callback(attempt)
	}
	return outcome.locked, outcome.err
}

func (s *scriptedLocker) Unlock() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.unlocks++
	return nil
}

func (s *scriptedLocker) attemptCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.attempts
}

// singleFileResolver serves one small file so a full Ensure can run end to end
// without the test caring about download mechanics.
func singleFileResolver() *fakeSnapshotResolver {
	weight := []byte("weight-bytes")
	sum := sha256.Sum256(weight)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write(weight)
	}))
	DeferCleanup(server.Close)
	return &fakeSnapshotResolver{snapshot: hfapi.Snapshot{
		Endpoint: "https://huggingface.co", Repo: "owner/repo",
		RequestedRevision: "main", ResolvedRevision: "0123456789abcdef0123456789abcdef01234567",
		Files: []hfapi.SnapshotFile{{
			Path: "model.safetensors", Size: int64(len(weight)),
			LFSOID: hex.EncodeToString(sum[:]), URL: server.URL + "/model",
		}},
	}}
}

var specUnderTest = modelartifacts.Spec{Source: modelartifacts.Source{Type: "huggingface", Repo: "owner/repo"}}

// CIFS/SMB maps STATUS_LOCK_NOT_GRANTED and STATUS_FILE_LOCK_CONFLICT to
// EACCES, so a contended flock(2) on a shared /models never produces the
// EWOULDBLOCK that gofrs/flock recognises. Treating that as a hard failure made
// both replicas abandon materialization and degrade to an in-band download of
// the whole repo inside LoadModel (#10981).
var _ = Describe("artifact lock contention on network filesystems", func() {
	It("waits out CIFS-style EACCES contention and then materializes", func() {
		locker := &scriptedLocker{script: []lockAttempt{
			{err: syscall.EACCES},
			{err: syscall.EACCES},
		}}
		manager := modelartifacts.NewManager(singleFileResolver(),
			modelartifacts.WithLocker(func(string) modelartifacts.Locker { return locker }),
			modelartifacts.WithLockWait(10*time.Second))

		result, err := manager.Ensure(context.Background(), GinkgoT().TempDir(), specUnderTest)
		Expect(err).NotTo(HaveOccurred())
		Expect(result.RelativePath).To(HavePrefix(".artifacts/huggingface/"))
		Expect(locker.attemptCount()).To(Equal(3))
	})

	It("waits out an EWOULDBLOCK contention reported without an error", func() {
		locker := &scriptedLocker{script: []lockAttempt{{}, {}}}
		manager := modelartifacts.NewManager(singleFileResolver(),
			modelartifacts.WithLocker(func(string) modelartifacts.Locker { return locker }),
			modelartifacts.WithLockWait(10*time.Second))

		_, err := manager.Ensure(context.Background(), GinkgoT().TempDir(), specUnderTest)
		Expect(err).NotTo(HaveOccurred())
		Expect(locker.attemptCount()).To(Equal(3))
	})

	It("does not retry a lock error that is not contention", func() {
		locker := &scriptedLocker{script: []lockAttempt{{err: syscall.ENOLCK}}}
		manager := modelartifacts.NewManager(singleFileResolver(),
			modelartifacts.WithLocker(func(string) modelartifacts.Locker { return locker }),
			modelartifacts.WithLockWait(10*time.Second))

		_, err := manager.Ensure(context.Background(), GinkgoT().TempDir(), specUnderTest)
		Expect(err).To(MatchError(syscall.ENOLCK))
		Expect(locker.attemptCount()).To(Equal(1))
	})

	It("adopts the peer's snapshot when contention outlasts the wait window", func() {
		modelsPath := GinkgoT().TempDir()
		resolver := singleFileResolver()
		// The "peer" replica commits the snapshot while we are still waiting,
		// which is the whole point of waiting: the result exists even though we
		// never held the lock ourselves.
		peer := modelartifacts.NewManager(resolver,
			modelartifacts.WithLocker(func(string) modelartifacts.Locker { return &scriptedLocker{} }))
		locker := &scriptedLocker{
			script: []lockAttempt{{err: syscall.EACCES}, {err: syscall.EACCES}, {err: syscall.EACCES}, {err: syscall.EACCES}},
			onAttempt: func(attempt int) {
				if attempt != 2 {
					return
				}
				_, err := peer.Ensure(context.Background(), modelsPath, specUnderTest)
				Expect(err).NotTo(HaveOccurred())
			},
		}
		manager := modelartifacts.NewManager(resolver,
			modelartifacts.WithLocker(func(string) modelartifacts.Locker { return locker }),
			modelartifacts.WithLockWait(300*time.Millisecond))

		result, err := manager.Ensure(context.Background(), modelsPath, specUnderTest)
		Expect(err).NotTo(HaveOccurred())
		Expect(result.CacheHit).To(BeTrue())
	})

	It("reports contention distinctly when the peer never commits", func() {
		locker := &scriptedLocker{script: []lockAttempt{
			{err: syscall.EACCES}, {err: syscall.EACCES}, {err: syscall.EACCES},
			{err: syscall.EACCES}, {err: syscall.EACCES}, {err: syscall.EACCES},
		}}
		manager := modelartifacts.NewManager(singleFileResolver(),
			modelartifacts.WithLocker(func(string) modelartifacts.Locker { return locker }),
			modelartifacts.WithLockWait(200*time.Millisecond))

		_, err := manager.Ensure(context.Background(), GinkgoT().TempDir(), specUnderTest)
		Expect(err).To(MatchError(modelartifacts.ErrLockContended))
	})
})
