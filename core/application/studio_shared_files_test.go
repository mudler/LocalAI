// SPDX-License-Identifier: MIT
package application

import (
	"context"
	"fmt"
	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/core/services/storage"
	"github.com/mudler/LocalAI/core/services/studio/files"
	"github.com/mudler/LocalAI/core/services/studio/workspace"
	"github.com/mudler/LocalAI/pkg/system"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"gorm.io/driver/postgres"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"
)

var _ = Describe("shared filesystem startup", func() {
	It("rejects explicit shared storage without a database even without a data path", func() {
		cfg := config.NewApplicationConfig()
		cfg.DataPath = ""
		cfg.ChatsSharedFilesDir = filepath.Join(GinkgoT().TempDir(), "shared")
		app := &Application{applicationConfig: cfg}
		Expect(app.initStudioFiles()).To(MatchError(ContainSubstring("PostgreSQL")))
	})
})

var _ = Describe("shared filesystem root policy", func() {
	DescribeTable("rejects unsafe syntax", func(path string) {
		Expect(validateSharedFilesRoot(path)).To(HaveOccurred())
	}, Entry("relative", "relative"), Entry("root", "/"), Entry("traversal", "/tmp/a/../b"), Entry("trailing slash", "/tmp/a/"))
	It("creates a dedicated directory restrictively and removes its probe", func() {
		root := filepath.Join(GinkgoT().TempDir(), "shared")
		Expect(validateSharedFilesRoot(root)).To(Succeed())
		info, err := os.Stat(root)
		Expect(err).NotTo(HaveOccurred())
		Expect(info.Mode().Perm() & 0o027).To(BeZero())
		entries, err := os.ReadDir(root)
		Expect(err).NotTo(HaveOccurred())
		Expect(entries).To(BeEmpty())
	})
	It("rejects files, symlink roots and symlink components without changing targets", func() {
		parent := GinkgoT().TempDir()
		target := filepath.Join(parent, "target")
		Expect(os.Mkdir(target, 0o750)).To(Succeed())
		link := filepath.Join(parent, "link")
		Expect(os.Symlink(target, link)).To(Succeed())
		Expect(validateSharedFilesRoot(link)).To(HaveOccurred())
		Expect(validateSharedFilesRoot(filepath.Join(link, "nested"))).To(HaveOccurred())
		file := filepath.Join(parent, "file")
		Expect(os.WriteFile(file, []byte("keep"), 0o600)).To(Succeed())
		Expect(validateSharedFilesRoot(file)).To(HaveOccurred())
		got, err := os.ReadFile(file)
		Expect(err).NotTo(HaveOccurred())
		Expect(string(got)).To(Equal("keep"))
		Expect(filepath.Join(target, "nested")).NotTo(BeADirectory())
	})
	It("rejects world-writable roots and non-sticky world-writable components", func() {
		root := GinkgoT().TempDir()
		Expect(os.Chmod(root, 0o777)).To(Succeed())
		Expect(validateSharedFilesRoot(root)).To(HaveOccurred())
		Expect(validateSharedFilesRoot(filepath.Join(root, "child"))).To(HaveOccurred())
	})
	It("fails the write probe as the normal service user without changing permissions", func() {
		Expect(os.Geteuid()).NotTo(BeZero(), "permission coverage requires an unprivileged test process")
		root := GinkgoT().TempDir()
		Expect(os.Chmod(root, 0o500)).To(Succeed())
		DeferCleanup(func() { Expect(os.Chmod(root, 0o700)).To(Succeed()) })
		Expect(validateSharedFilesRoot(root)).To(HaveOccurred())
		info, err := os.Stat(root)
		Expect(err).NotTo(HaveOccurred())
		Expect(info.Mode().Perm()).To(Equal(os.FileMode(0o500)))
	})
	It("rejects SQLite metadata rather than silently falling back", func() {
		db, err := gorm.Open(sqlite.Open(filepath.Join(GinkgoT().TempDir(), "auth.db")), &gorm.Config{})
		Expect(err).NotTo(HaveOccurred())
		pool, err := db.DB()
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(func() { Expect(pool.Close()).To(Succeed()) })
		cfg := config.NewApplicationConfig(config.WithChatsSharedFilesDir(filepath.Join(GinkgoT().TempDir(), "shared")))
		app := &Application{applicationConfig: cfg, authDB: db}
		Expect(app.initStudioFiles()).To(MatchError(ContainSubstring("PostgreSQL")))
	})
	It("propagates explicit configuration errors through New before services start", func() {
		state, err := system.GetSystemState(system.WithModelPath(GinkgoT().TempDir()), system.WithBackendPath(GinkgoT().TempDir()))
		Expect(err).NotTo(HaveOccurred())
		app, err := New(config.WithSystemState(state), config.WithChatsSharedFilesDir("relative"))
		Expect(app).To(BeNil())
		Expect(err).To(MatchError(ContainSubstring("PostgreSQL")))
	})
})

