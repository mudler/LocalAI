// SPDX-License-Identifier: MIT
package application

import (
	"context"
	"errors"
	"io"
	"path/filepath"
	"strings"

	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/core/services/storage"
	"github.com/mudler/LocalAI/core/services/studio/files"
	"github.com/mudler/LocalAI/core/services/studio/workspace"
	"github.com/mudler/LocalAI/core/services/studio/workspace/blob"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

type workspaceS3Spy struct {
	storage.ObjectStore
	bytes map[string]string
	puts  int
	gets  int
}

func (s *workspaceS3Spy) Put(_ context.Context, key string, r io.Reader) error {
	b, err := io.ReadAll(r)
	s.bytes[key] = string(b)
	s.puts++
	return err
}
func (s *workspaceS3Spy) Get(_ context.Context, key string) (io.ReadCloser, error) {
	s.gets++
	return io.NopCloser(strings.NewReader(s.bytes[key])), nil
}
func (s *workspaceS3Spy) Exists(_ context.Context, key string) (bool, error) {
	_, ok := s.bytes[key]
	return ok, nil
}

var _ = Describe("automatic workspace", func() {
	newApp := func(dp string) *Application {
		ctx, cancel := context.WithCancel(context.Background())
		DeferCleanup(cancel)
		app := &Application{applicationConfig: config.NewApplicationConfig(config.WithContext(ctx), config.WithDataPath(dp))}
		app.applicationConfig.Distributed.ChatWorker = true // do not start the text-index runner in unit fixtures
		DeferCleanup(func() { Expect(app.Shutdown()).To(Succeed()) })
		return app
	}
	withSQLite := func(app *Application) {
		db, err := gorm.Open(sqlite.Open(filepath.Join(GinkgoT().TempDir(), "auth.db")), &gorm.Config{})
		Expect(err).NotTo(HaveOccurred())
		pool, err := db.DB()
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(func() { _ = pool.Close() })
		app.authDB = db
	}
	It("uses direct filesystem in distributed mode without S3 despite a generic fallback store", func() {
		app := newApp(GinkgoT().TempDir())
		app.applicationConfig.Distributed.Enabled = true
		spy := &sharedFilesObjectSpy{}
		app.distributed = &DistributedServices{Store: spy}
		Expect(app.initStudioFiles()).To(Succeed())
		Expect(app.StudioFiles().Available()).To(BeTrue())
		Expect(filepath.Join(app.applicationConfig.DataPath, "chats", "blobs")).To(BeADirectory())
		Expect(spy.puts).To(BeZero())
	})
	It("honors owner configured S3 and rejects a missing bucket rather than choosing FS", func() {
		app := newApp(GinkgoT().TempDir())
		app.applicationConfig.Distributed.StorageURL = "http://s3.invalid:9000"
		Expect(app.initStudioFiles()).To(MatchError(ContainSubstring("bucket")))
		Expect(app.Distributed()).To(BeNil())
	})
	DescribeTable("cleans up standalone configured S3 startup failure", func(database bool) {
		caller, cancel := context.WithCancel(context.Background())
		DeferCleanup(cancel)
		cfg := config.NewApplicationConfig(config.WithContext(caller), config.WithDataPath(GinkgoT().TempDir()))
		cfg.Distributed.StorageURL = "http://s3.invalid:9000" // no bucket: fail before any network access
		cfg.Auth.Enabled = database
		cfg.Auth.DatabaseURL = filepath.Join(GinkgoT().TempDir(), "auth.db")
		ctx, cleanup := workspaceStartupCleanup(cfg.Context, cfg) // the same ownership scope used by New
		cfg.Context = ctx
		app := &Application{applicationConfig: cfg}
		DeferCleanup(func() { Expect(app.Shutdown()).To(Succeed()) })
		if database {
			withSQLite(app)
		}
		cleaned := false
		app.studioLoopsStop = func() { cleaned = true }
		stopped := make(chan struct{})
		go func() { <-cfg.Context.Done(); close(stopped) }()

		Expect(app.initStudioFiles()).To(MatchError(ContainSubstring("bucket")))
		cleanup(app)
		if database {
			pool, err := app.authDB.DB()
			Expect(err).NotTo(HaveOccurred())
			Expect(pool.Ping()).To(MatchError(ContainSubstring("database is closed")))
		}
		Expect(cfg.Context.Err()).To(MatchError(context.Canceled))
		Eventually(stopped).Should(BeClosed())
		Expect(cleaned).To(BeTrue(), "startup failure must shut down already opened resources")
		Expect(caller.Err()).NotTo(HaveOccurred())
		Expect(app.Distributed()).To(BeNil())
	}, Entry("SQLite owner", true), Entry("no-auth owner", false))
	DescribeTable("constructs owner S3 once and shares bytes through frontend S3 without changing global storage", func(database, noPath bool) {
		dp := GinkgoT().TempDir()
		if noPath {
			dp = ""
		}
		owner := newApp(dp)
		if database {
			withSQLite(owner)
		}
		cfg := &owner.applicationConfig.Distributed
		cfg.StorageURL, cfg.StorageBucket, cfg.StorageRegion = "http://s3.invalid:9000", "workspace", "test-region"
		cfg.StorageAccessKey, cfg.StorageSecretKey = "test-key", "test-secret"
		spy := &workspaceS3Spy{bytes: map[string]string{}}
		calls := 0
		constructor := func(_ context.Context, got storage.S3Config) (storage.ObjectStore, error) {
			calls++
			Expect(got).To(Equal(storage.S3Config{Endpoint: cfg.StorageURL, Bucket: cfg.StorageBucket, Region: cfg.StorageRegion, AccessKeyID: cfg.StorageAccessKey, SecretAccessKey: cfg.StorageSecretKey, ForcePathStyle: true}))
			return spy, nil
		}
		Expect(owner.initStudioFilesWithS3(constructor)).To(Succeed())
		Expect(calls).To(Equal(1))
		Expect(owner.Distributed()).To(BeNil())
		Expect(owner.applicationConfig.Distributed.Enabled).To(BeFalse())
		if noPath {
			Expect(owner.studioFilesTemp).NotTo(BeEmpty())
		}
		actor := workspace.Actor{Principal: files.AdminPrincipal}
		_, err := owner.StudioFiles().Store().Put(context.Background(), actor, "/shared/owner.txt", strings.NewReader("S3 owner bytes"), workspace.PutOptions{})
		Expect(err).NotTo(HaveOccurred())
		Expect(spy.puts).To(BeNumerically(">", 0))
		frontend := newApp(GinkgoT().TempDir())
		frontend.applicationConfig.Distributed = *cfg
		frontend.distributed = &DistributedServices{Store: spy}
		if database {
			frontend.authDB = owner.authDB
		}
		Expect(frontend.initStudioFilesWithS3(constructor)).To(Succeed())
		Expect(calls).To(Equal(1), "must reuse distributed S3 instance")
		Expect(frontend.Distributed().Store).To(BeIdenticalTo(spy))
		if database {
			r, _, err := frontend.StudioFiles().Store().Open(context.Background(), actor, "/shared/owner.txt")
			Expect(err).NotTo(HaveOccurred())
			b, err := io.ReadAll(r)
			Expect(err).NotTo(HaveOccurred())
			Expect(r.Close()).To(Succeed())
			Expect(string(b)).To(Equal("S3 owner bytes"))
			Expect(spy.gets).To(BeNumerically(">", 0))
		}
		Expect(filepath.Join(frontend.applicationConfig.DataPath, "chats", "blobs")).NotTo(BeADirectory())
	}, Entry("standalone JSON", false, false), Entry("standalone SQLite", true, false), Entry("S3 cache without DataPath", true, true))
	It("validates explicit credentials, permits SDK chain, and surfaces constructor failure", func() {
		app := newApp(GinkgoT().TempDir())
		cfg := &app.applicationConfig.Distributed
		cfg.StorageURL, cfg.StorageBucket = "http://s3.invalid", "workspace"
		calls := 0
		constructor := func(_ context.Context, got storage.S3Config) (storage.ObjectStore, error) {
			calls++
			Expect(got.AccessKeyID).To(BeEmpty())
			Expect(got.SecretAccessKey).To(BeEmpty())
			return nil, errors.New("constructor failed")
		}
		cfg.StorageAccessKey = "key"
		Expect(app.initStudioFilesWithS3(constructor)).To(MatchError(ContainSubstring("both access key")))
		cfg.StorageAccessKey, cfg.StorageSecretKey = "", "secret"
		Expect(app.initStudioFilesWithS3(constructor)).To(MatchError(ContainSubstring("both access key")))
		Expect(calls).To(BeZero())
		cfg.StorageSecretKey = ""
		Expect(app.initStudioFilesWithS3(constructor)).To(MatchError(ContainSubstring("constructor failed")))
		Expect(calls).To(Equal(1))
		cfg.StorageURL, cfg.StorageAccessKey = "", "unused"
		Expect(app.initStudioFilesWithS3(constructor)).To(Succeed())
		Expect(calls).To(Equal(1))
	})
	It("constructs the existing S3 adapter at a fake endpoint without contacting it", func() {
		app := newApp(GinkgoT().TempDir())
		cfg := &app.applicationConfig.Distributed
		cfg.StorageURL, cfg.StorageBucket, cfg.StorageAccessKey, cfg.StorageSecretKey = "http://127.0.0.1:1", "workspace", "key", "secret"
		objects, err := app.workspaceObjects(func(ctx context.Context, cfg storage.S3Config) (storage.ObjectStore, error) {
			return storage.NewS3Store(ctx, cfg)
		})
		Expect(err).NotTo(HaveOccurred())
		Expect(objects).To(BeAssignableToTypeOf(&storage.S3Store{}))
	})
	It("keeps Files disabled without DataPath even with SQLite or a generic fallback", func() {
		app := newApp("")
		withSQLite(app)
		app.distributed = &DistributedServices{Store: &sharedFilesObjectSpy{}}
		Expect(app.initStudioFiles()).To(Succeed())
		Expect(app.StudioFiles().Available()).To(BeFalse())
		Expect(app.studioFilesTemp).To(BeEmpty())
	})
	It("does not treat object-store cache as a direct filesystem", func() {
		fs, err := blob.NewFS(GinkgoT().TempDir())
		Expect(err).NotTo(HaveOccurred())
		Expect(fs.(interface{ IsFilesystem() bool }).IsFilesystem()).To(BeTrue())
		_, direct := blob.NewObjectStore(&sharedFilesObjectSpy{}, GinkgoT().TempDir()).(interface{ IsFilesystem() bool })
		Expect(direct).To(BeFalse())
	})
})
