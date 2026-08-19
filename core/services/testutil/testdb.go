package testutil

import (
	"context"
	"runtime"
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
	if runtime.GOOS == "darwin" {
		Skip("testcontainers requires Docker, not available on macOS CI")
	}
	ctx := context.Background()
	pgC, err := tcpostgres.Run(ctx, "postgres:16",
		tcpostgres.WithDatabase("testdb"),
		tcpostgres.WithUsername("test"),
		tcpostgres.WithPassword("test"),
		testcontainers.WithWaitStrategyAndDeadline(60*time.Second,
			wait.ForLog("database system is ready to accept connections").WithOccurrence(2)),
	)
	Expect(err).ToNot(HaveOccurred())
	DeferCleanup(func() { pgC.Terminate(context.Background()) })
	connStr, err := pgC.ConnectionString(ctx, "sslmode=disable")
	Expect(err).ToNot(HaveOccurred())
	db, err := gorm.Open(postgres.Open(connStr), &gorm.Config{
		Logger: logger.Default.LogMode(logger.Silent),
	})
	Expect(err).ToNot(HaveOccurred())
	return db
}
