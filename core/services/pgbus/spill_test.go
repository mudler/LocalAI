// SPDX-License-Identifier: MIT

package pgbus_test

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"gorm.io/gorm"

	"github.com/mudler/LocalAI/core/services/pgbus"
	"github.com/mudler/LocalAI/core/services/testutil"
)

// PostgreSQL refuses a pg_notify payload of 8000 bytes or more: async.c checks
// strlen(payload) >= NOTIFY_PAYLOAD_MAX_LENGTH, so 7999 is the largest that is
// accepted. Verified against postgres:16 rather than read off the docs, because
// the docs say "less than 8000 bytes" and the off-by-one is the whole subject of
// these specs.
//
// This literal is deliberately NOT pgbus.MaxNotifyPayloadBytes. A size spec
// that measures itself against the constant it is testing passes for every
// value of that constant, which makes it a spec about nothing.
const notifyCap = 8000

// encodedSize is what the carrier puts on the wire for an inline broadcast:
// the subject, the JSON escaping and the envelope's own keys all travel with
// the caller's payload. A payload sized against the cap alone would ship 7999
// bytes of data in an 8035-byte notification and fail at the server.
func encodedSize(subject string, data any) int {
	GinkgoHelper()
	subjectJSON, err := json.Marshal(subject)
	Expect(err).ToNot(HaveOccurred())
	dataJSON, err := json.Marshal(data)
	Expect(err).ToNot(HaveOccurred())
	return len(fmt.Sprintf(`{"s":%s,"d":%s}`, subjectJSON, dataJSON))
}

// payloadEncodingTo builds a valid single-key JSON object whose encoded
// notification is exactly total bytes. Valid on purpose: an oversized payload
// that the decoder would reject anyway cannot prove the SIZE routed it.
func payloadEncodingTo(subject string, total int) map[string]string {
	GinkgoHelper()
	data := map[string]string{"k": ""}
	filler := total - encodedSize(subject, data)
	Expect(filler).To(BeNumerically(">", 0))
	data["k"] = strings.Repeat("a", filler)
	Expect(encodedSize(subject, data)).To(Equal(total))
	return data
}

var _ = Describe("broadcasts too large for a notification", func() {
	const subject = "jobs.size.probe"

	var (
		db       *gorm.DB
		dsn      string
		pub, sub *pgbus.Bus
	)

	spilledRows := func() int64 {
		GinkgoHelper()
		var n int64
		Expect(db.Model(&pgbus.BusMessage{}).Where("subject = ?", subject).Count(&n).Error).To(Succeed())
		return n
	}

	BeforeEach(func() {
		db, dsn = testutil.SetupTestDBWithDSN()
		Expect(pgbus.Migrate(context.Background(), db)).To(Succeed())
		for _, b := range []**pgbus.Bus{&pub, &sub} {
			built, err := pgbus.New(context.Background(), pgbus.Config{DSN: dsn, DB: db})
			Expect(err).ToNot(HaveOccurred())
			DeferCleanup(built.Close)
			*b = built
		}
	})

	deliver := func(data any) []byte {
		GinkgoHelper()
		out := make(chan []byte, 4)
		_, err := sub.Subscribe(subject, func(b []byte) { out <- b })
		Expect(err).ToNot(HaveOccurred())

		Expect(pub.Publish(subject, data)).To(Succeed())

		var got []byte
		Eventually(out, 10*time.Second).Should(Receive(&got))
		return got
	}

	// The three rows of the size table. Each states an ABSOLUTE size, so a
	// mutation of the carrier's constant moves the behaviour without moving
	// the expectation.
	It("keeps a broadcast one byte under the cap in the notification itself", func() {
		data := payloadEncodingTo(subject, notifyCap-1)

		got := deliver(data)

		Expect(got).To(MatchJSON(mustJSON(data)))
		Expect(spilledRows()).To(BeZero(), "a notification PostgreSQL accepts must not touch the spill table")
	})

	It("spills a broadcast exactly at the cap, which PostgreSQL already refuses", func() {
		data := payloadEncodingTo(subject, notifyCap)

		got := deliver(data)

		Expect(got).To(MatchJSON(mustJSON(data)))
		Expect(spilledRows()).To(Equal(int64(1)))
	})

	It("carries a megabyte through the spill table byte for byte", func() {
		// Absolute, not derived from the cap: this row must stay green under a
		// mutated constant, which is what separates "the spill path works" from
		// "the spec sized itself against the thing it is testing".
		data := map[string]string{"k": strings.Repeat("a", 1<<20)}

		got := deliver(data)

		Expect(got).To(Equal(mustJSON(data)))
		Expect(spilledRows()).To(Equal(int64(1)))
	})

	It("stores the caller's payload in the spill row, not the envelope", func() {
		data := payloadEncodingTo(subject, notifyCap)

		_ = deliver(data)

		var row pgbus.BusMessage
		Expect(db.Where("subject = ?", subject).First(&row).Error).To(Succeed())
		Expect(row.Payload).To(Equal(mustJSON(data)))
		Expect(row.ID).ToNot(BeEmpty())
		Expect(row.CreatedAt).ToNot(BeZero())
	})

	It("delivers nothing for a notification that carries no payload at all", func() {
		// Publish cannot produce this, because json.Marshal is never empty. It
		// is reachable if anything else ever notifies on a localai_ channel,
		// and a handler called with nil is a message that says nothing rather
		// than no message, which is exactly the confusion this carrier must
		// never create. The sentinel proves it kept carrying.
		out := make(chan []byte, 4)
		_, err := sub.Subscribe(subject, func(b []byte) { out <- b })
		Expect(err).ToNot(HaveOccurred())

		Expect(db.Exec("SELECT pg_notify(?, ?)", "localai_jobs",
			fmt.Sprintf(`{"s":%q}`, subject)).Error).To(Succeed())
		Expect(pub.Publish(subject, map[string]string{"m": "sentinel"})).To(Succeed())

		var got []byte
		Eventually(out, 10*time.Second).Should(Receive(&got))
		Expect(got).To(MatchJSON(`{"m":"sentinel"}`))
	})

	It("delivers nothing, and keeps carrying, when a spilled row cannot be found", func() {
		// A notification whose row is gone is a lost message, not an empty one:
		// handing a handler nil would let a consumer read a carrier failure as
		// a fact about the deployment. The sentinel proves the carrier is still
		// alive afterwards, which needs no clock.
		out := make(chan []byte, 4)
		_, err := sub.Subscribe(subject, func(b []byte) { out <- b })
		Expect(err).ToNot(HaveOccurred())

		Expect(db.Exec("SELECT pg_notify(?, ?)", "localai_jobs",
			fmt.Sprintf(`{"s":%q,"i":"no-such-row"}`, subject)).Error).To(Succeed())
		Expect(pub.Publish(subject, map[string]string{"m": "sentinel"})).To(Succeed())

		var got []byte
		Eventually(out, 10*time.Second).Should(Receive(&got))
		Expect(got).To(MatchJSON(`{"m":"sentinel"}`))
	})
})

