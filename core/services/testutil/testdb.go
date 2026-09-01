package testutil

import (
	"context"
	"fmt"
	"net/url"
	"runtime"
	"sync"
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

// One PostgreSQL container per test PROCESS, not per spec, with a database per
// SetupTestDB call.
//
// Starting a container per spec was both slow and flaky. Slow because a
// postgres:16 start is seconds and the packages behind this helper hold several
// hundred specs; flaky because every start was a fresh chance to miss the
// readiness deadline, and a miss lands in the caller's BeforeEach as a failure
// of whichever spec happened to be running. That is the exact shape of the
// intermittent single-spec failure seen twice in this package and never
// reproduced: one spec of many, no pattern, never twice in the same place.
// Starting the container once per process leaves one chance to miss it instead
// of one per spec, and moves that chance onto a deadline that only has to be met
// while nothing else is competing for the machine.
//
// Isolation is unchanged and is what callers actually depend on: each call still
// hands back an empty database that no other spec can see. The database is
// dropped when the spec that asked for it ends. Advisory locks, sequences and
// extensions are all per-database in PostgreSQL, so nothing the packages behind
// this helper rely on leaks between specs.
//
// This mirrors the pattern already proven in tests/e2e/distributed
// (testhelpers_test.go), which is where the argument and the measurements come
// from.
//
// One container per process rather than one shared across `ginkgo -p` workers is
// deliberate: parallel Ginkgo processes are separate OS processes, each gets its
// own container, and nothing has to coordinate database names across them.
var (
	sharedOnce sync.Once
	sharedPG   *tcpostgres.PostgresContainer
	sharedDSN  string
	sharedErr  error

	// dbCounter makes each database name unique within this process. The
	// container is not shared across processes, so a process-local counter is
	// enough.
	dbCounter atomic.Int64
)

// The container outlives every spec, so its teardown belongs to the suite. This
// registers one AfterSuite in every suite that imports this package, which is
// every suite that could have started a container; it is a no-op in the ones
// that never call SetupTestDB.
//
// Package-level rather than something callers have to remember: a helper whose
// cleanup depends on 56 test files each declaring a hook is a helper that leaks
// containers the first time someone forgets. Registration happens during package
// initialisation, which is before RunSpecs, so Ginkgo is still building its tree.
var _ = AfterSuite(func() {
	if sharedPG == nil {
		return
	}
	// Best-effort: a failed terminate must not fail a suite whose specs all
	// passed. Testcontainers' reaper removes it in that case.
	_ = sharedPG.Terminate(context.Background())
})

// sharedPostgres returns the DSN of this process's PostgreSQL container,
// starting it on first use.
//
// The error is remembered rather than only asserted inside the sync.Once: an
// assertion there fails the one spec that happened to be first, and every later
// spec would then find a nil container and fail for some unrelated-looking
// reason. Re-asserting the stored error makes every affected spec say the same
// true thing.
func sharedPostgres() string {
	GinkgoHelper()

	sharedOnce.Do(func() {
		ctx := context.Background()
		sharedPG, sharedErr = tcpostgres.Run(ctx, "postgres:16",
			tcpostgres.WithDatabase("testdb"),
			tcpostgres.WithUsername("test"),
			tcpostgres.WithPassword("test"),
			// The deadline is per process now, not per spec, so it is generous
			// on purpose: it is paid once, and the cost of missing it is a
			// whole suite rather than one spec.
			testcontainers.WithWaitStrategyAndDeadline(120*time.Second,
				wait.ForLog("database system is ready to accept connections").WithOccurrence(2)),
		)
		if sharedErr != nil {
			return
		}
		sharedDSN, sharedErr = sharedPG.ConnectionString(ctx, "sslmode=disable")
	})

	Expect(sharedErr).ToNot(HaveOccurred(), "the suite's PostgreSQL container could not be started")
	return sharedDSN
}

// SetupTestDB returns a gorm.DB on a PostgreSQL database created for the calling
// spec. The database is dropped, and its connection pool closed, when the spec
// ends.
func SetupTestDB() *gorm.DB {
	GinkgoHelper()
	if runtime.GOOS == "darwin" {
		Skip("testcontainers requires Docker, not available on macOS CI")
	}

	dsn := sharedPostgres()
	name := fmt.Sprintf("testdb_%d", dbCounter.Add(1))

	// Scoped so a failed CREATE cannot leak the pool: the assertion panics out
	// of this function, and a leaked pool per failing spec exhausts the
	// server's connection limit for every spec after it.
	//
	// CREATE and DROP DATABASE cannot run against the target database itself,
	// so both go through a short-lived connection to the container's own
	// maintenance database.
	func() {
		admin := openPool(dsn)
		defer closePool(admin)
		Expect(admin.Exec(fmt.Sprintf("CREATE DATABASE %q", name)).Error).To(Succeed())
	}()

	db, err := gorm.Open(postgres.Open(replaceDBName(dsn, name)), &gorm.Config{
		Logger: logger.Default.LogMode(logger.Silent),
	})
	Expect(err).ToNot(HaveOccurred())

	DeferCleanup(func() {
		// The caller's own DeferCleanups were registered later and so run
		// first, which is what lets a spec keep using this handle in its
		// teardown.
		closePool(db)

		drop, err := openTolerantPool(dsn)
		if err != nil {
			// Reported, never asserted. A cleanup that fails the spec turns one
			// database hiccup into a failure that buries whatever the spec was
			// actually about.
			AddReportEntry("drop test database skipped", fmt.Sprintf("%s: %v", name, err))
			return
		}
		defer closePool(drop)
		// FORCE terminates whatever connections the spec left open, including
		// any a background goroutine is still holding (PostgreSQL 13+).
		if err := drop.Exec(fmt.Sprintf("DROP DATABASE IF EXISTS %q WITH (FORCE)", name)).Error; err != nil {
			AddReportEntry("drop test database failed", fmt.Sprintf("%s: %v", name, err))
		}
	})

	return db
}

// maintenanceDSN is dsn with every server-side timeout disabled as a CONNECTION
// STARTUP OPTION rather than as a statement.
//
// The timeouts have to go because CREATE DATABASE and DROP DATABASE must not be
// bounded by anything a spec configured. A spec that sets a short
// statement_timeout on ITS own database cannot reach this connection, but a spec
// that names the maintenance database by mistake can, and that is not
// hypothetical: two advisory-lock specs did exactly that.
//
// Clearing it with `SET statement_timeout = 0` on an already-open connection is
// circular and was a real defect here: that connection has already inherited the
// database's bound, so the statement that clears the bound runs under it and can
// be aborted by it with SQLSTATE 57014. It failed roughly once in fifty at
// 8-way concurrency, which is the same invisible load-dependent single-spec
// flake this helper exists to remove. A startup option removes the circularity
// instead of buying headroom against it: the value is delivered in the startup
// packet, so the connection is already unbounded before it can run anything.
//
// The route is verified in the driver rather than assumed. pgx puts every URL
// query parameter into settings (pgconn/config.go:614), `options` is absent from
// notRuntimeParams (pgconn/config.go:340-362) so it becomes a runtime parameter
// (pgconn/config.go:374-378), and runtime parameters are copied into the startup
// message (pgconn/pgconn.go:382-388). PostgreSQL treats `options` as backend
// command-line switches, so `-c statement_timeout=0` is applied before the
// session accepts a query.
func maintenanceDSN(dsn string) (string, error) {
	u, err := url.Parse(dsn)
	if err != nil {
		return "", err
	}
	q := u.Query()
	// Percent-encoded by Encode, and pgx decodes query values before they reach
	// settings, so the server receives the switches with their spaces intact.
	q.Set("options", "-c statement_timeout=0 -c lock_timeout=0")
	u.RawQuery = q.Encode()
	return u.String(), nil
}

// openPool connects to the maintenance database with logging off and no
// server-side timeouts. Used for the short-lived maintenance connections only;
// the database a spec is handed keeps gorm's silent logger and the server's
// defaults, because setting timeouts on it is a thing specs do on purpose.
func openPool(dsn string) *gorm.DB {
	GinkgoHelper()
	db, err := openTolerantPool(dsn)
	Expect(err).ToNot(HaveOccurred())
	return db
}

// openTolerantPool is openPool for the cleanup path, which must report a
// failure rather than assert one: an assertion here would fail a spec that had
// already passed, and bury whatever the next real failure was.
//
// It carries the same startup options, and the DROP is the statement that most
// needs them: FORCE waits on terminating other sessions, measured at up to 169ms
// against the 300ms bound that used to leak here, and a DROP aborted mid-way is
// swallowed and leaks a database.
func openTolerantPool(dsn string) (*gorm.DB, error) {
	maintenance, err := maintenanceDSN(dsn)
	if err != nil {
		return nil, err
	}
	db, err := gorm.Open(postgres.Open(maintenance), &gorm.Config{Logger: logger.Discard})
	if err != nil {
		return nil, err
	}
	return db, nil
}

func closePool(db *gorm.DB) {
	if db == nil {
		return
	}
	if sqlDB, err := db.DB(); err == nil {
		_ = sqlDB.Close()
	}
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
