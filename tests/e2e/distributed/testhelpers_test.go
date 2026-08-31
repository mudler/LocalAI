package distributed_test

import (
	"context"
	"fmt"
	"net/url"
	"strings"
	"sync/atomic"
	"time"

	"github.com/mudler/LocalAI/core/services/messaging"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/testcontainers/testcontainers-go"
	tcnats "github.com/testcontainers/testcontainers-go/modules/nats"
	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"
	"github.com/testcontainers/testcontainers-go/wait"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	gormlogger "gorm.io/gorm/logger"
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

// Containers are suite-scoped, not spec-scoped. Starting a Postgres (~10s) and a
// NATS (~3.5s) per spec cost roughly 48 minutes of pure startup across the 213
// specs behind SetupInfra, which is why this suite was never wired into CI.
// Isolation now comes from a database per spec (~67ms), which is what the dbName
// argument was always describing.
//
// Plain BeforeSuite rather than SynchronizedBeforeSuite is deliberate: under
// `ginkgo -p` each process gets its own container pair, which keeps NATS subjects
// isolated per process. A single shared NATS across parallel processes would let
// specs on different processes see each other's messages on the same subject.
var (
	suitePG      *tcpostgres.PostgresContainer
	suiteNATS    *tcnats.NATSContainer
	suitePGDSN   string
	suiteNatsURL string
	dbCounter    atomic.Int64
)

var _ = BeforeSuite(func() {
	ctx := context.Background()
	var err error

	suitePG, err = tcpostgres.Run(ctx, "postgres:16-alpine",
		tcpostgres.WithDatabase("localai_suite"),
		tcpostgres.WithUsername("test"),
		tcpostgres.WithPassword("test"),
		testcontainers.WithWaitStrategy(
			wait.ForLog("database system is ready to accept connections").
				WithOccurrence(2).
				WithStartupTimeout(90*time.Second),
		),
	)
	Expect(err).ToNot(HaveOccurred())

	suitePGDSN, err = suitePG.ConnectionString(ctx, "sslmode=disable")
	Expect(err).ToNot(HaveOccurred())

	suiteNATS, err = tcnats.Run(ctx, "nats:2-alpine")
	Expect(err).ToNot(HaveOccurred())

	suiteNatsURL, err = suiteNATS.ConnectionString(ctx)
	Expect(err).ToNot(HaveOccurred())
})

var _ = AfterSuite(func() {
	ctx := context.Background()
	if suitePG != nil {
		_ = suitePG.Terminate(ctx)
	}
	if suiteNATS != nil {
		_ = suiteNATS.Terminate(ctx)
	}
})

// sanitizeDBName maps a spec-supplied label onto a legal unquoted Postgres
// identifier, leaving headroom for the uniqueness suffix appended by SetupInfra.
func sanitizeDBName(name string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(name) {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9', r == '_':
			b.WriteRune(r)
		default:
			b.WriteRune('_')
		}
	}
	out := strings.Trim(b.String(), "_")
	if out == "" {
		out = "spec"
	}
	// Postgres identifiers cap at 63 bytes; reserve the rest for "_<counter>".
	if len(out) > 50 {
		out = out[:50]
	}
	return out
}

// replaceDBName swaps the database component of a DSN, preserving credentials,
// host, port and query parameters.
func replaceDBName(dsn, name string) string {
	GinkgoHelper()
	u, err := url.Parse(dsn)
	Expect(err).ToNot(HaveOccurred())
	u.Path = "/" + name
	return u.String()
}

// adminDB opens a short-lived connection to the suite's maintenance database.
// CREATE/DROP DATABASE cannot run inside a transaction or against the target
// database itself, so every call gets its own connection and closes it.
func adminDB() *gorm.DB {
	GinkgoHelper()
	db, err := gorm.Open(postgres.Open(suitePGDSN), &gorm.Config{Logger: gormlogger.Discard})
	Expect(err).ToNot(HaveOccurred())
	return db
}

func closeDB(db *gorm.DB) {
	if db == nil {
		return
	}
	if sqlDB, err := db.DB(); err == nil {
		_ = sqlDB.Close()
	}
}

// SetupInfra provisions a dedicated database on the suite-scoped Postgres and
// returns a client connected to the suite-scoped NATS. Call in BeforeEach;
// cleanup is registered with DeferCleanup.
func SetupInfra(dbName string) *TestInfra {
	GinkgoHelper()
	Expect(suitePG).ToNot(BeNil(), "SetupInfra called before BeforeSuite started the shared containers")

	infra := &TestInfra{
		Ctx:           context.Background(),
		PGContainer:   suitePG,
		NATSContainer: suiteNATS,
		NatsURL:       suiteNatsURL,
	}

	db := fmt.Sprintf("%s_%d", sanitizeDBName(dbName), dbCounter.Add(1))

	admin := adminDB()
	Expect(admin.Exec(fmt.Sprintf("CREATE DATABASE %q", db)).Error).To(Succeed())
	closeDB(admin)

	infra.PGURL = replaceDBName(suitePGDSN, db)

	var err error
	infra.NC, err = messaging.New(infra.NatsURL)
	Expect(err).ToNot(HaveOccurred())

	DeferCleanup(func() {
		if infra.NC != nil {
			infra.NC.Close()
		}
		// FORCE terminates any connection the spec left open (Postgres 13+).
		// Failure to drop must not fail the spec: the container dies at AfterSuite.
		drop := adminDB()
		if err := drop.Exec(fmt.Sprintf("DROP DATABASE IF EXISTS %q WITH (FORCE)", db)).Error; err != nil {
			AddReportEntry("drop database failed", fmt.Sprintf("%s: %v", db, err))
		}
		closeDB(drop)
	})

	return infra
}

// SetupNATSOnly returns a client on the suite-scoped NATS for specs that need no
// database.
func SetupNATSOnly() *TestInfra {
	GinkgoHelper()
	Expect(suiteNATS).ToNot(BeNil(), "SetupNATSOnly called before BeforeSuite started the shared containers")

	infra := &TestInfra{
		Ctx:           context.Background(),
		NATSContainer: suiteNATS,
		NatsURL:       suiteNatsURL,
	}

	var err error
	infra.NC, err = messaging.New(infra.NatsURL)
	Expect(err).ToNot(HaveOccurred())

	DeferCleanup(func() {
		if infra.NC != nil {
			infra.NC.Close()
		}
	})

	return infra
}

// FlushNATS ensures all subscriptions are registered server-side before publishing.
func FlushNATS(nc *messaging.Client) {
	GinkgoHelper()
	Expect(nc.Conn().Flush()).To(Succeed())
}
