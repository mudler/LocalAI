// SPDX-License-Identifier: MIT
package gormstore

import (
	"context"
	"database/sql"
	"fmt"
	"io"
	"net/url"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"

	"github.com/mudler/LocalAI/core/services/studio/workspace"
	"github.com/mudler/LocalAI/core/services/studio/workspace/blob"
	"github.com/mudler/LocalAI/core/services/studio/workspace/wstest"
)

var sharedSchemaSeq atomic.Int64

// gateBlob exposes explicit ingest and removal barriers without timing guesses.
type gateBlob struct {
	blob.Store
	pool                   *sql.DB
	mu                     sync.Mutex
	uploaded, resume       chan struct{}
	deleting, removeResume chan struct{}
}

func (b *gateBlob) IsFilesystem() bool {
	fs, ok := b.Store.(blob.Filesystem)
	return ok && fs.IsFilesystem()
}

// Stage gates publication, the point where the blob is visible and the
// revision lock is held but the row that refers to it is not written yet.
func (b *gateBlob) Stage(ctx context.Context, r io.Reader, max int64) (blob.Staged, error) {
	st, err := b.Store.(blob.Stager).Stage(ctx, r, max)
	if err != nil {
		return nil, err
	}
	return &gateStaged{Staged: st, b: b}, nil
}

type gateStaged struct {
	blob.Staged
	b *gateBlob
}

func (g *gateStaged) Publish(ctx context.Context) error {
	if err := g.Staged.Publish(ctx); err != nil {
		return err
	}
	g.b.mu.Lock()
	entered, resume := g.b.uploaded, g.b.resume
	g.b.mu.Unlock()
	if entered != nil {
		close(entered)
		<-resume
	}
	return nil
}

func (b *gateBlob) Put(ctx context.Context, r io.Reader, max int64) (string, int64, error) {
	sha, n, err := b.Store.Put(ctx, r, max)
	b.mu.Lock()
	entered, resume := b.uploaded, b.resume
	b.mu.Unlock()
	if err == nil && entered != nil {
		close(entered)
		<-resume
	}
	return sha, n, err
}
func (b *gateBlob) Remove(ctx context.Context, sha string) error {
	b.mu.Lock()
	entered, resume := b.deleting, b.removeResume
	b.mu.Unlock()
	if entered != nil {
		close(entered)
		<-resume
	}
	return b.Store.(blob.Remover).Remove(ctx, sha)
}

func sharedPG() (*Workspace, *Workspace, *gateBlob, *gateBlob) {
	GinkgoHelper()
	rawURL := os.Getenv("LOCALAI_TEST_POSTGRES_URL")
	if rawURL == "" {
		Skip("LOCALAI_TEST_POSTGRES_URL is not set")
	}
	u, err := url.Parse(rawURL)
	Expect(err).NotTo(HaveOccurred())
	schema := fmt.Sprintf("swf_%d_%d", os.Getpid(), sharedSchemaSeq.Add(1))
	admin, err := gorm.Open(postgres.Open(rawURL), &gorm.Config{})
	Expect(err).NotTo(HaveOccurred())
	adminRaw, err := admin.DB()
	Expect(err).NotTo(HaveOccurred())
	Expect(admin.Exec("CREATE SCHEMA " + schema).Error).To(Succeed())
	DeferCleanup(func() {
		Expect(admin.Exec("DROP SCHEMA " + schema + " CASCADE").Error).To(Succeed())
		Expect(adminRaw.Close()).To(Succeed())
	})
	q := u.Query()
	q.Set("search_path", schema)
	u.RawQuery = q.Encode()
	root := GinkgoT().TempDir()
	open := func() (*Workspace, *gateBlob) {
		db, err := gorm.Open(postgres.Open(u.String()), &gorm.Config{})
		Expect(err).NotTo(HaveOccurred())
		pool, err := db.DB()
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(func() { Expect(pool.Close()).To(Succeed()) })
		fs, err := blob.NewFS(root)
		Expect(err).NotTo(HaveOccurred())
		g := &gateBlob{Store: fs, pool: pool}
		s, err := New(db, g, Options{Admin: wstest.Admin, BlobGrace: -1})
		Expect(err).NotTo(HaveOccurred())
		return s, g
	}
	a, ba := open()
	b, bb := open()
	return a, b, ba, bb
}

