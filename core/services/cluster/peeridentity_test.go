package cluster_test

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"strings"

	"github.com/mudler/LocalAI/core/services/cluster"
	"github.com/mudler/LocalAI/core/services/testutil"

	"github.com/labstack/echo/v4"
	"github.com/libp2p/go-yamux/v5"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"gorm.io/gorm"
)

// These specs drive the PRODUCTION handler over a real WebSocket and a real
// yamux session, because the defect they pin is one a double cannot have: the
// id a peer declares is a query parameter on a transport, and whether it is
// checked is decided in the same place the connection is hijacked.
var _ = Describe("Peer identity", func() {
	var (
		db       *gorm.DB
		reg      *cluster.Registry
		store    *cluster.SessionStore
		srv      *httptest.Server
		ctx      context.Context
		realCred cluster.PeerCredential
	)

	// The victim. "real" is a legitimate replica with a published credential;
	// everything below is an attempt to be it.
	const realID = "real-replica"

	// The deployment-wide secret. Every worker holds it, and every spec below
	// that is meant to fail holds it too: the whole point is that holding it is
	// no longer enough.
	const clusterToken = "shared-cluster-token"

	BeforeEach(func() {
		ctx = context.Background()
		db = testutil.SetupTestDB()
		Expect(cluster.Migrate(ctx, db)).To(Succeed())
		reg = cluster.NewRegistry(db)

		// A store with no relay: every accepted stream is closed at once, which
		// is enough to observe WHICH links exist without standing up a worker.
		store = cluster.NewSessionStore(nil)
		DeferCleanup(store.CloseAll)

		e := echo.New()
		servePeerRoute(e, clusterToken, reg, store.Accept)
		srv = httptest.NewServer(e)
		DeferCleanup(srv.Close)

		realCred = cluster.NewPeerCredential()
		Expect(reg.Register(ctx, realID, "10.0.0.7:8080", "test", realCred.Hash())).To(Succeed())
		// The replica being dialled needs a row of its own, since the pool
		// resolves the address it dials out of the table.
		Expect(reg.Register(ctx, "listener", strings.TrimPrefix(srv.URL, "http://"), "test", "")).To(Succeed())
	})

	// dialAs opens a link to the listening replica, declaring id and presenting
	// cred. It returns the pool so a spec can assert on what the link did.
	dialAs := func(id string, cred cluster.PeerCredential) (*cluster.PeerPool, error) {
		pool := cluster.NewPeerPool(id, clusterToken, cred, reg)
		DeferCleanup(pool.Close)
		st, err := pool.Open(ctx, "listener")
		if err == nil {
			DeferCleanup(func() { _ = st.Close() })
		}
		return pool, err
	}

	It("accepts a replica that proves the id it declares", func() {
		_, err := dialAs(realID, realCred)
		Expect(err).ToNot(HaveOccurred())
		Eventually(func() bool {
			_, held := store.Get(realID)
			return held
		}, "10s").Should(BeTrue())
	})

	It("refuses a dialler that declares another replica's id while holding the shared cluster token", func() {
		// THE attack this whole change exists for. The impostor has everything
		// a worker has: the deployment's registration token, and the id of a
		// replica it read out of any log line or API response. Before per
		// replica credentials that was the whole of the check, so this dial
		// succeeded and the link it opened relayed to every worker tunnel the
		// listening replica owns.
		//
		// It is the case the cheap patch does not cover: validating the id
		// against the instances table passes here, because the id IS in the
		// table. Only a secret the impostor does not have refuses it.
		_, err := dialAs(realID, cluster.NewPeerCredential())
		Expect(err).To(MatchError(cluster.ErrPeerRejected))
		Expect(err).ToNot(MatchError(cluster.ErrInstanceNotFound),
			"a refused peer is an authorization failure; read as absence it makes the scheduler reap rows and evict models")
		Consistently(func() bool {
			_, held := store.Get(realID)
			return held
		}, "1s", "100ms").Should(BeFalse())
	})

	It("does not let an impostor evict the link a real replica already holds", func() {
		// Displacement, which is a denial of service rather than an
		// eavesdrop: SessionStore keeps ONE link per peer id, so a dial that is
		// accepted under an id closes whatever was there. Repeated at will, it
		// keeps a replica's inbound link permanently broken.
		_, err := dialAs(realID, realCred)
		Expect(err).ToNot(HaveOccurred())

		var held *yamux.Session
		Eventually(func() bool {
			sess, ok := store.Get(realID)
			held = sess
			return ok
		}, "10s").Should(BeTrue())

		_, err = dialAs(realID, cluster.NewPeerCredential())
		Expect(err).To(MatchError(cluster.ErrPeerRejected))

		// The same session object, still alive. A refused dial that had reached
		// Accept would have replaced this entry and closed what it replaced.
		current, ok := store.Get(realID)
		Expect(ok).To(BeTrue())
		Expect(current).To(BeIdenticalTo(held))
		Expect(current.IsClosed()).To(BeFalse(),
			"an impostor closed the real replica's inbound link")
	})

	It("refuses a dialler whose declared replica has no published credential", func() {
		// A replica registered by a release that predates peer identity. Its
		// row carries an empty hash, and empty must authorize NOBODY: read as
		// "no restriction" it would leave every not-yet-upgraded replica
		// impersonable by anything holding the shared token, which is the
		// exposure rather than a step towards closing it.
		Expect(reg.Register(ctx, "legacy-replica", "10.0.0.8:8080", "test", "")).To(Succeed())
		_, err := dialAs("legacy-replica", cluster.NewPeerCredential())
		Expect(err).To(MatchError(cluster.ErrPeerRejected))
	})

	It("refuses a dialler holding a credential that matches no replica at all", func() {
		// The zero value is a credential nobody may use. A pool built without
		// one presents an empty token, and an empty token must not match an
		// empty stored hash either.
		Expect(reg.Register(ctx, "blank-replica", "10.0.0.9:8080", "test", "")).To(Succeed())
		_, err := dialAs("blank-replica", cluster.PeerCredential{})
		Expect(err).To(MatchError(cluster.ErrPeerRejected))
	})

	It("refuses a dialler that declares an id no replica holds", func() {
		_, err := dialAs("invented-replica", cluster.NewPeerCredential())
		Expect(err).To(MatchError(cluster.ErrPeerRejected))
		Expect(err).ToNot(MatchError(cluster.ErrInstanceNotFound),
			"the peer answered and refused; nothing here says a worker went away")
	})

	It("still requires the shared cluster token from a replica that can prove its identity", func() {
		// Identity is added to the existing check, not swapped for it. A
		// dialler with a perfectly good credential and the wrong deployment
		// token belongs to another deployment.
		pool := cluster.NewPeerPool(realID, "not-the-cluster-token", realCred, reg)
		DeferCleanup(pool.Close)
		_, err := pool.Open(ctx, "listener")
		Expect(err).To(MatchError(cluster.ErrPeerRejected))
		Expect(err).ToNot(MatchError(cluster.ErrInstanceNotFound))
	})

	It("reports every refusal as unreachable too, so no existing consumer changes behaviour", func() {
		// ErrPeerUnreachable has named "the peer refused the credentials" since
		// it was written, and every consumer already treats it as a retryable
		// transport condition. ErrPeerRejected is added UNDERNEATH that rather
		// than in place of it, so gaining a distinguishable authorization
		// failure cannot silently re-route one existing caller.
		_, err := dialAs(realID, cluster.NewPeerCredential())
		Expect(err).To(MatchError(cluster.ErrPeerUnreachable))
		Expect(err).To(MatchError(cluster.ErrPeerRejected))
		Expect(err).ToNot(MatchError(cluster.ErrInstanceNotFound))
		Expect(err).ToNot(MatchError(cluster.ErrNoConnection))
	})
})

