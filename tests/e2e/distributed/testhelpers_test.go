package distributed_test

import (
	"context"
	"fmt"
	"net/url"
	"strings"
	"sync/atomic"
	"time"

	"github.com/mudler/LocalAI/core/services/pgbus"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/testcontainers/testcontainers-go"
	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"
	"github.com/testcontainers/testcontainers-go/wait"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	gormlogger "gorm.io/gorm/logger"
)

// TestInfra holds the shared test container and the connection strings derived
// from it.
//
// PGContainer is the SUITE-WIDE container, shared by every spec. Never call
// Terminate or Stop on it from a spec: it ends the run for everything after it.
//
// There is one container and not two. Every carrier these specs exercise is a
// PostgreSQL LISTEN/NOTIFY channel and every worker verb is an HTTP route on a
// tunnel, so a message broker in this suite would be infrastructure no spec and
// no production path can reach.
type TestInfra struct {
	Ctx         context.Context
	PGContainer *tcpostgres.PostgresContainer
	PGURL       string
}

// The container is suite-scoped, not spec-scoped. Starting a Postgres (~10s) per
// spec cost roughly 36 minutes of pure startup across the 213 specs behind
// SetupInfra, which is why this suite was never wired into CI. Isolation now
// comes from a database per spec (~67ms), which is what the dbName argument was
// always describing.
//
// Plain BeforeSuite rather than SynchronizedBeforeSuite is deliberate: under
// `ginkgo -p` each process gets its own container, and a database per spec on
// top of that keeps two processes from reading each other's notifications on a
// channel of the same name.
var (
	suitePG    *tcpostgres.PostgresContainer
	suitePGDSN string
	dbCounter  atomic.Int64
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
})

var _ = AfterSuite(func() {
	ctx := context.Background()
	if suitePG != nil {
		_ = suitePG.Terminate(ctx)
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

// tryAdminDB opens a short-lived connection to the suite's maintenance
// database. Cleanup paths use this rather than adminDB: once connections are
// scarce, a fatal assertion here would convert one Postgres hiccup into a
// suite-wide cascade that buries the original failure.
//
// CREATE/DROP DATABASE cannot run inside a transaction or against the target
// database itself, so every call gets its own connection and closes it.
func tryAdminDB() (*gorm.DB, error) {
	db, err := gorm.Open(postgres.Open(suitePGDSN), &gorm.Config{Logger: gormlogger.Discard})
	if err != nil {
		return nil, fmt.Errorf("connecting to the suite maintenance database: %w", err)
	}
	return db, nil
}

func adminDB() *gorm.DB {
	GinkgoHelper()
	db, err := tryAdminDB()
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

// SetupInfra provisions a dedicated database on the suite-scoped Postgres. Call
// in BeforeEach; cleanup is registered with DeferCleanup. A spec that needs a
// broadcast carrier opens one with Bus().
func SetupInfra(dbName string) *TestInfra {
	GinkgoHelper()
	Expect(suitePG).ToNot(BeNil(), "SetupInfra called before BeforeSuite started the shared container")

	infra := &TestInfra{
		Ctx:         context.Background(),
		PGContainer: suitePG,
	}

	db := fmt.Sprintf("%s_%d", sanitizeDBName(dbName), dbCounter.Add(1))

	// Scoped so a failed CREATE cannot leak the pool: the assertion panics, and a
	// leaked pgx pool per failing spec exhausts the server's connection limit.
	func() {
		admin := adminDB()
		defer closeDB(admin)
		Expect(admin.Exec(fmt.Sprintf("CREATE DATABASE %q", db)).Error).To(Succeed())
	}()

	// Registered immediately after the CREATE, so no later failure in this
	// helper can leave the database behind for the rest of the suite.
	DeferCleanup(func() {
		drop, err := tryAdminDB()
		if err != nil {
			AddReportEntry("drop database skipped", fmt.Sprintf("%s: %v", db, err))
			return
		}
		defer closeDB(drop)
		// FORCE terminates any connection the spec left open (Postgres 13+).
		if err := drop.Exec(fmt.Sprintf("DROP DATABASE IF EXISTS %q WITH (FORCE)", db)).Error; err != nil {
			AddReportEntry("drop database failed", fmt.Sprintf("%s: %v", db, err))
		}
	})

	infra.PGURL = replaceDBName(suitePGDSN, db)

	return infra
}

// Bus opens a broadcast carrier on THIS spec's database.
//
// It is the carrier the job, agent and response families travel on, and it is
// what these specs must build their dispatchers and bridges with. Publishing on
// one carrier while the subscriber reads another is a defect with no error
// anywhere: the publish succeeds and the SSE stream is simply empty, so a spec
// that reached for a message-bus client here would keep passing after
// production had gone silent.
//
// Every call returns a SEPARATE carrier on the same database, so a spec can
// build two and assert across them, which is the shape a deployment has.
func (i *TestInfra) Bus() *pgbus.Bus {
	GinkgoHelper()
	Expect(i.PGURL).ToNot(BeEmpty(), "Bus needs a database; call SetupInfra first")

	db, err := gorm.Open(postgres.Open(i.PGURL), &gorm.Config{Logger: gormlogger.Default.LogMode(gormlogger.Silent)})
	Expect(err).ToNot(HaveOccurred())
	Expect(pgbus.Migrate(i.Ctx, db)).To(Succeed())

	bus, err := pgbus.New(i.Ctx, pgbus.Config{DSN: i.PGURL, DB: db})
	Expect(err).ToNot(HaveOccurred())
	DeferCleanup(bus.Close)
	return bus
}
