package distributed_test

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"github.com/mudler/LocalAI/core/services/galleryop"
	"github.com/mudler/LocalAI/core/services/messaging"
	"github.com/mudler/LocalAI/core/services/pgbus"
	"github.com/mudler/LocalAI/tests/e2e/distributed/cluster"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"gorm.io/gorm"
)

// The fan-out carrier, under the binaries an operator runs.
//
// Removing the broker left ONE thing carrying every broadcast family in the
// product: PostgreSQL LISTEN/NOTIFY, in core/services/pgbus. That package and
// its callers are covered thoroughly in process, against a real database, by
// test-e2e-distributed. What was not covered anywhere was the same carrier
// running inside two separate `local-ai` processes: nothing in this Cluster
// partition mentioned pgbus, bus_messages, LISTEN or NOTIFY, so a deployment
// whose replicas each published to themselves and heard nobody would have left
// every suite green.
//
// The family both specs drive is a gallery operation, and it is chosen for one
// property nothing else on this carrier has: the answer a peer replica gives is
// held in memory ALONE. GET /models/jobs/<id> reads galleryop's statuses map;
// that map is filled on a peer by the SubjectGalleryProgressWildcard subscriber
// and by nothing else, because the only other filler, Hydrate, runs once at
// startup and every operation here is created long after both replicas booted.
// Every other family this carrier serves has a durable table behind it that a
// peer would converge through eventually, and a spec on one of those cannot say
// whether the broadcast arrived or the row was read.
//
// The specs then take that further and make it checkable rather than asserted:
// the gallery_operations row for an operation is written when the gallery
// WORKER dequeues it, so an operation still waiting in the queue has NO row at
// all. Both specs hold the queue and assert exactly that. A peer replica
// answering with the operation's own bytes while PostgreSQL holds no record of
// the operation is a peer that was told, and it can have been told only one way.
//
// What holds the queue is the gallery index fetch. The gallery worker runs one
// operation at a time on an unbuffered channel (see galleryop.EnqueueModelOp,
// which exists because of that), so an install whose index fetch is parked
// inside a server the spec controls parks every operation submitted behind it.
// That is the difference between proving an ordering and observing one: without
// the gate the admission broadcast and the terminal broadcast are separated by
// two database round trips, which no HTTP poller can reliably see between.

const (
	// fanoutTimeout bounds the wait for a broadcast published at one replica to
	// become readable at another. Generous against what it covers, which is one
	// NOTIFY, one primary-key SELECT for a spilled body, and one HTTP poll.
	fanoutTimeout = "60s"
	fanoutPoll    = "250ms"

	// fanoutHoldWindow is how long a spec requires a held operation to stay
	// unfinished at the replica that did not start it.
	//
	// It is not a tolerance. The gallery worker is parked inside an HTTP read
	// that only this spec can complete, so a terminal status appearing in this
	// window is a status published ahead of the work it reports on, which is
	// the ordering the assertion is about.
	fanoutHoldWindow = "3s"
	fanoutHoldPoll   = "250ms"

	// fanoutGalleryName names the one gallery the fan-out frontends run with.
	fanoutGalleryName = "e2e-fanout"

	// fanoutBlockerElement is the operation whose index fetch parks the gallery
	// worker. Nothing is ever installed under this name: the gate stops the
	// fetch long before any name is looked up in the index.
	fanoutBlockerElement = "fanout-queue-blocker"

	// oversizedElementBytes is how long an element name has to be for the
	// broadcast carrying it to exceed PostgreSQL's notification cap.
	//
	// An ABSOLUTE size, chosen against the cap rather than derived from it, and
	// that is deliberate. pgbus.maxNotifyPayloadBytes is 8000 and exclusive,
	// and the encoded notification carries the subject, the envelope keys and
	// the rest of the status alongside the name, so 9000 bytes of name clears
	// it by roughly 1300. A name sized as "the cap plus a bit" would move with
	// the cap, and a spec that moves with the thing it is measuring cannot
	// fail: shrink the constant and both sides shrink together. This one, with
	// the one-byte control below, brackets the cap from both directions, so
	// moving the cap in EITHER direction reddens the spill spec.
	oversizedElementBytes = 9000

	// tinyElement is the control: an element name whose whole broadcast is a
	// few hundred bytes, which no plausible cap would spill. It is what makes
	// "a bus_messages row exists" a statement about size rather than a
	// statement that is true of every operation.
	tinyElement = "f"

	// fanoutNameExcerpt is how much of an element name a failure message
	// prints. A nine-kilobyte name rendered whole makes a Ginkgo report
	// unreadable, which costs more than it tells.
	fanoutNameExcerpt = 48
)