// Embedding the interface makes every unexpected operation fail immediately;
// only calls exercised by the legacy adapter are implemented explicitly.
type sharedFilesObjectSpy struct {
	storage.ObjectStore
	puts int
}

func (s *sharedFilesObjectSpy) Put(_ context.Context, _ string, r io.Reader) error {
	s.puts++
	_, err := io.Copy(io.Discard, r)
	return err
}
func (s *sharedFilesObjectSpy) Exists(context.Context, string) (bool, error) { return false, nil }

var _ = Describe("PostgreSQL shared filesystem application selection", func() {
	It("shares owner/frontend Files with independent connections and overrides S3 without changing roles", func() {
		raw := os.Getenv("LOCALAI_TEST_POSTGRES_URL")
		if raw == "" {
			Skip("LOCALAI_TEST_POSTGRES_URL is not set")
		}
		admin, err := gorm.Open(postgres.Open(raw), &gorm.Config{})
		Expect(err).NotTo(HaveOccurred())
		pool, err := admin.DB()
		Expect(err).NotTo(HaveOccurred())
		schema := fmt.Sprintf("swf_app_%d", time.Now().UnixNano())
		Expect(admin.Exec("CREATE SCHEMA " + schema).Error).To(Succeed())
		DeferCleanup(func() {
			Expect(admin.Exec("DROP SCHEMA " + schema + " CASCADE").Error).To(Succeed())
			Expect(pool.Close()).To(Succeed())
		})
		u, err := url.Parse(raw)
		Expect(err).NotTo(HaveOccurred())
		q := u.Query()
		q.Set("search_path", schema)
		u.RawQuery = q.Encode()
		root := filepath.Join(GinkgoT().TempDir(), "shared")
		open := func(dp string, shared bool, spy *sharedFilesObjectSpy) *Application {
			db, err := gorm.Open(postgres.Open(u.String()), &gorm.Config{})
			Expect(err).NotTo(HaveOccurred())
			pool, err := db.DB()
			Expect(err).NotTo(HaveOccurred())
			DeferCleanup(func() { Expect(pool.Close()).To(Succeed()) })
			ctx, cancel := context.WithCancel(context.Background())
			DeferCleanup(cancel)
			cfg := config.NewApplicationConfig(config.WithContext(ctx), config.WithDataPath(dp))
			cfg.DisableChatsIndexFiles = true
			if shared {
				cfg.ChatsSharedFilesDir = root
			}
			app := &Application{applicationConfig: cfg, authDB: db}
			if spy != nil {
				cfg.Distributed.Enabled = true
				cfg.Distributed.StorageURL = "http://s3.invalid:9000"
				app.distributed = &DistributedServices{Store: spy}
			}
			Expect(app.initStudioFiles()).To(Succeed())
			Expect(app.StudioFiles().Available()).To(BeTrue())
			DeferCleanup(func() {
				cancel()
				if app.studioFilesTemp != "" {
					Expect(os.RemoveAll(app.studioFilesTemp)).To(Succeed())
				}
			})
			return app
		}
		owner := open(GinkgoT().TempDir(), true, nil)
		spy := &sharedFilesObjectSpy{}
		frontend := open(GinkgoT().TempDir(), true, spy)
		Expect(owner.Distributed()).To(BeNil())
		Expect(owner.applicationConfig.Distributed.Enabled).To(BeFalse())
		Expect(frontend.Distributed().Store).To(BeIdenticalTo(spy))
		Expect(frontend.applicationConfig.Distributed.Enabled).To(BeTrue())
		Expect(owner.authDB).NotTo(BeIdenticalTo(frontend.authDB))
		Expect(owner.applicationConfig.DataPath).NotTo(Equal(frontend.applicationConfig.DataPath))
		Expect(owner.StudioFiles().EnsureHome(context.Background(), "alice")).To(Succeed())
		actor := workspace.Actor{Principal: "alice"}
		path, err := owner.StudioFiles().Abs("alice", "/from-owner.txt")
		Expect(err).NotTo(HaveOccurred())
		_, err = owner.StudioFiles().Store().Put(context.Background(), actor, path, strings.NewReader("owner bytes"), workspace.PutOptions{})
		Expect(err).NotTo(HaveOccurred())
		read := func(app *Application) {
			r, _, err := app.StudioFiles().Store().Open(context.Background(), actor, path)
			Expect(err).NotTo(HaveOccurred())
			data, err := io.ReadAll(r)
			Expect(err).NotTo(HaveOccurred())
			Expect(r.Close()).To(Succeed())
			Expect(string(data)).To(Equal("owner bytes"))
		}
		read(frontend)
		scratch := open("", true, nil)
		Expect(scratch.studioFilesTemp).NotTo(BeEmpty())
		read(scratch)
		Expect(spy.puts).To(BeZero(), "explicit workspace FS must never upload through S3")
		legacySpy := &sharedFilesObjectSpy{}
		legacy := open(GinkgoT().TempDir(), false, legacySpy)
		_, err = legacy.StudioFiles().Store().Put(context.Background(), workspace.Actor{Principal: files.AdminPrincipal}, "/shared/legacy.txt", strings.NewReader("legacy object bytes"), workspace.PutOptions{})
		Expect(err).NotTo(HaveOccurred())
		Expect(legacySpy.puts).To(BeNumerically(">", 0), "unset distributed storage must retain ObjectStore")
		blockedCfg := config.NewApplicationConfig(config.WithDataPath(GinkgoT().TempDir()))
		blocked := &Application{applicationConfig: blockedCfg, authDB: owner.authDB, distributed: &DistributedServices{Store: spy}}
		Expect(blocked.initStudioFiles()).To(Succeed())
		Expect(blocked.StudioFiles().Available()).To(BeFalse())
		Expect(blocked.StudioFiles().Unavailable()).To(ContainSubstring("S3"))
		badCfg := config.NewApplicationConfig(config.WithChatsSharedFilesDir("relative"))
		bad := &Application{applicationConfig: badCfg, authDB: owner.authDB}
		Expect(bad.initStudioFiles()).To(MatchError(ContainSubstring("absolute")))
	})
})

