package distributed_test

import (
	"context"
	"fmt"
	"time"

	"github.com/mudler/LocalAI/core/services/messaging"
	"github.com/mudler/LocalAI/internal/testfixtures"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/testcontainers/testcontainers-go"
	tcnats "github.com/testcontainers/testcontainers-go/modules/nats"
	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"
	tcnetwork "github.com/testcontainers/testcontainers-go/network"
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
	Expect(testfixtures.RequireImage(infra.Ctx, testfixtures.Postgres16Alpine, "distributed-e2e")).To(Succeed())
	Expect(testfixtures.RequireImage(infra.Ctx, testfixtures.NATS2Alpine, "distributed-e2e")).To(Succeed())
	testNetwork, err := testfixtures.DockerNetwork()
	Expect(err).NotTo(HaveOccurred())

	// Start PostgreSQL container
	infra.PGContainer, err = tcpostgres.Run(infra.Ctx, testfixtures.Postgres16Alpine,
		tcpostgres.WithDatabase(dbName),
		tcpostgres.WithUsername("test"),
		tcpostgres.WithPassword("test"),
		testcontainers.WithWaitStrategy(
			wait.ForLog("database system is ready to accept connections").
				WithOccurrence(2).
				WithStartupTimeout(30*time.Second),
		),
		tcnetwork.WithNetworkName([]string{"postgres"}, testNetwork),
	)
	Expect(err).ToNot(HaveOccurred())

	pgEndpoint, err := testfixtures.ContainerEndpoint(infra.Ctx, infra.PGContainer, "5432")
	Expect(err).ToNot(HaveOccurred())
	infra.PGURL = fmt.Sprintf("postgres://test:test@%s/%s?sslmode=disable", pgEndpoint, dbName)

	// Start NATS container
	infra.NATSContainer, err = tcnats.Run(infra.Ctx, testfixtures.NATS2Alpine,
		tcnetwork.WithNetworkName([]string{"nats"}, testNetwork),
		testcontainers.WithWaitStrategy(wait.ForLog("Server is ready")))
	Expect(err).ToNot(HaveOccurred())

	natsEndpoint, err := testfixtures.ContainerEndpoint(infra.Ctx, infra.NATSContainer, "4222")
	Expect(err).ToNot(HaveOccurred())
	infra.NatsURL = "nats://" + natsEndpoint

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
	Expect(testfixtures.RequireImage(infra.Ctx, testfixtures.NATS2Alpine, "distributed-e2e")).To(Succeed())
	testNetwork, err := testfixtures.DockerNetwork()
	Expect(err).NotTo(HaveOccurred())

	infra.NATSContainer, err = tcnats.Run(infra.Ctx, testfixtures.NATS2Alpine,
		tcnetwork.WithNetworkName([]string{"nats"}, testNetwork),
		testcontainers.WithWaitStrategy(wait.ForLog("Server is ready")))
	Expect(err).ToNot(HaveOccurred())

	natsEndpoint, err := testfixtures.ContainerEndpoint(infra.Ctx, infra.NATSContainer, "4222")
	Expect(err).ToNot(HaveOccurred())
	infra.NatsURL = "nats://" + natsEndpoint

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