// fanoutGalleryIndex is what the gate serves once a spec lets it go. Its
// contents do not matter and deliberately name nothing either spec installs:
// what the gate is for is the WAIT, and every operation here is expected to end
// in a failure once released.
const fanoutGalleryIndex = "- name: fanout-decoy\n  description: e2e fan-out probe gallery\n"

// modelGalleriesJSON is the --galleries value that points every frontend at one
// gated index.
func modelGalleriesJSON(g *gatedGallery) string {
	return fmt.Sprintf(`[{"name":%q,"url":%q}]`, fanoutGalleryName, g.URL())
}

// withModelGalleries pins the model gallery list every frontend runs with.
func withModelGalleries(galleries string) func(*cluster.Options) {
	return func(o *cluster.Options) { o.Galleries = galleries }
}

// modelOpStatus is the subset of galleryop.OpStatus these specs assert on, as
// GET /models/jobs/<id> serializes it.
type modelOpStatus struct {
	Processed          bool   `json:"processed"`
	Phase              string `json:"phase"`
	Message            string `json:"message"`
	GalleryElementName string `json:"gallery_element_name"`
	Error              string `json:"error"`
}

// summarise renders a status for a failure message with long fields abbreviated.
func (s modelOpStatus) summarise() string {
	return fmt.Sprintf("{processed:%t phase:%q message:%s element:%s error:%q}",
		s.Processed, s.Phase, abbreviateName(s.Message), abbreviateName(s.GalleryElementName), s.Error)
}

// abbreviateName quotes a field, keeping a report readable when it is long.
func abbreviateName(s string) string {
	if len(s) <= fanoutNameExcerpt {
		return fmt.Sprintf("%q", s)
	}
	return fmt.Sprintf("%q...(%d bytes total)", s[:fanoutNameExcerpt], len(s))
}

// modelOpProbe polls one gallery operation at one frontend and keeps what it
// last saw, so a failing Eventually can name it.
//
// Every accessor returns a zero value rather than raising on a failed read: a
// peer replica answers 500 for an operation it has not been told about yet, and
// that is the state these specs are waiting out, not a defect.
type modelOpProbe struct {
	cluster  *cluster.Cluster
	client   *http.Client
	frontend int
	opID     string

	lastErr error
	last    modelOpStatus
}

func newModelOpProbe(c *cluster.Cluster, client *http.Client, frontend int, opID string) *modelOpProbe {
	return &modelOpProbe{cluster: c, client: client, frontend: frontend, opID: opID}
}

func (p *modelOpProbe) read() modelOpStatus {
	var status modelOpStatus
	if err := p.cluster.GetJSON(p.client, p.frontend, "/models/jobs/"+p.opID, &status); err != nil {
		p.lastErr = err
		return modelOpStatus{}
	}
	p.lastErr = nil
	p.last = status
	return status
}

// elementDigest is the SHA-256 of the element name this frontend reports, and
// it is what the waiting assertions compare.
//
// A digest rather than the name itself, for the failure message alone: an
// Eventually that times out on a nine-kilobyte string prints it twice, and the
// exact-bytes assertion each spec makes afterwards is the one that states the
// claim. Comparing digests is comparing bytes; comparing them HERE keeps the
// report legible when the wait is what failed.
func (p *modelOpProbe) elementDigest() string { return digestOf(p.read().GalleryElementName) }

func (p *modelOpProbe) processed() bool { return p.read().Processed }