var _ = Describe("PostgreSQL shared filesystem startup cleanup", func() {
	DescribeTable("cancels only the owned context and closes the pool on Files failure", func(early bool) {
		raw := os.Getenv("LOCALAI_TEST_POSTGRES_URL")
		if raw == "" {
			Skip("LOCALAI_TEST_POSTGRES_URL is not set")
		}
		db, err := gorm.Open(postgres.Open(raw), &gorm.Config{})
		Expect(err).NotTo(HaveOccurred())
		pool, err := db.DB()
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(func() { _ = pool.Close() })
		caller, cancel := context.WithCancel(context.Background())
		DeferCleanup(cancel)
		cfg := config.NewApplicationConfig(config.WithContext(caller), config.WithDataPath(""), config.WithChatsSharedFilesDir(filepath.Join(GinkgoT().TempDir(), "shared")))
		cleanup := sharedFilesStartupCleanup(cfg)
		app := &Application{applicationConfig: cfg, authDB: db}
		// The early validation succeeds; a later change makes Files init fail.
		Expect(app.validateSharedFiles()).To(Succeed())
		if early {
			cfg.ChatsSharedFilesDir = "relative"
		} else {
			Expect(os.Remove(cfg.ChatsSharedFilesDir)).To(Succeed())
			Expect(os.WriteFile(cfg.ChatsSharedFilesDir, []byte("blocked"), 0o600)).To(Succeed())
		}
		DeferCleanup(func() {
			if app.studioFilesTemp != "" {
				_ = os.RemoveAll(app.studioFilesTemp)
			}
		})
		stopped := make(chan struct{})
		go func() { <-cfg.Context.Done(); close(stopped) }()
		if early {
			err = app.validateSharedFiles()
		} else {
			err = app.initStudioFiles()
			Expect(app.studioFilesTemp).To(BeEmpty(), "failed Files init must remove its temporary scratch")
		}
		Expect(err).To(HaveOccurred())
		cleanup(app)
		Expect(pool.Ping()).To(MatchError(ContainSubstring("database is closed")))
		Eventually(stopped).Should(BeClosed())
		Expect(caller.Err()).NotTo(HaveOccurred())
	}, Entry("early validation", true), Entry("late Files init", false))
})
