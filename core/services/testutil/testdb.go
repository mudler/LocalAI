package testutil

import (
	"context"
	"fmt"
	"runtime"
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

// SetupTestDB creates a fresh PostgreSQL 16 container and returns a gorm.DB.
// The container is cleaned up via DeferCleanup when the test completes.
func SetupTestDB() *gorm.DB {
	if runtime.GOOS == "darwin" {
		Skip("testcontainers requires Docker, not available on macOS CI")
	}
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
	DeferCleanup(func() { pgC.Terminate(context.Background()) })
	endpoint, err := testfixtures.ContainerEndpoint(ctx, pgC, "5432")
	Expect(err).ToNot(HaveOccurred())
	connStr := fmt.Sprintf("postgres://test:test@%s/testdb?sslmode=disable", endpoint)
	db, err := gorm.Open(postgres.Open(connStr), &gorm.Config{
		Logger: logger.Default.LogMode(logger.Silent),
	})
	Expect(err).ToNot(HaveOccurred())
	return db
}
