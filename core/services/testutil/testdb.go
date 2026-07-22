package testutil

import (
	"context"
	"fmt"
	"runtime"
	"sync"
	"sync/atomic"
	"time"

	"github.com/mudler/LocalAI/internal/testfixtures"
	"github.com/testcontainers/testcontainers-go"
	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"
	tcnetwork "github.com/testcontainers/testcontainers-go/network"
	"github.com/testcontainers/testcontainers-go/wait"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var (
	sharedDBMu        sync.Mutex
	sharedDBContainer testcontainers.Container
	sharedDBEndpoint  string
	sharedDBSequence  atomic.Uint64
)

// StartSharedTestDB starts one PostgreSQL container and returns its endpoint.
// Pass that endpoint to SetSharedTestDBEndpoint in every parallel test process.
func StartSharedTestDB() string {
	if runtime.GOOS == "darwin" {
		return ""
	}
	sharedDBMu.Lock()
	defer sharedDBMu.Unlock()
	if sharedDBContainer != nil {
		return sharedDBEndpoint
	}

	container, endpoint := startTestDBContainer()
	sharedDBContainer = container
	sharedDBEndpoint = endpoint
	return endpoint
}

// SetSharedTestDBEndpoint attaches this test process to the suite database.
func SetSharedTestDBEndpoint(endpoint string) {
	sharedDBMu.Lock()
	defer sharedDBMu.Unlock()
	sharedDBEndpoint = endpoint
}

// StopSharedTestDB terminates the process-scoped PostgreSQL fixture.
func StopSharedTestDB() {
	sharedDBMu.Lock()
	defer sharedDBMu.Unlock()
	if sharedDBContainer == nil {
		return
	}
	Expect(sharedDBContainer.Terminate(context.Background())).To(Succeed())
	sharedDBContainer = nil
	sharedDBEndpoint = ""
}

// SetupTestDB returns an isolated PostgreSQL database fixture. Suites that call
// StartSharedTestDB get a fresh schema; other suites retain a fresh container.
func SetupTestDB() *gorm.DB {
	if runtime.GOOS == "darwin" {
		Skip("testcontainers requires Docker, not available on macOS CI")
	}

	sharedDBMu.Lock()
	endpoint := sharedDBEndpoint
	sharedDBMu.Unlock()
	if endpoint != "" {
		return setupIsolatedSchema(endpoint)
	}

	pgC, endpoint := startTestDBContainer()
	DeferCleanup(func() { _ = pgC.Terminate(context.Background()) })
	return openTestDB(endpoint, "")
}

func startTestDBContainer() (testcontainers.Container, string) {
	ctx := context.Background()
	Expect(testfixtures.RequireImage(ctx, testfixtures.Postgres16, "default")).To(Succeed())
	testNetwork, err := testfixtures.DockerNetwork()
	Expect(err).NotTo(HaveOccurred())
	pgC, err := tcpostgres.Run(ctx, testfixtures.Postgres16,
		tcpostgres.WithDatabase("testdb"),
		tcpostgres.WithUsername("test"),
		tcpostgres.WithPassword("test"),
		testcontainers.WithWaitStrategyAndDeadline(60*time.Second,
			wait.ForLog("database system is ready to accept connections").WithOccurrence(2)),
		tcnetwork.WithNetworkName([]string{"postgres"}, testNetwork),
	)
	Expect(err).ToNot(HaveOccurred())
	endpoint, err := testfixtures.ContainerEndpoint(ctx, pgC, "5432")
	Expect(err).ToNot(HaveOccurred())
	return pgC, endpoint
}

func setupIsolatedSchema(endpoint string) *gorm.DB {
	schema := fmt.Sprintf("test_%d_%d", GinkgoParallelProcess(), sharedDBSequence.Add(1))
	admin := openTestDB(endpoint, "")
	Expect(admin.Exec("CREATE SCHEMA " + schema).Error).ToNot(HaveOccurred())
	db := openTestDB(endpoint, schema)
	DeferCleanup(func() {
		if sqlDB, err := db.DB(); err == nil {
			_ = sqlDB.Close()
		}
		Expect(admin.Exec("DROP SCHEMA " + schema + " CASCADE").Error).ToNot(HaveOccurred())
		if sqlDB, err := admin.DB(); err == nil {
			_ = sqlDB.Close()
		}
	})
	return db
}

func openTestDB(endpoint, schema string) *gorm.DB {
	connStr := fmt.Sprintf("postgres://test:test@%s/testdb?sslmode=disable", endpoint)
	if schema != "" {
		connStr += "&search_path=" + schema
	}
	db, err := gorm.Open(postgres.Open(connStr), &gorm.Config{
		Logger: logger.Default.LogMode(logger.Silent),
	})
	Expect(err).ToNot(HaveOccurred())
	return db
}
