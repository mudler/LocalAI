// SPDX-License-Identifier: MIT
package outputstore

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	"errors"
	"github.com/mudler/LocalAI/core/services/music"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
	"strings"
)

var _ = Describe("configuration", func() {
	It("rejects missing durable dependencies", func() {
		_, err := New(Config{})
		Expect(err).To(HaveOccurred())
	})
	It("uses an actual file SQLite database and private filesystem", func() {
		dir, err := os.MkdirTemp("", "music-db-")
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(os.RemoveAll, dir)
		db, err := gorm.Open(sqlite.Open(filepath.Join(dir, "music.db")+"?_busy_timeout=5000&_txlock=immediate"), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
		Expect(err).NotTo(HaveOccurred())
		sqlDB, err := db.DB()
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(sqlDB.Close)
		objects, err := NewPrivateFilesystem(filepath.Join(dir, "objects"))
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(objects.Close)
		s, err := New(Config{DB: db, Objects: objects, StagingRoot: filepath.Join(dir, "staging"), Clock: time.Now, ValidateWAV: func(context.Context, io.ReadSeeker, int64) error { return nil }})
		Expect(err).NotTo(HaveOccurred())
		Expect(s).NotTo(BeNil())
		DeferCleanup(s.Close)
	})
})

var _ = Describe("durable lifecycle", func() {
	var s *Store
	var cfg Config
	var db *gorm.DB
	var now time.Time
	var dir string
	var objects *faultStore
	var ctx context.Context
	BeforeEach(func() {
		ctx = context.Background()
		now = time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC)
		var e error
		dir, e = os.MkdirTemp("", "music-lifecycle-")
		Expect(e).NotTo(HaveOccurred())
		DeferCleanup(os.RemoveAll, dir)
		db, e = gorm.Open(sqlite.Open(filepath.Join(dir, "music.db")+"?_busy_timeout=5000&_txlock=immediate"), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
		Expect(e).NotTo(HaveOccurred())
		sqlDB, e := db.DB()
		Expect(e).NotTo(HaveOccurred())
		DeferCleanup(sqlDB.Close)
		fs, e := NewPrivateFilesystem(filepath.Join(dir, "objects"))
		Expect(e).NotTo(HaveOccurred())
		DeferCleanup(fs.Close)
		objects = &faultStore{PrivateFilesystem: fs}
		cfg = Config{DB: db, Objects: objects, StagingRoot: filepath.Join(dir, "staging"), Clock: func() time.Time { return now }, ValidateWAV: func(_ context.Context, r io.ReadSeeker, n int64) error {
			b, e := io.ReadAll(r)
			if e != nil {
				return e
			}
			if string(b) != "trusted-test-wav" || int64(len(b)) != n {
				return ErrDenied
			}
			return nil
		}}
		s, e = New(cfg)
		Expect(e).NotTo(HaveOccurred())
		DeferCleanup(func() { Expect(s.Close()).To(Succeed()) })
	})
	register := func() {
		Expect(s.RegisterRequest(ctx, RequestMetadata{ID: "request", Principal: "alice", TranscriptionModel: "transcriber", GenerationModel: "generator", Deadline: now.Add(900 * time.Second)})).To(Succeed())
	}
	candidate := func() music.Candidate {
		c, e := s.ImportCandidate(ctx, "request", strings.NewReader("trusted-test-wav"))
		Expect(e).NotTo(HaveOccurred())
		_, e = s.FetchValidate(ctx, "request", c)
		Expect(e).NotTo(HaveOccurred())
		return c
	}
	prepare := func() music.PreparedOutput {
		p, e := s.Prepare(ctx, "request", candidate())
		Expect(e).NotTo(HaveOccurred())
		return p
	}
	ack := func(t music.CleanupTask) {
		Expect(t.AttemptError).NotTo(HaveOccurred())
		Expect(t.Released).To(Receive(BeNil()))
	}
	It("commits privately, survives restart, and expires immediately", func() {
		register()
		p := prepare()
		id, e := s.Commit(ctx, "request", p)
		Expect(e).NotTo(HaveOccurred())
		Expect(id).To(HaveLen(64))
		m, e := s.Lookup(ctx, id)
		Expect(e).NotTo(HaveOccurred())
		Expect(m.Principal).To(Equal("alice"))
		Expect(m.ContentType).To(Equal("audio/wav"))
		Expect(m.ExpiresAt).To(Equal(now.Add(24 * time.Hour)))
		other, e := New(cfg)
		Expect(e).NotTo(HaveOccurred())
		defer other.Close()
		m2, e := other.Lookup(ctx, id)
		Expect(e).NotTo(HaveOccurred())
		Expect(m2).To(Equal(m))
		r, e := other.Open(ctx, m2)
		Expect(e).NotTo(HaveOccurred())
		bytes, e := io.ReadAll(r)
		Expect(e).NotTo(HaveOccurred())
		Expect(r.Close()).To(Succeed())
		Expect(string(bytes)).To(Equal("trusted-test-wav"))
		forged := m
		forged.Principal = "bob"
		_, e = other.Open(ctx, forged)
		Expect(e).To(HaveOccurred())
		ack(s.Revoke(ctx, "request"))
		_, e = s.Lookup(ctx, id)
		Expect(e).NotTo(HaveOccurred())
		now = now.Add(24 * time.Hour)
		_, e = other.Lookup(ctx, id)
		Expect(e).To(HaveOccurred())
		_, e = other.Open(ctx, m)
		Expect(e).To(HaveOccurred())
		Expect(other.Reconcile(ctx)).To(Succeed())
		exists, e := objects.Exists(ctx, m.ObjectKey)
		Expect(e).NotTo(HaveOccurred())
		Expect(exists).To(BeFalse())
	})
	It("persists revoke before registration and rejects prepare after restart", func() {
		ack(s.Revoke(ctx, "request"))
		Expect(s.RegisterRequest(ctx, RequestMetadata{ID: "request", Principal: "alice", Deadline: now.Add(time.Minute)})).NotTo(Succeed())
		other, e := New(cfg)
		Expect(e).NotTo(HaveOccurred())
		defer other.Close()
		_, e = other.ImportCandidate(ctx, "request", strings.NewReader("trusted-test-wav"))
		Expect(e).To(HaveOccurred())
	})
	It("revokes after preparation and never commits a URL", func() {
		register()
		p := prepare()
		ack(s.Revoke(ctx, "request"))
		id, e := s.Commit(ctx, "request", p)
		Expect(e).To(HaveOccurred())
		Expect(id).To(BeEmpty())
		var count int64
		Expect(db.Model(&Manifest{}).Count(&count).Error).To(Succeed())
		Expect(count).To(BeZero())
	})
	It("rejects foreign exact-issuer tokens even with identical key strings", func() {
		register()
		c := candidate()
		key, ok := s.Issuer().CandidateKey(c)
		Expect(ok).To(BeTrue())
		forged := music.NewCapabilityIssuer().Candidate("request", music.GenerationStage, key)
		_, e := s.FetchValidate(ctx, "request", forged)
		Expect(e).To(HaveOccurred())
		_, e = s.Prepare(ctx, "foreign", c)
		Expect(e).To(HaveOccurred())
		task := s.CleanArtifact(ctx, "request", forged, music.PreparedOutput{})
		Expect(task.AttemptError).To(HaveOccurred())
		Expect(task.Released).NotTo(Receive())
	})
	It("retains partial upload intent through restart and retries failed deletion without ACK", func() {
		register()
		c := candidate()
		objects.failPut = true
		_, e := s.Prepare(ctx, "request", c)
		Expect(e).To(HaveOccurred())
		var rows []artifactRow
		Expect(db.Where("kind = ?", "object").Find(&rows).Error).To(Succeed())
		Expect(rows).To(HaveLen(1))
		Expect(rows[0].State).To(Equal("pending"))
		exists, e := objects.Exists(ctx, rows[0].ObjectKey)
		Expect(e).NotTo(HaveOccurred())
		Expect(exists).To(BeTrue())
		objects.failDelete = true
		task := s.Revoke(ctx, "request")
		Expect(task.AttemptError).To(HaveOccurred())
		Expect(task.Released).NotTo(Receive())
		objects.failDelete = false
		Expect(s.Reconcile(ctx)).To(Succeed())
		Expect(task.Released).To(Receive(BeNil()))
		// Simulate a remote PUT that completes after an earlier crash/delete.
		objects.failPut = false
		Expect(objects.Put(ctx, rows[0].ObjectKey, strings.NewReader("late"))).To(Succeed())
		other, e := New(cfg)
		Expect(e).NotTo(HaveOccurred())
		defer other.Close()
		Expect(other.Reconcile(ctx)).To(Succeed())
		exists, e = objects.Exists(ctx, rows[0].ObjectKey)
		Expect(e).NotTo(HaveOccurred())
		Expect(exists).To(BeFalse())
	})
	It("does not publish when the manifest transaction fails", func() {
		register()
		p := prepare()
		Expect(db.Exec("CREATE TRIGGER deny_manifest BEFORE INSERT ON music_output_manifests BEGIN SELECT RAISE(ABORT, 'forced manifest failure'); END").Error).To(Succeed())
		id, e := s.Commit(ctx, "request", p)
		Expect(e).To(HaveOccurred())
		Expect(id).To(BeEmpty())
		var r requestRow
		Expect(db.First(&r, "id = ?", "request").Error).To(Succeed())
		Expect(r.State).To(Equal("active"))
		ack(s.Revoke(ctx, "request"))
	})
	It("rejects symlink and hardlink replacement before validation", func() {
		register()
		c, e := s.ImportCandidate(ctx, "request", strings.NewReader("trusted-test-wav"))
		Expect(e).NotTo(HaveOccurred())
		reg, _ := s.tokenCandidate("request", c)
		leaf := filepath.Join(cfg.StagingRoot, reg.id, "bytes")
		outside := filepath.Join(dir, "outside")
		Expect(os.WriteFile(outside, []byte("trusted-test-wav"), 0600)).To(Succeed())
		Expect(os.Remove(leaf)).To(Succeed())
		Expect(os.Symlink(outside, leaf)).To(Succeed())
		_, e = s.FetchValidate(ctx, "request", c)
		Expect(e).To(HaveOccurred())
		Expect(os.Remove(leaf)).To(Succeed())
		Expect(os.Link(outside, leaf)).To(Succeed())
		_, e = s.FetchValidate(ctx, "request", c)
		Expect(e).To(HaveOccurred())
		ack(s.Revoke(ctx, "request"))
	})
	It("cleans temporary bytes after restart at deadline plus one hour", func() {
		register()
		_, e := s.ImportInput(ctx, "request", strings.NewReader("recording"))
		Expect(e).NotTo(HaveOccurred())
		var a artifactRow
		Expect(db.First(&a, "kind = ?", "input").Error).To(Succeed())
		Expect(a.ExpiresAt).To(Equal(now.Add(900*time.Second + time.Hour)))
		now = a.ExpiresAt
		other, e := New(cfg)
		Expect(e).NotTo(HaveOccurred())
		defer other.Close()
		Expect(other.Reconcile(ctx)).To(Succeed())
		_, e = os.Stat(filepath.Join(cfg.StagingRoot, a.Leaf))
		Expect(os.IsNotExist(e)).To(BeTrue())
	})
	It("serializes an in-flight PUT against a second instance's revoke", func() {
		register()
		c := candidate()
		other, e := New(cfg)
		Expect(e).NotTo(HaveOccurred())
		defer other.Close()
		objects.enter = make(chan struct{})
		objects.resume = make(chan struct{})
		type preparedResult struct {
			p   music.PreparedOutput
			err error
		}
		prepared := make(chan preparedResult, 1)
		go func() { p, err := s.Prepare(ctx, "request", c); prepared <- preparedResult{p, err} }()
		Eventually(objects.enter).Should(BeClosed())
		revoked := make(chan music.CleanupTask, 1)
		go func() { revoked <- other.Revoke(ctx, "request") }()
		Consistently(revoked, 50*time.Millisecond).ShouldNot(Receive())
		close(objects.resume)
		var result preparedResult
		Eventually(prepared).Should(Receive(&result))
		Expect(result.err).NotTo(HaveOccurred())
		var task music.CleanupTask
		Eventually(revoked).Should(Receive(&task))
		ack(task)
		id, e := s.Commit(ctx, "request", result.p)
		Expect(e).To(HaveOccurred())
		Expect(id).To(BeEmpty())
		var row artifactRow
		Expect(db.First(&row, "kind = ?", "object").Error).To(Succeed())
		Expect(row.Generation).To(BeNumerically(">", 1))
		exists, e := objects.Exists(ctx, row.ObjectKey)
		Expect(e).NotTo(HaveOccurred())
		Expect(exists).To(BeFalse())
	})
	It("rejects corrupt bytes without a fetch and rejects changed private object symlinks", func() {
		register()
		c, e := s.ImportCandidate(ctx, "request", strings.NewReader("not wav"))
		Expect(e).NotTo(HaveOccurred())
		_, e = s.FetchValidate(ctx, "request", c)
		Expect(e).To(HaveOccurred())
		p := prepare()
		id, e := s.Commit(ctx, "request", p)
		Expect(e).NotTo(HaveOccurred())
		m, e := s.Lookup(ctx, id)
		Expect(e).NotTo(HaveOccurred())
		leaf := filepath.Join(dir, "objects", strings.TrimPrefix(m.ObjectKey, "music/"))
		Expect(os.Remove(leaf)).To(Succeed())
		outside := filepath.Join(dir, "object-outside")
		Expect(os.WriteFile(outside, []byte("trusted-test-wav"), 0600)).To(Succeed())
		Expect(os.Symlink(outside, leaf)).To(Succeed())
		_, e = s.Open(ctx, m)
		Expect(e).To(HaveOccurred())
	})
	It("retains revoke retry ownership when the tombstone transaction fails", func() {
		register()
		Expect(db.Exec("CREATE TRIGGER deny_revoke BEFORE UPDATE OF state ON music_output_requests WHEN NEW.state = 'revoked' BEGIN SELECT RAISE(ABORT, 'forced revoke failure'); END").Error).To(Succeed())
		task := s.Revoke(ctx, "request")
		Expect(task.AttemptError).To(HaveOccurred())
		Expect(task.Released).NotTo(Receive())
		Expect(db.Exec("DROP TRIGGER deny_revoke").Error).To(Succeed())
		Expect(s.Reconcile(ctx)).To(Succeed())
		Expect(task.Released).To(Receive(BeNil()))
		var row requestRow
		Expect(db.First(&row, "id = ?", "request").Error).To(Succeed())
		Expect(row.State).To(Equal("revoked"))
	})
	It("does not ACK another staging root's missing local bytes", func() {
		register()
		c := candidate()
		reg, _ := s.tokenCandidate("request", c)
		foreign := cfg
		foreign.StagingRoot = filepath.Join(dir, "other-staging")
		other, e := New(foreign)
		Expect(e).NotTo(HaveOccurred())
		defer other.Close()
		task := other.Revoke(ctx, "request")
		Expect(task.AttemptError).To(HaveOccurred())
		Expect(task.Released).NotTo(Receive())
		_, e = os.Stat(filepath.Join(cfg.StagingRoot, reg.id, "bytes"))
		Expect(e).NotTo(HaveOccurred())
		Expect(s.Reconcile(ctx)).To(Succeed())
		Expect(other.Reconcile(ctx)).To(Succeed())
		Expect(task.Released).To(Receive(BeNil()))
	})
	It("returns registration indexes to baseline after cleanup and transfer", func() {
		register()
		input, e := s.ImportInput(ctx, "request", strings.NewReader("recording"))
		Expect(e).NotTo(HaveOccurred())
		ack(s.CleanInput(ctx, "request", input))
		c := candidate()
		p, e := s.Prepare(ctx, "request", c)
		Expect(e).NotTo(HaveOccurred())
		_, e = s.Commit(ctx, "request", p)
		Expect(e).NotTo(HaveOccurred())
		Expect(s.inputs).To(BeEmpty())
		Expect(s.candidates).To(BeEmpty())
		Expect(s.prepared).To(BeEmpty())
	})
	It("recovers durable publication after closing and reopening the SQLite connection", func() {
		register()
		id, e := s.Commit(ctx, "request", prepare())
		Expect(e).NotTo(HaveOccurred())
		sqlDB, e := db.DB()
		Expect(e).NotTo(HaveOccurred())
		Expect(sqlDB.Close()).To(Succeed())
		reopened, e := gorm.Open(sqlite.Open(filepath.Join(dir, "music.db")+"?_busy_timeout=5000&_txlock=immediate"), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
		Expect(e).NotTo(HaveOccurred())
		conn, e := reopened.DB()
		Expect(e).NotTo(HaveOccurred())
		defer conn.Close()
		fresh := cfg
		fresh.DB = reopened
		other, e := New(fresh)
		Expect(e).NotTo(HaveOccurred())
		defer other.Close()
		manifest, e := other.Lookup(ctx, id)
		Expect(e).NotTo(HaveOccurred())
		r, e := other.Open(ctx, manifest)
		Expect(e).NotTo(HaveOccurred())
		Expect(r.Close()).To(Succeed())
	})
	It("deduplicates failed revoke obligations without losing duplicate explicit ACKs", func() {
		register()
		_ = prepare()
		objects.failDelete = true
		first := s.Revoke(ctx, "request")
		second := s.Revoke(ctx, "request")
		Expect(first.AttemptError).To(HaveOccurred())
		Expect(second.AttemptError).To(HaveOccurred())
		Expect(s.waiters).To(HaveLen(1))
		objects.failDelete = false
		Expect(s.Reconcile(ctx)).To(Succeed())
		Eventually(first.Released).Should(Receive(BeNil()))
		Eventually(second.Released).Should(Receive(BeNil()))
	})
	It("bounds a reconciliation pass while keyset pagination eventually reaches every row", func() {
		for i := 0; i < 130; i++ {
			req := fmt.Sprintf("paged-%03d", i)
			// Seed durable crash-recovery records, not active local admissions.
			Expect(db.Create(&requestRow{ID: req, Principal: "alice", Deadline: now.Add(time.Minute), State: "active"}).Error).To(Succeed())
			id, e := randomID()
			Expect(e).NotTo(HaveOccurred())
			Expect(db.Create(&artifactRow{ID: id, RequestID: req, Kind: "input", State: "allocated", StagingOwner: s.stagingOwner, Leaf: id + "/bytes", ExpiresAt: now.Add(time.Hour)}).Error).To(Succeed())
		}
		now = now.Add(2 * time.Hour)
		Expect(s.Reconcile(ctx)).To(Succeed())
		var count int64
		Expect(db.Model(&artifactRow{}).Where("state = ?", "deleted").Count(&count).Error).To(Succeed())
		Expect(count).To(BeNumerically("<=", 64))
		for i := 0; i < 6; i++ {
			Expect(s.Reconcile(ctx)).To(Succeed())
		}
		Expect(db.Model(&artifactRow{}).Where("state = ?", "deleted").Count(&count).Error).To(Succeed())
		Expect(count).To(Equal(int64(130)))
		Expect(s.inputs).To(BeEmpty())
	})
	It("backpressures saturated callers while independent Reconcile frees ACK slots", func() {
		register()
		_ = prepare()
		objects.failDelete = true
		tasks := make([]music.CleanupTask, 64)
		for i := range tasks {
			tasks[i] = s.Revoke(ctx, "request")
			Expect(tasks[i].AttemptError).To(HaveOccurred())
		}
		Expect(s.waiters).To(HaveLen(1))
		blocked := make(chan music.CleanupTask, 1)
		cancelled, cancel := context.WithCancel(ctx)
		cancel()
		go func() { blocked <- s.Revoke(cancelled, "request") }()
		Consistently(blocked, 50*time.Millisecond).ShouldNot(Receive())
		_, denied := s.ImportInput(ctx, "request", strings.NewReader("late recording"))
		Expect(denied).To(HaveOccurred())
		objects.failDelete = false
		Expect(s.Reconcile(ctx)).To(Succeed())
		for _, task := range tasks {
			Expect(task.Released).To(Receive(BeNil()))
		}
		var last music.CleanupTask
		Eventually(blocked).Should(Receive(&last))
		Expect(last.AttemptError).To(HaveOccurred())
		Expect(s.Reconcile(ctx)).To(Succeed())
		Expect(last.Released).To(Receive(BeNil()))
		Expect(s.waiters).To(BeEmpty())
	})
	It("bounds live request admission and returns registrations to baseline across sustained workflows", func() {
		for i := 0; i < 80; i++ {
			req := fmt.Sprintf("sustained-%03d", i)
			Expect(s.RegisterRequest(ctx, RequestMetadata{ID: req, Principal: "alice", Deadline: now.Add(time.Minute)})).To(Succeed())
			_, e := s.ImportInput(ctx, req, strings.NewReader("recording"))
			Expect(e).NotTo(HaveOccurred())
			ack(s.Revoke(ctx, req))
			Expect(s.inputs).To(BeEmpty())
			Expect(s.candidates).To(BeEmpty())
			Expect(s.prepared).To(BeEmpty())
			Expect(s.admitted).To(BeEmpty())
		}
		for i := 0; i < 64; i++ {
			Expect(s.RegisterRequest(ctx, RequestMetadata{ID: fmt.Sprintf("live-%03d", i), Principal: "alice", Deadline: now.Add(time.Minute)})).To(Succeed())
		}
		Expect(s.RegisterRequest(ctx, RequestMetadata{ID: "overflow", Principal: "alice", Deadline: now.Add(time.Minute)})).NotTo(Succeed())
	})
	It("releases exactly 64 artifact-free expired admissions but retains admission for owned bytes", func() {
		for i := 0; i < 64; i++ {
			Expect(s.RegisterRequest(ctx, RequestMetadata{ID: fmt.Sprintf("empty-%03d", i), Principal: "alice", Deadline: now.Add(time.Minute)})).To(Succeed())
		}
		now = now.Add(2 * time.Minute)
		Expect(s.Reconcile(ctx)).To(Succeed())
		Expect(s.admitted).To(BeEmpty())
		Expect(s.RegisterRequest(ctx, RequestMetadata{ID: "owned", Principal: "alice", Deadline: now.Add(time.Minute)})).To(Succeed())
		_, e := s.ImportInput(ctx, "owned", strings.NewReader("recording"))
		Expect(e).NotTo(HaveOccurred())
		// Make physical cleanup unavailable: visiting a terminal request row must
		// not discard admission while durable owned bytes remain.
		var owned artifactRow
		Expect(db.First(&owned, "request_id = ?", "owned").Error).To(Succeed())
		Expect(db.Model(&artifactRow{}).Where("id = ?", owned.ID).Update("staging_owner", "offline-owner").Error).To(Succeed())
		now = now.Add(2 * time.Minute)
		for i := 0; i < 3; i++ {
			_ = s.Reconcile(ctx)
		}
		Expect(s.admitted).To(HaveKey("owned"))
		Expect(db.Model(&artifactRow{}).Where("id = ?", owned.ID).Update("staging_owner", s.stagingOwner).Error).To(Succeed())
		for i := 0; i < 3; i++ {
			Expect(s.Reconcile(ctx)).To(Succeed())
		}
		Expect(s.admitted).To(BeEmpty())
	})
	It("rejects shared mode with local storage instead of falling back", func() {
		cfg.Shared = &SharedReadiness{SharedDatabase: true, PrivateS3Bucket: true}
		_, e := New(cfg)
		Expect(e).To(HaveOccurred())
	})
})

type faultStore struct {
	*PrivateFilesystem
	failPut, failDelete bool
	enter, resume       chan struct{}
}

func (f *faultStore) Put(ctx context.Context, key string, r io.Reader) error {
	if f.enter != nil {
		close(f.enter)
		<-f.resume
	}
	if f.failPut {
		if e := f.PrivateFilesystem.Put(ctx, key, strings.NewReader("partial")); e != nil {
			return e
		}
		return errors.New("injected partial PUT")
	}
	return f.PrivateFilesystem.Put(ctx, key, r)
}
func (f *faultStore) Delete(ctx context.Context, key string) error {
	if f.failDelete {
		return errors.New("injected deletion failure")
	}
	return f.PrivateFilesystem.Delete(ctx, key)
}