var _ = Describe("retiring spilled rows", func() {
	var (
		db  *gorm.DB
		dsn string
	)

	BeforeEach(func() {
		db, dsn = testutil.SetupTestDBWithDSN()
		Expect(pgbus.Migrate(context.Background(), db)).To(Succeed())
	})

	// aged writes one spilled row and backdates it on the DATABASE clock, which
	// is the clock the sweep compares against.
	aged := func(id string) {
		GinkgoHelper()
		Expect(db.Create(&pgbus.BusMessage{ID: id, Subject: "jobs.x", Payload: []byte(`{}`)}).Error).To(Succeed())
		Expect(db.Exec("UPDATE bus_messages SET created_at = now() - interval '1 hour' WHERE id = ?", id).Error).To(Succeed())
	}

	rows := func() []string {
		GinkgoHelper()
		var ids []string
		Expect(db.Model(&pgbus.BusMessage{}).Pluck("id", &ids).Error).To(Succeed())
		return ids
	}

	It("leaves the cutoff to the database clock", func() {
		// Pinned as a statement shape rather than as behaviour on purpose. The
		// test container shares this host's clock, so a cutoff computed in Go
		// and one computed by the server agree to the microsecond and no
		// behavioural spec can tell them apart. The deployment they differ in
		// is the one that matters: replicas whose clocks are minutes apart,
		// where a Go-side cutoff reaps rows other replicas have not read yet.
		Expect(pgbus.SpillSweepSQL).To(ContainSubstring("now()"))
		Expect(pgbus.SpillSweepSQL).ToNot(ContainSubstring("created_at < ?"))
	})

	It("runs the sweep on its own, without anyone asking it to", func() {
		// SweepSpill and SpillSweepSQL are both spec'd directly, and neither of
		// them proves the carrier ever CALLS them. Deleting the sweeper's `go`
		// statement left the whole suite green and bus_messages growing
		// forever, which is the unpinned-wiring shape this programme exists to
		// remove. The interval is a Config field so that this spec exists.
		aged("swept")
		Expect(db.Create(&pgbus.BusMessage{ID: "kept", Subject: "jobs.x", Payload: []byte(`{}`)}).Error).To(Succeed())

		b, err := pgbus.New(context.Background(), pgbus.Config{DSN: dsn, DB: db, SweepInterval: 20 * time.Millisecond})
		Expect(err).ToNot(HaveOccurred())
		DeferCleanup(b.Close)

		Eventually(rows, 30*time.Second).Should(ConsistOf("kept"))
	})

	It("deletes rows past the retention and keeps the rest", func() {
		aged("old")
		Expect(db.Create(&pgbus.BusMessage{ID: "fresh", Subject: "jobs.x", Payload: []byte(`{}`)}).Error).To(Succeed())

		Expect(pgbus.SweepSpill(context.Background(), db, 5*time.Minute)).To(Succeed())

		Expect(rows()).To(ConsistOf("fresh"))
	})
})

func mustJSON(v any) []byte {
	GinkgoHelper()
	b, err := json.Marshal(v)
	Expect(err).ToNot(HaveOccurred())
	return b
}
