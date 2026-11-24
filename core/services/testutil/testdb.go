package testutil

import (
	"context"
	"fmt"
	"net/url"
	"os"
	"runtime"
	"sync/atomic"
	"time"

	"github.com/testcontainers/testcontainers-go"
	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"
	"github.com/testcontainers/testcontainers-go/wait"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// SetupTestDB creates a fresh PostgreSQL 16 container and returns a gorm.DB.
// The container is cleaned up via DeferCleanup when the test completes.
func SetupTestDB() *gorm.DB {
	db, _ := SetupTestDBWithDSN()
	return db
}

// SetupTestDBWithDSN is SetupTestDB that also returns the connection string of
// the container, for code that opens a connection of its own, such as a
// LISTEN connection that cannot come from a pool.
func SetupTestDBWithDSN() (*gorm.DB, string) {
	if runtime.GOOS == "darwin" {
		Skip("testcontainers requires Docker, not available on macOS CI")
	}
	if runtime.GOOS == "windows" {
		Skip("testcontainers requires Docker, not available on Windows CI")
	}
	ctx := context.Background()
	pgC, err := tcpostgres.Run(ctx, "postgres:16",
		tcpostgres.WithDatabase("testdb"),
		tcpostgres.WithUsername("test"),
		tcpostgres.WithPassword("test"),
		// The data directory lives in memory. The image declares it as a volume,
		// and a container that is removed leaves that anonymous volume behind,
		// so every spec would leave tens of megabytes on the disk of the host.
		testcontainers.WithTmpfs(map[string]string{"/var/lib/postgresql/data": "rw"}),
		testcontainers.WithWaitStrategyAndDeadline(60*time.Second,
			wait.ForLog("database system is ready to accept connections").WithOccurrence(2)),
	)
	Expect(err).ToNot(HaveOccurred())
	DeferCleanup(func() {
		if err := pgC.Terminate(context.Background()); err != nil {
			GinkgoWriter.Printf("terminating the test PostgreSQL container: %v\n", err)
		}
	})
	connStr, err := pgC.ConnectionString(ctx, "sslmode=disable")
	Expect(err).ToNot(HaveOccurred())
	db, err := gorm.Open(postgres.Open(connStr), &gorm.Config{
		Logger: logger.Default.LogMode(logger.Silent),
	})
	Expect(err).ToNot(HaveOccurred())
	return db, connStr
}

var schemaSeq atomic.Int64

// OpenPostgresSchema opens the PostgreSQL database at dsn (a URL, such as
// LOCALAI_TEST_POSTGRES_URL) in a schema of its own, named after prefix, the
// process and a counter, and drops the schema when the spec ends. Packages
// that run at the same time on one database then never see each other's
// rows: a spec that lists every running row, or deletes every row of a
// table, stays inside its own schema.
func OpenPostgresSchema(dsn, prefix string) *gorm.DB {
	schema := fmt.Sprintf("%s_%d_%d_%d", prefix, os.Getpid(), time.Now().UnixNano()%1_000_000, schemaSeq.Add(1))
	admin, err := gorm.Open(postgres.Open(dsn), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	Expect(err).ToNot(HaveOccurred())
	Expect(admin.Exec(`CREATE SCHEMA ` + schema).Error).To(Succeed())
	u, err := url.Parse(dsn)
	Expect(err).ToNot(HaveOccurred())
	q := u.Query()
	q.Set("search_path", schema)
	u.RawQuery = q.Encode()
	db, err := gorm.Open(postgres.Open(u.String()), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	Expect(err).ToNot(HaveOccurred())
	DeferCleanup(func() {
		if raw, err := db.DB(); err == nil {
			_ = raw.Close()
		}
		_ = admin.Exec(`DROP SCHEMA IF EXISTS ` + schema + ` CASCADE`).Error
		if raw, err := admin.DB(); err == nil {
			_ = raw.Close()
		}
	})
	return db
}
