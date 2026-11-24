// SPDX-License-Identifier: MIT
package application

import (
	"context"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/core/services/storage"
	"github.com/mudler/LocalAI/core/services/studio/workspace"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
)

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
	It("shares owner/frontend/worker Files automatically with independent connections without changing roles", func() {
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
		open := func(spy *sharedFilesObjectSpy) *Application {
			db, err := gorm.Open(postgres.Open(u.String()), &gorm.Config{})
			Expect(err).NotTo(HaveOccurred())
			pool, err := db.DB()
			Expect(err).NotTo(HaveOccurred())
			DeferCleanup(func() { Expect(pool.Close()).To(Succeed()) })
			ctx, cancel := context.WithCancel(context.Background())
			DeferCleanup(cancel)
			cfg := config.NewApplicationConfig(config.WithContext(ctx), config.WithDataPath(root))
			cfg.DisableChatsIndexFiles = true
			app := &Application{applicationConfig: cfg, authDB: db}
			if spy != nil {
				cfg.Distributed.Enabled = true
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
		owner := open(nil)
		spy := &sharedFilesObjectSpy{}
		frontend := open(spy)
		Expect(owner.Distributed()).To(BeNil())
		Expect(owner.applicationConfig.Distributed.Enabled).To(BeFalse())
		Expect(frontend.Distributed().Store).To(BeIdenticalTo(spy))
		Expect(frontend.applicationConfig.Distributed.Enabled).To(BeTrue())
		Expect(owner.authDB).NotTo(BeIdenticalTo(frontend.authDB))
		Expect(owner.applicationConfig.DataPath).To(Equal(frontend.applicationConfig.DataPath))
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
		worker := open(spy)
		worker.applicationConfig.Distributed.ChatWorker = true
		read(worker)
		reopened := open(spy)
		read(reopened)
		Expect(spy.puts).To(BeZero(), "workspace FS must never upload through generic fallback")
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
		cfg := config.NewApplicationConfig(config.WithContext(caller), config.WithDataPath(GinkgoT().TempDir()), config.WithAuthEnabled(true), config.WithAuthDatabaseURL(raw))
		ctx, cleanup := workspaceStartupCleanup(cfg.Context, cfg)
		cfg.Context = ctx
		app := &Application{applicationConfig: cfg, authDB: db}
		if early {
			cfg.Distributed.StorageURL = "http://s3.invalid"
		} else {
			Expect(os.WriteFile(filepath.Join(cfg.DataPath, "chats"), []byte("blocked"), 0o600)).To(Succeed())
		}
		DeferCleanup(func() {
			if app.studioFilesTemp != "" {
				_ = os.RemoveAll(app.studioFilesTemp)
			}
		})
		stopped := make(chan struct{})
		go func() { <-cfg.Context.Done(); close(stopped) }()
		err = app.initStudioFiles()
		Expect(app.studioFilesTemp).To(BeEmpty())
		Expect(err).To(HaveOccurred())
		cleanup(app)
		Expect(pool.Ping()).To(MatchError(ContainSubstring("database is closed")))
		Eventually(stopped).Should(BeClosed())
		Expect(caller.Err()).NotTo(HaveOccurred())
	}, Entry("early S3 configuration", true), Entry("late Files init", false))
})