var _ = Describe("PostgreSQL shared filesystem", func() {
	ctx := context.Background()
	actor := wstest.AdminActor
	It("bounds cleanup waiting on the revision row and preserves blobs", func() {
		_, b, ba, bb := sharedPG()
		sha, _, err := bb.Store.Put(ctx, strings.NewReader("unreferenced"), 0)
		Expect(err).NotTo(HaveOccurred())
		lock, err := ba.pool.BeginTx(ctx, nil)
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(func() { _ = lock.Rollback() })
		_, err = lock.ExecContext(ctx, `UPDATE studio_ws_meta SET value = value WHERE key = 'rev'`)
		Expect(err).NotTo(HaveOccurred())
		cleanup, cancel := context.WithTimeout(ctx, 500*time.Millisecond)
		defer cancel()
		done := make(chan struct{})
		go func() { b.removeBlobs(cleanup, []string{sha}); close(done) }()
		waitSharedLock(ba.pool)
		Eventually(done, 2*time.Second).Should(BeClosed())
		Eventually(func() int { return bb.pool.Stats().InUse }, time.Second).Should(BeZero())
		exists, err := bb.Store.Exists(ctx, sha)
		Expect(err).NotTo(HaveOccurred())
		Expect(exists).To(BeTrue())
	})

	It("honors immediate caller cancellation without deleting blobs", func() {
		_, b, _, bb := sharedPG()
		sha, _, err := bb.Store.Put(ctx, strings.NewReader("canceled cleanup"), 0)
		Expect(err).NotTo(HaveOccurred())
		caller, cancel := context.WithCancel(ctx)
		cancel()
		b.removeBlobs(caller, []string{sha})
		exists, err := bb.Store.Exists(ctx, sha)
		Expect(err).NotTo(HaveOccurred())
		Expect(exists).To(BeTrue())
		Expect(bb.pool.Stats().InUse).To(BeZero())
	})

	It("lets delayed cleanup outlive caller cancellation", func() {
		_, b, _, bb := sharedPG()
		sha, _, err := bb.Store.Put(ctx, strings.NewReader("delayed cleanup"), 0)
		Expect(err).NotTo(HaveOccurred())
		b.grace = 20 * time.Millisecond
		caller, cancel := context.WithCancel(ctx)
		cancel()
		b.removeBlobs(caller, []string{sha})
		Eventually(func() bool {
			exists, err := bb.Store.Exists(ctx, sha)
			Expect(err).NotTo(HaveOccurred())
			return exists
		}, 2*time.Second).Should(BeFalse())
		Eventually(func() int { return bb.pool.Stats().InUse }, time.Second).Should(BeZero())
	})

	It("shares owner writes, authorization and materialization across independent pools", func() {
		a, b, _, _ := sharedPG()
		wstest.MustPut(a, actor, "/owner/file", "shared")
		Expect(wstest.MustRead(b, actor, "/owner/file")).To(Equal("shared"))
		_, _, err := b.Open(ctx, wstest.Bob, "/owner/file")
		Expect(err).To(MatchError(workspace.ErrNotFound))
		v := workspace.View{Actor: actor, Mounts: []workspace.Mount{{NodeID: 1, At: "files", Mode: workspace.ModeWrite}}}
		dir := GinkgoT().TempDir()
		_, err = workspace.MaterializeAt(ctx, b, v, dir)
		Expect(err).NotTo(HaveOccurred())
		got, err := os.ReadFile(dir + "/files/owner/file")
		Expect(err).NotTo(HaveOccurred())
		Expect(string(got)).To(Equal("shared"))
	})
	for _, kind := range []string{"Put", "CommitUpper"} {
		It("keeps a newly referenced blob when a purge overlaps "+kind+" before metadata", func() {
			a, b, ba, _ := sharedPG()
			wstest.MustPut(a, actor, "/old", "same")
			Expect(a.Delete(ctx, actor, "/old")).To(Succeed())
			ba.uploaded = make(chan struct{})
			ba.resume = make(chan struct{})
			done := make(chan error, 1)
			go func() {
				var err error
				if kind == "Put" {
					_, err = a.Put(ctx, actor, "/new", strings.NewReader("same"), workspace.PutOptions{})
				} else {
					v := workspace.View{Actor: actor, Mounts: []workspace.Mount{{NodeID: 1, At: "files", Mode: workspace.ModeWrite}}}
					_, _, err = a.CommitUpper(ctx, v, actor, 0, workspace.FixtureSource{workspace.FixtureFile("files/new", "same")}, workspace.UpperOptions{})
				}
				done <- err
			}()
			<-ba.uploaded
			purged := make(chan error, 1)
			go func() { _, err := b.PurgeAll(ctx, actor, "/", false); purged <- err }()
			waitSharedLock(ba.pool)
			close(ba.resume)
			Expect(<-purged).To(Succeed())
			Expect(<-done).To(Succeed())
			Expect(wstest.MustRead(b, actor, "/new")).To(Equal("same"))
		})
	}
	for _, kind := range []string{"Put", "CommitUpper", "transaction"} {
		It("serializes reference-check/delete with "+kind+" publication", func() {
			a, b, _, bb := sharedPG()
			wstest.MustPut(a, actor, "/old", "same")
			Expect(a.Delete(ctx, actor, "/old")).To(Succeed())
			bb.deleting = make(chan struct{})
			bb.removeResume = make(chan struct{})
			purged := make(chan error, 1)
			go func() { _, err := b.PurgeAll(ctx, actor, "/", false); purged <- err }()
			<-bb.deleting // Reference check finished; deletion has not happened.
			written := make(chan error, 1)
			go func() {
				var err error
				switch kind {
				case "Put":
					_, err = a.Put(ctx, actor, "/new", strings.NewReader("same"), workspace.PutOptions{})
				case "transaction":
					var tx workspace.Tx
					tx, err = a.Begin(ctx, actor, "race")
					if err == nil {
						_, err = tx.Put(ctx, "/new", strings.NewReader("same"), workspace.PutOptions{})
						if err == nil {
							_, err = tx.Commit()
						} else {
							_ = tx.Rollback()
						}
					}
				case "CommitUpper":
					v := workspace.View{Actor: actor, Mounts: []workspace.Mount{{NodeID: 1, At: "files", Mode: workspace.ModeWrite}}}
					_, _, err = a.CommitUpper(ctx, v, actor, 0, workspace.FixtureSource{workspace.FixtureFile("files/new", "same")}, workspace.UpperOptions{})
				}
				written <- err
			}()
			waitSharedLock(bb.pool)
			close(bb.removeResume)
			Expect(<-purged).To(Succeed())
			Expect(<-written).To(Succeed())
			Expect(wstest.MustRead(b, actor, "/new")).To(Equal("same"))
		})
	}
	It("keeps retained journal references and permits purge/re-upload", func() {
		a, b, _, _ := sharedPG()
		wstest.MustPut(a, actor, "/history", "old")
		wstest.MustPut(a, actor, "/history", "current")
		wstest.MustPut(a, actor, "/deleted", "old")
		Expect(a.Delete(ctx, actor, "/deleted")).To(Succeed())
		_, err := b.PurgeAll(ctx, actor, "/", false)
		Expect(err).NotTo(HaveOccurred())
		// Undo needs the retained old_blob even though no live node names it.
		history, err := a.History(ctx, actor, workspace.HistoryQuery{Path: "/history"})
		Expect(err).NotTo(HaveOccurred())
		_, _, err = b.Undo(ctx, actor, history[0].Rev, workspace.UndoOptions{})
		Expect(err).NotTo(HaveOccurred())
		Expect(wstest.MustRead(a, actor, "/history")).To(Equal("old"))
		wstest.MustPut(a, actor, "/deleted", "old")
		Expect(wstest.MustRead(b, actor, "/deleted")).To(Equal("old"))
	})
	It("shares concurrent same and different content publications", func() {
		a, b, _, _ := sharedPG()
		done := make(chan error, 3)
		for i, content := range []string{"same", "same", "different"} {
			s := a
			if i%2 == 1 {
				s = b
			}
			path := fmt.Sprintf("/concurrent/%d", i)
			go func() {
				_, err := s.Put(ctx, actor, path, strings.NewReader(content), workspace.PutOptions{})
				done <- err
			}()
		}
		for i := 0; i < 3; i++ {
			Expect(<-done).To(Succeed())
		}
		for i, content := range []string{"same", "same", "different"} {
			Expect(wstest.MustRead(b, actor, fmt.Sprintf("/concurrent/%d", i))).To(Equal(content))
		}
	})

	It("leaves the revision lock free while a slow upload is read", func() {
		a, b, _, _ := sharedPG()
		request, cancel := context.WithCancel(ctx)
		defer cancel()
		reader := &cancelUpload{ctx: request, entered: make(chan struct{})}
		done := make(chan error, 1)
		go func() { _, err := a.Put(request, actor, "/slow", reader, workspace.PutOptions{}); done <- err }()
		<-reader.entered
		_, err := b.Mkdir(ctx, actor, "/after")
		Expect(err).NotTo(HaveOccurred(), "a slow upload must not block other writers")
		cancel()
		Expect(<-done).To(MatchError(context.Canceled))
		_, err = b.Stat(ctx, actor, "/slow")
		Expect(err).To(MatchError(workspace.ErrNotFound))
	})

	for _, kind := range []string{"Put", "CommitUpper"} {
		It("holds the revision lock from publication to metadata for "+kind, func() {
			a, b, ba, _ := sharedPG()
			ba.uploaded = make(chan struct{})
			ba.resume = make(chan struct{})
			done := make(chan error, 1)
			go func() {
				var err error
				if kind == "Put" {
					_, err = a.Put(ctx, actor, "/new", strings.NewReader("content"), workspace.PutOptions{})
				} else {
					v := workspace.View{Actor: actor, Mounts: []workspace.Mount{{NodeID: 1, At: "files", Mode: workspace.ModeWrite}}}
					_, _, err = a.CommitUpper(ctx, v, actor, 0, workspace.FixtureSource{workspace.FixtureFile("files/new", "content")}, workspace.UpperOptions{})
				}
				done <- err
			}()
			<-ba.uploaded
			writer := make(chan error, 1)
			go func() { _, err := b.Mkdir(ctx, actor, "/blocked"); writer <- err }()
			waitSharedLock(ba.pool)
			Consistently(writer, 50*time.Millisecond).ShouldNot(Receive())
			close(ba.resume)
			Expect(<-done).To(Succeed())
			Expect(<-writer).To(Succeed())
		})
	}

})

// Observe PostgreSQL's actual lock wait rather than inferring it from sleeps.
func waitSharedLock(pool *sql.DB) {
	GinkgoHelper()
	Eventually(func() (bool, error) {
		var blocked bool
		err := pool.QueryRow(`SELECT EXISTS(SELECT 1 FROM pg_stat_activity WHERE datname=current_database() AND wait_event_type='Lock' AND query LIKE '%studio_ws_meta%')`).Scan(&blocked)
		return blocked, err
	}, 5*time.Second, 10*time.Millisecond).Should(BeTrue())
}

type cancelUpload struct {
	ctx     context.Context
	entered chan struct{}
}

func (r *cancelUpload) Read([]byte) (int, error) {
	close(r.entered)
	<-r.ctx.Done()
	return 0, r.ctx.Err()
}