func (p *modelOpProbe) describe() string {
	if p.lastErr != nil {
		return fmt.Sprintf("frontend %d: the last read of gallery operation %s failed: %v",
			p.frontend, p.opID, p.lastErr)
	}
	return fmt.Sprintf("frontend %d: gallery operation %s last read as %s",
		p.frontend, p.opID, p.last.summarise())
}

// explain builds a lazy failure description, for the reason jobProbe.explain
// gives: Gomega renders a (string, args...) description when the assertion is
// CONSTRUCTED, which for a wait is before anything has gone wrong.
func (p *modelOpProbe) explain(format string, args ...any) func() string {
	return func() string {
		return fmt.Sprintf(format, args...) + ": " + p.describe()
	}
}

func digestOf(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])
}

// applyModel posts a model install at one frontend and returns the operation id
// it was handed.
//
// The endpoint answers 200 with an id the instant the operation is ADMITTED,
// before the gallery worker has seen it, which is exactly the window both specs
// live in.
func applyModel(c *cluster.Cluster, client *http.Client, frontend int, elementName string) string {
	GinkgoHelper()
	var accepted struct {
		UUID string `json:"uuid"`
	}
	status, err := c.PostJSON(client, frontend, "/models/apply",
		map[string]any{"id": elementName}, &accepted)
	Expect(err).ToNot(HaveOccurred())
	Expect(status).To(Equal(http.StatusOK),
		"frontend %d refused the install outright, so no operation was ever admitted", frontend)
	Expect(accepted.UUID).ToNot(BeEmpty())
	return accepted.UUID
}

// queuedBroadcast rebuilds the admission broadcast galleryop.markQueued
// publishes for an operation.
//
// It exists so the spill spec can ask the carrier's OWN size predicate about
// the exact payload the product built, rather than about a payload of the
// spec's invention. pgbus.FitsInline shares its encoder and its comparison with
// Publish, so an answer from it is the answer Publish gave.
func queuedBroadcast(opID, elementName string) galleryop.GalleryProgressEvent {
	return galleryop.GalleryProgressEvent{
		JobID: opID,
		Status: &galleryop.OpStatus{
			Message:            galleryop.PhaseQueued,
			Phase:              galleryop.PhaseQueued,
			GalleryElementName: elementName,
			Cancellable:        true,
		},
	}
}

// spilledBroadcasts returns the rows the carrier wrote for one subject, oldest
// first. A broadcast that fit in its notification leaves none.
func spilledBroadcasts(db *gorm.DB, subject string) ([]pgbus.BusMessage, error) {
	var rows []pgbus.BusMessage
	if err := db.Where("subject = ?", subject).Order("created_at").Find(&rows).Error; err != nil {
		return nil, err
	}
	return rows, nil
}

// spillCount is spilledBroadcasts reduced to a number, for a wait.
func spillCount(db *gorm.DB, subject string) int {
	rows, err := spilledBroadcasts(db, subject)
	if err != nil {
		return -1
	}
	return len(rows)
}

// galleryOperationRows counts what PostgreSQL records about one operation.
//
// Zero is the interesting answer: the row is written when the gallery worker
// DEQUEUES an operation, so an operation still waiting in the queue has none,
// and a replica answering about it is answering from something other than the
// database.
func galleryOperationRows(db *gorm.DB, opID string) int64 {
	GinkgoHelper()
	var count int64
	Expect(db.Table("gallery_operations").Where("id = ?", opID).Count(&count).Error).To(Succeed())
	return count
}

// fanoutFiller builds an element name of exactly n bytes.
//
// Ordinary printable characters, cycling, so the value is a name the product
// would accept anywhere it accepts a short one. That matters more than it
// looks: the point of the spill spec is that an OVERSIZED message survives, and
// a body the decoder would have rejected at any size proves nothing about size.
func fanoutFiller(n int) string {
	const alphabet = "abcdefghijklmnopqrstuvwxyz0123456789"
	var b strings.Builder
	b.Grow(n)
	for i := 0; i < n; i++ {
		b.WriteByte(alphabet[i%len(alphabet)])
	}
	return b.String()
}

