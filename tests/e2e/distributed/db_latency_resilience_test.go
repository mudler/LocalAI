package distributed_test

import (
	"context"
	"fmt"
	"net/url"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	pgdriver "gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"

	"github.com/mudler/LocalAI/core/services/nodes"
)

// routerRole is the unprivileged login the router's own handle uses. The
// control plane must be exercised as a role whose reads can actually be
// refused: the test container's owner is a PostgreSQL superuser, and a
// superuser bypasses every privilege check, so a REVOKE against it is recorded
// and then ignored. Injecting the failure through a separate role is the only
// way this spec observes a refusal at all.
const (
	routerRole     = "slot_blind_router"
	routerPassword = "slot_blind"
)

// A control-plane database that fails its queries must cost the cluster
// throughput, never loaded models. Before the eviction guard, a slot lookup
// that failed evicted a healthy model that had done nothing wrong.
var _ = Describe("Control plane under a failing database", Label("Distributed"), func() {
	var (
		infra    *TestInfra
		db       *gorm.DB
		routerDB *gorm.DB
		ctx      context.Context
	)

	openDB := func(dsn string) *gorm.DB {
		GinkgoHelper()
		h, err := gorm.Open(pgdriver.Open(dsn), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
		Expect(err).ToNot(HaveOccurred())
		return h
	}

	// asRole rewrites the container's admin DSN to log in as the unprivileged
	// router role, so the router's connections carry its restrictions.
	asRole := func(adminDSN, role, password string) string {
		GinkgoHelper()
		u, err := url.Parse(adminDSN)
		Expect(err).ToNot(HaveOccurred())
		u.User = url.UserPassword(role, password)
		return u.String()
	}

	BeforeEach(func() {
		infra = SetupInfra("localai_db_latency_test")
		ctx = context.Background()
		db = openDB(infra.PGURL)

		_, err := nodes.NewNodeRegistry(db)
		Expect(err).ToNot(HaveOccurred())

		Expect(db.Create(&nodes.BackendNode{
			ID: "n-keep", Name: "keeper", NodeType: "backend",
			Address: "10.0.0.1:50051", Status: nodes.StatusHealthy,
			LastHeartbeat: time.Now(), MaxReplicasPerModel: 1,
		}).Error).ToNot(HaveOccurred())

		// A loaded model with nothing in flight: the exact row the old code
		// would evict when it could not read the slot table.
		Expect(db.Create(&nodes.NodeModel{
			ID: "nm-victim", NodeID: "n-keep", ModelName: "victim", ReplicaIndex: 0,
			State: "loaded", InFlight: 0, Address: "10.0.0.1:9001",
			LastUsed: time.Now().Add(-time.Hour),
		}).Error).ToNot(HaveOccurred())

		Expect(db.Exec(fmt.Sprintf(
			`CREATE ROLE %s LOGIN PASSWORD '%s'`, routerRole, routerPassword)).Error).ToNot(HaveOccurred())
		for _, grant := range []string{
			fmt.Sprintf(`GRANT USAGE ON SCHEMA public TO %s`, routerRole),
			fmt.Sprintf(`GRANT ALL PRIVILEGES ON ALL TABLES IN SCHEMA public TO %s`, routerRole),
			fmt.Sprintf(`GRANT ALL PRIVILEGES ON ALL SEQUENCES IN SCHEMA public TO %s`, routerRole),
		} {
			Expect(db.Exec(grant).Error).ToNot(HaveOccurred())
		}

		routerDB = openDB(asRole(infra.PGURL, routerRole, routerPassword))

		// Registered here rather than next to the REVOKE so the restore is
		// unconditional: it runs whether the body revokes anything or not, and
		// whether the body returns, fails an assertion or panics. A spec that
		// aborts midway must not hand the next one a role that cannot read.
		// A table-level grant covers every column, so this also supersedes the
		// per-column grants the revoke leaves behind.
		DeferCleanup(func() {
			_ = db.Exec(fmt.Sprintf(`GRANT SELECT ON node_models TO %s`, routerRole)).Error
		})
	})

	// revokeSlotReads takes away the router's ability to read the replica-slot
	// column, and only that column.
	//
	// The scope matters. Revoking the whole node_models table would break node
	// selection too, which runs first and has a guard of its own, so the
	// scheduler would never reach the slot lookup this spec is about. Column
	// scope lets selection succeed (it reads node_id, state and in_flight, and
	// counts rows) and lands the refusal exactly on NextFreeReplicaIndex, which
	// plucks replica_index. A table-level grant covers every column, so the
	// grant has to be re-issued column by column rather than carved out with a
	// column-level REVOKE, which PostgreSQL ignores while the table grant
	// stands.
	revokeSlotReads := func() {
		GinkgoHelper()
		Expect(db.Exec(fmt.Sprintf(`REVOKE SELECT ON node_models FROM %s`, routerRole)).Error).ToNot(HaveOccurred())
		Expect(db.Exec(fmt.Sprintf(`DO $$
DECLARE cols text;
BEGIN
  SELECT string_agg(quote_ident(column_name), ', ') INTO cols
    FROM information_schema.columns
   WHERE table_schema = 'public' AND table_name = 'node_models'
     AND column_name <> 'replica_index';
  EXECUTE format('GRANT SELECT (%%s) ON public.node_models TO %s', cols);
END $$`, routerRole)).Error).ToNot(HaveOccurred())
	}

	It("does not evict a loaded model when the database refuses the slot lookup", func() {
		// Built before the revoke: AutoMigrate inspects the schema, and a
		// migration that cannot run would degrade this spec into asserting
		// almost nothing.
		routerRegistry, err := nodes.NewNodeRegistry(routerDB)
		Expect(err).ToNot(HaveOccurred())

		revokeSlotReads()

		// Precondition: the injection actually bites. Without this the spec
		// could pass against a database that answers every query.
		_, slotErr := routerRegistry.NextFreeReplicaIndex(ctx, "n-keep", "newcomer", 1)
		Expect(slotErr).To(HaveOccurred(), "the slot lookup must be refused for this spec to mean anything")
		Expect(slotErr).ToNot(MatchError(nodes.ErrNoFreeSlot), "a refusal is not evidence that the node is full")

		router := nodes.NewSmartRouter(routerRegistry, nodes.SmartRouterOptions{DB: routerDB})
		_, routeErr := router.Route(ctx, "newcomer", "models/newcomer.gguf", "llama-cpp", "", nil, false)

		Expect(routeErr).To(HaveOccurred(), "a failing database cannot produce a successful placement")

		// These two carry the regression. Removing the guard makes the router
		// fall through to eviction, and the message becomes "no replica slot on
		// keeper and eviction failed", which trips both of them.
		Expect(routeErr.Error()).To(ContainSubstring("determining free replica slot"),
			"the failure must name the lookup that could not be answered")
		Expect(routeErr.Error()).ToNot(ContainSubstring("eviction failed"),
			"a failed lookup is not evidence that the node is full")

		// The row assertions below state the property the guard exists to
		// protect, but under this particular injection they cannot fail on
		// their own: the eviction path selects whole node_models rows, so the
		// same revoke blinds it too and it deletes nothing. They are kept as
		// the statement of intent, and would catch a regression that reaches a
		// working eviction. The message assertions above are what actually
		// pins the guard.
		var victim nodes.NodeModel
		Expect(db.First(&victim, "node_id = ? AND model_name = ?", "n-keep", "victim").Error).
			ToNot(HaveOccurred(), "the loaded model must have survived the database outage")
		Expect(victim.State).To(Equal("loaded"))

		var keeper nodes.BackendNode
		Expect(db.First(&keeper, "id = ?", "n-keep").Error).ToNot(HaveOccurred())
		Expect(keeper.Status).To(Equal(nodes.StatusHealthy),
			"a database failure must not change a node's liveness")
	})
})
