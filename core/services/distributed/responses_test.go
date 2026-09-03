package distributed_test

import (
	"context"
	"regexp"
	"sync"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	gormlogger "gorm.io/gorm/logger"

	"github.com/mudler/LocalAI/core/services/distributed"
	"github.com/mudler/LocalAI/core/services/testutil"
)

// responseSQLRecorder captures the statements a call under test issues, so a
// spec can pin the SHAPE of one rather than only its effect.
//
// The shape is what has to be pinned for anything measured on the database
// clock. The test container shares this host's clock, so a Go-side cutoff and
// now() agree to the microsecond and no behavioural spec can tell them apart;
// the difference only shows up in a deployment whose replicas' clocks differ,
// which is every real one.
type responseSQLRecorder struct {
	gormlogger.Interface
	mu         sync.Mutex
	statements []string
	errs       []error
}

func newResponseSQLRecorder() *responseSQLRecorder {
	return &responseSQLRecorder{Interface: gormlogger.Default.LogMode(gormlogger.Silent)}
}

func (r *responseSQLRecorder) Trace(ctx context.Context, begin time.Time, fc func() (string, int64), err error) {
	sql, rows := fc()
	r.mu.Lock()
	r.statements = append(r.statements, sql)
	if err != nil {
		r.errs = append(r.errs, err)
	}
	r.mu.Unlock()
	// Delegated so a failing statement is still reported the way gorm would
	// report it: the instrument used to prove what the SQL does must not be the
	// one thing that hides it erroring.
	r.Interface.Trace(ctx, begin, func() (string, int64) { return sql, rows }, err)
}

// reset forgets everything recorded so far, so a spec can ignore the migration
// and advisory-lock traffic the constructor issues and pin only the call under
// test.
func (r *responseSQLRecorder) reset() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.statements = nil
	r.errs = nil
}

// only returns the single recorded statement, failing the spec if the call under
// test issued anything other than exactly one. A read-then-delete shape shows up
// here as two.
func (r *responseSQLRecorder) only() string {
	r.mu.Lock()
	defer r.mu.Unlock()
	ExpectWithOffset(1, r.errs).To(BeEmpty(), "the recorded statement failed")
	ExpectWithOffset(1, r.statements).To(HaveLen(1), "expected exactly one statement, got: %v", r.statements)
	return r.statements[0]
}