// startFanoutCluster brings up two frontends sharing one database, with their
// model gallery served by a gate this spec holds, and parks the gallery worker
// of the STARTER replica inside that gate.
//
// It returns everything the caller asserts against, positioned on the state
// both specs need: one operation running and stopped inside its index fetch, so
// anything admitted afterwards stays queued for as long as the spec wants.
func startFanoutCluster(starter int) (*cluster.Cluster, *gorm.DB, *http.Client, *gatedGallery) {
	GinkgoHelper()
	gate := newGatedGallery(fanoutGalleryIndex, true)
	c, dsn := startClusterOnFreshDB(2, 0, withModelGalleries(modelGalleriesJSON(gate)))
	db := openClusterDB(dsn)
	client := controlSession(c)

	blocker := applyModel(c, client, starter, fanoutBlockerElement)
	Eventually(gate.fetches.Load, fanoutTimeout, fanoutPoll).Should(BeNumerically(">", 0),
		"frontend %d never fetched the gated gallery index for operation %s, so nothing is holding its "+
			"gallery worker and every ordering assertion below would be timing rather than arrangement",
		starter, blocker)
	return c, db, client, gate
}

// requireDistinctReplicas fails unless the two frontends are separate live
// replicas of one deployment.
//
// This is the assertion that keeps "the other replica" from becoming a spelling
// of "this replica". Reading a broadcast back at the process that published it
// is the wrong implementation: a carrier that only ever hears itself passes
// every such reading and delivers nothing to anybody, and that is precisely the
// deployment nothing in this suite could previously tell apart from a working
// one. Two different ports would be a weaker version of this; the instances
// table is the deployment's own answer to "how many replicas are there", and it
// gives each a distinct id.
func requireDistinctReplicas(c *cluster.Cluster, db *gorm.DB, starter, reader int) {
	GinkgoHelper()
	Expect(c.FrontendURL(starter)).ToNot(Equal(c.FrontendURL(reader)))

	roster := newInstanceRoster(db)
	awaitReplicas(roster, hostPortOf(c.FrontendURL(0)), hostPortOf(c.FrontendURL(1)))

	starterID := roster.idAt(hostPortOf(c.FrontendURL(starter)))
	readerID := roster.idAt(hostPortOf(c.FrontendURL(reader)))
	Expect(starterID).ToNot(BeEmpty(), roster.describe)
	Expect(readerID).ToNot(BeEmpty(), roster.describe)
	Expect(readerID).ToNot(Equal(starterID),
		"frontends %d and %d are the same replica, so reading one and publishing at the other proves nothing: %s",
		starter, reader, roster.describe())
}

