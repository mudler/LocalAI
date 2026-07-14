package distributed_test

import (
	"context"
	"time"

	"github.com/mudler/LocalAI/core/services/messaging"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/testcontainers/testcontainers-go"
	tcnats "github.com/testcontainers/testcontainers-go/modules/nats"
	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"
	"github.com/testcontainers/testcontainers-go/wait"
)

// TestInfra holds shared test containers and connection strings.
type TestInfra struct {
	Ctx           context.Context
	PGContainer   *tcpostgres.PostgresContainer
	NATSContainer *tcnats.NATSContainer
	PGURL         string
	NatsURL       string
	NC            *messaging.Client
}

// SetupInfra starts PostgreSQL and NATS containers and connects a messaging client.
// Call in BeforeEach. Use DeferCleanup or call Teardown in AfterEach.
func SetupInfra(dbName string) *TestInfra {
	GinkgoHelper()

	infra := &TestInfra{Ctx: context.Background()}
	var err error

	// Start PostgreSQL container
	infra.PGContainer, err = tcpostgres.Run(infra.Ctx, "postgres:16-alpine",
		tcpostgres.WithDatabase(dbName),
		tcpostgres.WithUsername("test"),
		tcpostgres.WithPassword("test"),
		testcontainers.WithWaitStrategy(
			wait.ForLog("database system is ready to accept connections").
				WithOccurrence(2).
				WithStartupTimeout(30*time.Second),
		),
	)
	Expect(err).ToNot(HaveOccurred())

	infra.PGURL, err = infra.PGContainer.ConnectionString(infra.Ctx, "sslmode=disable")
	Expect(err).ToNot(HaveOccurred())

	// Start NATS container
	infra.NATSContainer, err = tcnats.Run(infra.Ctx, "nats:2-alpine")
	Expect(err).ToNot(HaveOccurred())

	infra.NatsURL, err = infra.NATSContainer.ConnectionString(infra.Ctx)
	Expect(err).ToNot(HaveOccurred())

	// Connect messaging client
	infra.NC, err = messaging.New(infra.NatsURL)
	Expect(err).ToNot(HaveOccurred())

	// Register cleanup in LIFO order
	DeferCleanup(func() {
		if infra.NC != nil {
			infra.NC.Close()
		}
		if infra.PGContainer != nil {
			infra.PGContainer.Terminate(context.Background())
		}
		if infra.NATSContainer != nil {
			infra.NATSContainer.Terminate(context.Background())
		}
	})

	return infra
}

// SetupNATSOnly starts only a NATS container and connects a messaging client.
// Useful for tests that don't need PostgreSQL.
func SetupNATSOnly() *TestInfra {
	GinkgoHelper()

	infra := &TestInfra{Ctx: context.Background()}
	var err error

	infra.NATSContainer, err = tcnats.Run(infra.Ctx, "nats:2-alpine")
	Expect(err).ToNot(HaveOccurred())

	infra.NatsURL, err = infra.NATSContainer.ConnectionString(infra.Ctx)
	Expect(err).ToNot(HaveOccurred())

	infra.NC, err = messaging.New(infra.NatsURL)
	Expect(err).ToNot(HaveOccurred())

	DeferCleanup(func() {
		if infra.NC != nil {
			infra.NC.Close()
		}
		if infra.NATSContainer != nil {
			infra.NATSContainer.Terminate(context.Background())
		}
	})

	return infra
}

// FlushNATS ensures all subscriptions are registered server-side before publishing.
// Replaces time.Sleep(100ms) after Subscribe calls.
func FlushNATS(nc *messaging.Client) {
	GinkgoHelper()
	Expect(nc.Conn().Flush()).To(Succeed())
}