var _ = Describe("Peer credential", func() {
	It("publishes only a hash and never the secret", func() {
		cred := cluster.NewPeerCredential()
		Expect(cred.Token()).ToNot(BeEmpty())
		Expect(cred.Hash()).To(Equal(cluster.HashPeerToken(cred.Token())))
		Expect(cred.Hash()).ToNot(Equal(cred.Token()))
		Expect(cred.Hash()).To(HaveLen(64))
	})

	It("mints a different secret every time", func() {
		Expect(cluster.NewPeerCredential().Token()).ToNot(Equal(cluster.NewPeerCredential().Token()))
	})

	It("treats an empty stored hash as permission for nobody", func() {
		// The trap this programme hit twice: an empty allow list read as
		// unrestricted. An unpublished credential authorizes nothing, not
		// everything, and an empty presented token is not a match for it.
		Expect(cluster.PeerTokenMatches("anything", "")).To(BeFalse())
		Expect(cluster.PeerTokenMatches("", "")).To(BeFalse())
		Expect(cluster.PeerTokenMatches("", cluster.HashPeerToken("secret"))).To(BeFalse())
	})

	It("is not serialised out of an instance row", func() {
		// The column holds a secret's hash, and Instance carries json tags for
		// every other field. Nothing marshals it today; the tag is what keeps
		// that safe the first time something does.
		cred := cluster.NewPeerCredential()
		out, err := json.Marshal(cluster.Instance{ID: "inst-a", PeerTokenHash: cred.Hash()})
		Expect(err).ToNot(HaveOccurred())
		Expect(string(out)).ToNot(ContainSubstring(cred.Hash()))
		Expect(string(out)).ToNot(ContainSubstring("peer_token_hash"))
	})

	It("matches only the secret behind the hash", func() {
		Expect(cluster.PeerTokenMatches("secret", cluster.HashPeerToken("secret"))).To(BeTrue())
		Expect(cluster.PeerTokenMatches("secret ", cluster.HashPeerToken("secret"))).To(BeFalse())
		Expect(cluster.PeerTokenMatches(cluster.HashPeerToken("secret"), cluster.HashPeerToken("secret"))).To(BeFalse())
	})
})