var _ = Describe("Cross-replica fan-out", Label("Distributed"), Label("Cluster"), func() {
	// starter admits every operation; reader is never sent one and only ever
	// reads. Written down here rather than at each call so the one thing that
	// makes these specs mean anything is impossible to lose in an edit.
	const (
		starter = 0
		reader  = 1
	)

	// Scenario 1. A broadcast published inside one process, read out of
	// another, with an ordering the spec enforces rather than hopes for.
	//
	// A wrong implementation is not exotic. A carrier wired so each replica
	// hears only its own publishes satisfies every in-process spec in this
	// repository, because those run both ends in one process on purpose. What
	// it cannot do is answer here: the reader was never sent the request, and
	// while the operation is queued PostgreSQL holds nothing about it either.
	It("carries a gallery operation to the replica that did not start it, queued state before terminal", func() {
		c, db, client, gate := startFanoutCluster(starter)
		requireDistinctReplicas(c, db, starter, reader)

		// A name nothing else in this deployment uses, so an element name read
		// back at the reader names this operation and no other.
		const probeElement = "fanout-ordered-probe"
		probeID := applyModel(c, client, starter, probeElement)

		// Nothing was installed and nothing was written: the gallery worker is
		// parked, so this operation is one goroutine blocked on an unbuffered
		// send, with an admission broadcast already published and no row of its
		// own anywhere.
		Expect(galleryOperationRows(db, probeID)).To(BeZero(),
			"PostgreSQL already records operation %s, so a peer could read it from the database and this spec "+
				"would no longer be about the broadcast", probeID)

		reading := newModelOpProbe(c, client, reader, probeID)
		Eventually(reading.elementDigest, fanoutTimeout, fanoutPoll).
			Should(Equal(digestOf(probeElement)), reading.describe)

		// What arrived is the ADMISSION status and not some later one. Phase is
		// the product's own marker for "the worker has not started this yet"
		// (galleryop.PhaseQueued), so a reader holding it is a reader that was
		// told before the work rather than after it.
		Expect(reading.last.GalleryElementName).To(Equal(probeElement))
		Expect(reading.last.Phase).To(Equal(galleryop.PhaseQueued), reading.describe)
		Expect(reading.last.Processed).To(BeFalse(), reading.describe)

		// Still nothing in PostgreSQL, read AFTER the peer answered rather than
		// before. Read only beforehand it would say nothing: the row could have
		// appeared in between and been what the peer read.
		Expect(galleryOperationRows(db, probeID)).To(BeZero(),
			"PostgreSQL now records operation %s, so the reading above could have come from the database", probeID)

		// Held, not sampled. The worker is stopped inside an HTTP read only
		// this spec can complete, so a terminal status inside this window is
		// one published ahead of the work it reports on.
		Consistently(reading.processed, fanoutHoldWindow, fanoutHoldPoll).Should(BeFalse(),
			reading.explain("the reader saw the operation finish while its gallery worker was still blocked fetching the index"))

		gate.release()

		// And the LATER broadcast crosses too. Both ends of the ordering are
		// now facts at the reader: queued while the gate was shut, terminal
		// only after it opened.
		Eventually(reading.processed, fanoutTimeout, fanoutPoll).Should(BeTrue(), reading.describe)

		// What the terminal broadcast CARRIES, and not merely that one arrived.
		// The gallery index the gate serves names no such model, so the
		// operation ends in the failure galleryop.Start builds, whose two
		// fields are written together and in one place: the message is the
		// error with a fixed prefix. A reader holding both in that relation is
		// a reader that decoded a payload rather than a flag.
		//
		// The element name is deliberately NOT re-asserted here. A terminal
		// status does not carry one (see updateError in galleryop.Start, which
		// builds a fresh OpStatus with only the error on it), so requiring one
		// would be asserting against the product rather than about it.
		Expect(reading.last.Error).ToNot(BeEmpty(), reading.describe)
		Expect(reading.last.Message).To(Equal("error: "+reading.last.Error),
			reading.explain("the terminal broadcast arrived with a message that is not the error it reports"))
	})

	// Scenario 2. The spill, end to end.
	//
	// PostgreSQL refuses a notification payload of 8000 bytes or more, so
	// anything larger is written to bus_messages and the notification carries
	// an id. That path is the ORDINARY one for several families here (a gallery
	// progress event carries an entry per node; a job result carries a whole
	// model output), so it is not an edge case, and until now no spec ran it
	// between two processes at all.
	//
	// The trap this spec is written against is specific and has been fallen
	// into three times on this branch: a size-limit spec that cannot fail,
	// because the oversized body was malformed and the decoder refused it
	// either way, or because the body was sized against the cap so shrinking
	// the cap moved both sides. Here the body is an ordinary element name that
	// the real consumer decodes and surfaces, the size is absolute, and a
	// second operation a few hundred bytes long is asserted NOT to spill in the
	// same run. Move the cap in either direction and one of the two goes red.
	It("carries a broadcast too large for a notification between two replicas, byte for byte, through the spill table", func() {
		c, db, client, gate := startFanoutCluster(starter)
		requireDistinctReplicas(c, db, starter, reader)

		oversized := fanoutFiller(oversizedElementBytes)
		oversizedID := applyModel(c, client, starter, oversized)
		tinyID := applyModel(c, client, starter, tinyElement)

		oversizedSubject := messaging.SubjectGalleryProgress(oversizedID)
		tinySubject := messaging.SubjectGalleryProgress(tinyID)

		// The size decision itself, asked of the predicate Publish shares its
		// encoder and its comparison with, about the payload the product built.
		// The pair is the point: one answer either way from one function means
		// the cap is being measured rather than assumed.
		oversizedFits, err := pgbus.FitsInline(oversizedSubject, queuedBroadcast(oversizedID, oversized))
		Expect(err).ToNot(HaveOccurred())
		Expect(oversizedFits).To(BeFalse(),
			"a %d byte element name still fits in a notification, so this spec would never reach the spill path",
			len(oversized))

		tinyFits, err := pgbus.FitsInline(tinySubject, queuedBroadcast(tinyID, tinyElement))
		Expect(err).ToNot(HaveOccurred())
		Expect(tinyFits).To(BeTrue(),
			"a %d byte element name no longer fits inline either, so spilling has stopped being a statement about size",
			len(tinyElement))

		// The wire. One row, written by the publishing replica because its
		// notification would have been refused.
		Eventually(func() int { return spillCount(db, oversizedSubject) }, fanoutTimeout, fanoutPoll).
			Should(Equal(1), "no bus_messages row was written for %s, so the oversized broadcast did not take the spill path", oversizedSubject)

		// And the control: the small operation crossed the same carrier in the
		// same run and left nothing behind. Without this, "a row exists" would
		// be satisfied by an implementation that spilled everything, which is a
		// different product and would hide the cap entirely.
		Expect(spillCount(db, tinySubject)).To(BeZero(),
			"a bus_messages row was written for the %d byte broadcast on %s too, so the row above says nothing about size",
			len(tinyElement), tinySubject)

		rows, err := spilledBroadcasts(db, oversizedSubject)
		Expect(err).ToNot(HaveOccurred())
		Expect(rows).To(HaveLen(1))

		var wire galleryop.GalleryProgressEvent
		Expect(json.Unmarshal(rows[0].Payload, &wire)).To(Succeed(),
			"the spilled row is not a decodable broadcast, so nothing downstream could have read it")
		Expect(wire.JobID).To(Equal(oversizedID))
		Expect(wire.Status).ToNot(BeNil())
		Expect(wire.Status.GalleryElementName).To(HaveLen(oversizedElementBytes))
		Expect(wire.Status.GalleryElementName).To(Equal(oversized),
			"the bytes in the spill row are not the bytes that were published")

		// The other end. The reader was never sent this request and, while the
		// operation is queued, PostgreSQL records nothing about it, so the only
		// account of it there has ever been outside the starter's memory is the
		// row above plus the notification naming it.
		reading := newModelOpProbe(c, client, reader, oversizedID)
		Eventually(reading.elementDigest, fanoutTimeout, fanoutPoll).
			Should(Equal(digestOf(oversized)), reading.describe)
		Expect(reading.last.GalleryElementName).To(HaveLen(oversizedElementBytes))
		Expect(reading.last.GalleryElementName).To(Equal(oversized),
			"the reader reconstructed a different %d byte element name from the spilled row", oversizedElementBytes)
		Expect(reading.last.Phase).To(Equal(galleryop.PhaseQueued), reading.describe)

		Expect(galleryOperationRows(db, oversizedID)).To(BeZero(),
			"PostgreSQL records operation %s, so the reader could have read the name from gallery_operations "+
				"rather than from the broadcast", oversizedID)

		// The small operation reaches the reader too. A carrier that delivered
		// only what it spilled would pass everything above.
		tinyReading := newModelOpProbe(c, client, reader, tinyID)
		Eventually(tinyReading.elementDigest, fanoutTimeout, fanoutPoll).
			Should(Equal(digestOf(tinyElement)), tinyReading.describe)
		Expect(tinyReading.last.GalleryElementName).To(Equal(tinyElement))

		// Released here rather than left to cleanup, so the operations retire
		// while the cluster is still up and their logs are still being written.
		gate.release()
		Eventually(reading.processed, fanoutTimeout, fanoutPoll).Should(BeTrue(), reading.describe)
	})
})