var _ = Describe("ResponseMetadataStore", func() {
	var (
		db    *gorm.DB
		store *distributed.ResponseMetadataStore
		ctx   context.Context
	)

	newRecord := func(id string, expiresAt *time.Time) *distributed.ResponseMetadataRecord {
		return &distributed.ResponseMetadataRecord{
			ID:           id,
			OwnerReplica: "replica-a",
			Owner:        "user-1",
			PayloadJSON:  []byte(`{"id":"` + id + `"}`),
			ExpiresAt:    expiresAt,
		}
	}

	ids := func(recs []distributed.ResponseMetadataRecord) []string {
		out := make([]string, 0, len(recs))
		for i := range recs {
			out = append(out, recs[i].ID)
		}
		return out
	}

	BeforeEach(func() {
		ctx = context.Background()
		db = testutil.SetupTestDB()
		var err error
		store, err = distributed.NewResponseMetadataStore(db)
		Expect(err).ToNot(HaveOccurred())
	})

	Describe("construction", func() {
		It("migrates and is idempotent when called twice on the same handle", func() {
			second, err := distributed.NewResponseMetadataStore(db)
			Expect(err).ToNot(HaveOccurred())
			Expect(second).ToNot(BeNil())

			// The second construction must have left the first store's data
			// alone: a re-run AutoMigrate that dropped and recreated the table
			// would pass a "no error" assertion and lose every live response.
			Expect(store.Upsert(ctx, newRecord("resp_idempotent", nil))).To(Succeed())
			recs, err := second.ListUnexpired(ctx)
			Expect(err).ToNot(HaveOccurred())
			Expect(ids(recs)).To(ConsistOf("resp_idempotent"))
		})

		It("refuses a SQLite handle by naming the dialect", func() {
			lite, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{Logger: gormlogger.Discard})
			Expect(err).ToNot(HaveOccurred())

			_, err = distributed.NewResponseMetadataStore(lite)
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("sqlite"))
			Expect(err.Error()).To(ContainSubstring("PostgreSQL"))
		})
	})

	Describe("InitStores", func() {
		It("builds a working response metadata store", func() {
			// Pins the wiring: without it InitStores compiles, every other store
			// works, and the map that needed this one silently falls back to
			// nothing to re-hydrate from.
			stores, err := distributed.InitStores(db)
			Expect(err).ToNot(HaveOccurred())
			Expect(stores.Responses).ToNot(BeNil())

			Expect(stores.Responses.Upsert(ctx, newRecord("resp_from_init", nil))).To(Succeed())
			recs, err := stores.Responses.ListUnexpired(ctx)
			Expect(err).ToNot(HaveOccurred())
			Expect(ids(recs)).To(ConsistOf("resp_from_init"))
		})
	})

	Describe("Upsert", func() {
		It("leaves one row carrying the second payload", func() {
			first := newRecord("resp_upsert", nil)
			Expect(store.Upsert(ctx, first)).To(Succeed())

			second := newRecord("resp_upsert", nil)
			second.PayloadJSON = []byte(`{"id":"resp_upsert","status":"completed"}`)
			second.OwnerReplica = "replica-b"
			Expect(store.Upsert(ctx, second)).To(Succeed())

			recs, err := store.ListUnexpired(ctx)
			Expect(err).ToNot(HaveOccurred())
			Expect(recs).To(HaveLen(1))
			Expect(string(recs[0].PayloadJSON)).To(Equal(`{"id":"resp_upsert","status":"completed"}`))
			Expect(recs[0].OwnerReplica).To(Equal("replica-b"))
		})

		It("refuses a record with no id rather than writing an anonymous row", func() {
			Expect(store.Upsert(ctx, newRecord("", nil))).ToNot(Succeed())
		})
	})

	Describe("Delete", func() {
		It("removes the row", func() {
			Expect(store.Upsert(ctx, newRecord("resp_delete", nil))).To(Succeed())

			Expect(store.Delete(ctx, "resp_delete")).To(Succeed())

			recs, err := store.ListUnexpired(ctx)
			Expect(err).ToNot(HaveOccurred())
			Expect(recs).To(BeEmpty())
		})
	})

	Describe("ListUnexpired", func() {
		It("returns rows that never expire and rows still in the future, and not one already expired", func() {
			future := time.Now().Add(time.Hour)
			past := time.Now().Add(-time.Hour)

			Expect(store.Upsert(ctx, newRecord("resp_forever", nil))).To(Succeed())
			Expect(store.Upsert(ctx, newRecord("resp_future", &future))).To(Succeed())
			Expect(store.Upsert(ctx, newRecord("resp_past", &past))).To(Succeed())

			recs, err := store.ListUnexpired(ctx)
			Expect(err).ToNot(HaveOccurred())
			Expect(ids(recs)).To(ConsistOf("resp_forever", "resp_future"))
		})

		It("reports an unreachable database as an error and never as an empty list", func() {
			// A missing row and an unreachable database are different facts. If
			// this returned (nil, nil) a hydrate would replace the whole map
			// with nothing and every response on this replica would 404 for as
			// long as the outage lasted.
			sqlDB, err := db.DB()
			Expect(err).ToNot(HaveOccurred())
			Expect(sqlDB.Close()).To(Succeed())

			recs, err := store.ListUnexpired(ctx)
			Expect(err).To(HaveOccurred())
			Expect(recs).To(BeNil())
		})
	})

	Describe("PurgeExpired", func() {
		It("deletes exactly the expired row and reports nothing left to do on a second call", func() {
			future := time.Now().Add(time.Hour)
			past := time.Now().Add(-time.Hour)

			Expect(store.Upsert(ctx, newRecord("resp_forever", nil))).To(Succeed())
			Expect(store.Upsert(ctx, newRecord("resp_future", &future))).To(Succeed())
			Expect(store.Upsert(ctx, newRecord("resp_past", &past))).To(Succeed())

			n, err := store.PurgeExpired(ctx)
			Expect(err).ToNot(HaveOccurred())
			Expect(n).To(Equal(int64(1)))

			recs, err := store.ListUnexpired(ctx)
			Expect(err).ToNot(HaveOccurred())
			Expect(ids(recs)).To(ConsistOf("resp_forever", "resp_future"))

			n, err = store.PurgeExpired(ctx)
			Expect(err).ToNot(HaveOccurred())
			Expect(n).To(Equal(int64(0)))
		})

		It("reports an unreachable database as an error and never as zero rows purged", func() {
			sqlDB, err := db.DB()
			Expect(err).ToNot(HaveOccurred())
			Expect(sqlDB.Close()).To(Succeed())

			_, err = store.PurgeExpired(ctx)
			Expect(err).To(HaveOccurred())
		})
	})

	// The expiry cutoff is the database's clock, not the asking process's. The
	// container shares this host's clock, so no behavioural spec above can tell
	// a Go-side time.Now() bind parameter from now(); these pin the statement
	// text instead.
	Describe("statement shape", func() {
		var rec *responseSQLRecorder

		BeforeEach(func() {
			rec = newResponseSQLRecorder()
			var err error
			store, err = distributed.NewResponseMetadataStore(db.Session(&gorm.Session{Logger: rec}))
			Expect(err).ToNot(HaveOccurred())
			rec.reset()
		})

		It("compares expires_at against the database clock in ListUnexpired", func() {
			_, err := store.ListUnexpired(ctx)
			Expect(err).ToNot(HaveOccurred())

			sql := rec.only()
			Expect(sql).To(MatchRegexp(`(?i)expires_at\s+IS\s+NULL\s+OR\s+expires_at\s*>\s*now\(\)`))
			// A bound parameter in the comparison position is a Go-side cutoff
			// wearing the same behaviour; gorm's logger explains binds into the
			// text, so a time literal here is exactly that defect.
			Expect(sql).ToNot(MatchRegexp(`expires_at\s*>\s*['$]`))
		})

		It("compares expires_at against the database clock in PurgeExpired", func() {
			_, err := store.PurgeExpired(ctx)
			Expect(err).ToNot(HaveOccurred())

			sql := rec.only()
			Expect(sql).To(MatchRegexp(`(?i)expires_at\s+IS\s+NOT\s+NULL\s+AND\s+expires_at\s*<=\s*now\(\)`))
			Expect(sql).ToNot(MatchRegexp(`expires_at\s*<=\s*['$]`))
		})

		It("issues one statement per call, so neither reads the clock into Go first", func() {
			_, err := store.ListUnexpired(ctx)
			Expect(err).ToNot(HaveOccurred())
			Expect(rec.only()).ToNot(BeEmpty())

			rec.reset()
			_, err = store.PurgeExpired(ctx)
			Expect(err).ToNot(HaveOccurred())
			Expect(regexp.MustCompile(`(?i)^\s*delete`).MatchString(rec.only())).To(BeTrue())
		})
	})
})
