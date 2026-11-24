package gormstore

import (
	"context"
	"io"
	"path/filepath"
	"strings"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/mudler/LocalAI/core/http/auth"
	"github.com/mudler/LocalAI/core/services/studio/workspace"
	"github.com/mudler/LocalAI/core/services/studio/workspace/blob"
)

// slowBlobs is a filesystem blob store whose upload stays in progress until
// release is closed. It signals started once a Stage or Put is under way.
type slowBlobs struct {
	blob.Store
	started chan struct{}
	release chan struct{}
}

func (b *slowBlobs) wait(ctx context.Context) error {
	select {
	case b.started <- struct{}{}:
	default:
	}
	select {
	case <-b.release:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (b *slowBlobs) Put(ctx context.Context, r io.Reader, max int64) (string, int64, error) {
	if err := b.wait(ctx); err != nil {
		return "", 0, err
	}
	return b.Store.Put(ctx, r, max)
}

func (b *slowBlobs) Stage(ctx context.Context, r io.Reader, max int64) (blob.Staged, error) {
	if err := b.wait(ctx); err != nil {
		return nil, err
	}
	return b.Store.(blob.Stager).Stage(ctx, r, max)
}

var _ = Describe("a slow upload on a shared filesystem", func() {
	var (
		s    *Workspace
		slow *slowBlobs
	)
	// The admin may write anywhere; the lock is global, so who writes is moot.
	alice := workspace.Actor{Principal: "admin", ChatID: "1"}
	bob := workspace.Actor{Principal: "admin", ChatID: "2"}

	BeforeEach(func() {
		db, err := auth.OpenSQLiteFile(filepath.Join(GinkgoT().TempDir(), "ws.db"))
		Expect(err).ToNot(HaveOccurred())
		DeferCleanup(func() {
			if raw, err := db.DB(); err == nil {
				_ = raw.Close()
			}
		})
		fs, err := blob.NewFS(filepath.Join(GinkgoT().TempDir(), "blobs"))
		Expect(err).ToNot(HaveOccurred())
		slow = &slowBlobs{Store: fs, started: make(chan struct{}, 1), release: make(chan struct{})}
		s, err = New(db, slow, Options{Admin: "admin"})
		Expect(err).ToNot(HaveOccurred())
		// SQLite stands in for PostgreSQL here; the lock order is the same.
		s.sharedFilesystem = true
		// Never leave the upload blocked, even when a spec fails.
		DeferCleanup(func() {
			select {
			case <-slow.release:
			default:
				close(slow.release)
			}
		})
	})

	It("does not hold the revision lock while Put reads the upload", func() {
		done := make(chan error, 1)
		go func() {
			_, err := s.Put(context.Background(), alice, "/big.bin", strings.NewReader("slow"), workspace.PutOptions{})
			done <- err
		}()
		Eventually(slow.started).Should(Receive())

		other := make(chan error, 1)
		go func() {
			_, err := s.Mkdir(context.Background(), bob, "/mine")
			other <- err
		}()
		Eventually(other, 3*time.Second).Should(Receive(BeNil()), "another user's write waits for a slow upload")

		close(slow.release)
		Eventually(done, 5*time.Second).Should(Receive(BeNil()))
	})
})