var _ = Describe("Instance registration identity", func() {
	var (
		db  *gorm.DB
		reg *cluster.Registry
		ctx context.Context
	)

	BeforeEach(func() {
		ctx = context.Background()
		db = testutil.SetupTestDB()
		Expect(cluster.Migrate(ctx, db)).To(Succeed())
		reg = cluster.NewRegistry(db)
	})

	It("publishes the credential hash in the same write as the address", func() {
		cred := cluster.NewPeerCredential()
		Expect(reg.Register(ctx, "inst-a", "10.0.0.1:8080", "v1", cred.Hash())).To(Succeed())

		got, err := reg.Get(ctx, "inst-a")
		Expect(err).ToNot(HaveOccurred())
		Expect(got.PeerTokenHash).To(Equal(cred.Hash()))
		Expect(got.PeerTokenHash).ToNot(Equal(cred.Token()),
			"the plaintext must never reach the table")
	})

	It("replaces the published hash when a replica re-registers", func() {
		first := cluster.NewPeerCredential()
		second := cluster.NewPeerCredential()
		Expect(reg.Register(ctx, "inst-a", "10.0.0.1:8080", "v1", first.Hash())).To(Succeed())
		Expect(reg.Register(ctx, "inst-a", "10.0.0.1:8080", "v1", second.Hash())).To(Succeed())

		got, err := reg.Get(ctx, "inst-a")
		Expect(err).ToNot(HaveOccurred())
		Expect(got.PeerTokenHash).To(Equal(second.Hash()),
			"a restarted replica mints a new secret; a row that kept the old hash would refuse its own dials")
	})

	It("keeps a heartbeat from clearing the published hash", func() {
		// Heartbeat writes last_seen and nothing else. A heartbeat that reset
		// the column would make every replica unpeerable five seconds after it
		// started, which no spec that only registers would ever see.
		cred := cluster.NewPeerCredential()
		Expect(reg.Register(ctx, "inst-a", "10.0.0.1:8080", "v1", cred.Hash())).To(Succeed())
		Expect(reg.Heartbeat(ctx, "inst-a")).To(Succeed())

		got, err := reg.Get(ctx, "inst-a")
		Expect(err).ToNot(HaveOccurred())
		Expect(got.PeerTokenHash).To(Equal(cred.Hash()))
	})
})

var _ = Describe("Membership identity", func() {
	It("republishes the credential after another replica reaped this row", func() {
		// The heartbeat path, which is a SECOND writer of the identity and the
		// one nothing else covers. A replica stalled long enough is reaped by a
		// peer; its next tick finds no row, re-registers, and must publish the
		// same credential it is still dialling with. A re-registration that
		// wrote an empty hash would leave a replica that is up, heartbeating
		// and refused by every peer it dials, with nothing naming the cause.
		ctx := context.Background()
		db := testutil.SetupTestDB()
		Expect(cluster.Migrate(ctx, db)).To(Succeed())
		reg := cluster.NewRegistry(db)

		cred := cluster.NewPeerCredential()
		m := cluster.NewMembership(reg, "me", "10.0.0.1:8080", "v1", cred)
		Expect(m.Start(ctx)).To(Succeed())
		DeferCleanup(m.Stop)

		// What a peer's sweep leaves behind.
		Expect(db.WithContext(ctx).Where("id = ?", "me").Delete(&cluster.Instance{}).Error).To(Succeed())

		Eventually(func() (string, error) {
			got, err := reg.Get(ctx, "me")
			if err != nil {
				return "", err
			}
			return got.PeerTokenHash, nil
		}, "20s", "250ms").Should(Equal(cred.Hash()),
			"the replica rebuilt its row without the identity peers check it by")
	})

	It("publishes the credential it was built with", func() {
		// The membership loop is the ONLY writer of this replica's row, so if
		// it does not carry the hash there is nothing for a peer to check and
		// every dial this replica makes is refused.
		ctx := context.Background()
		db := testutil.SetupTestDB()
		Expect(cluster.Migrate(ctx, db)).To(Succeed())
		reg := cluster.NewRegistry(db)

		cred := cluster.NewPeerCredential()
		m := cluster.NewMembership(reg, "me", "10.0.0.1:8080", "v1", cred)
		Expect(m.Start(ctx)).To(Succeed())
		DeferCleanup(m.Stop)

		got, err := reg.Get(ctx, "me")
		Expect(err).ToNot(HaveOccurred())
		Expect(got.PeerTokenHash).To(Equal(cred.Hash()))
	})
})
