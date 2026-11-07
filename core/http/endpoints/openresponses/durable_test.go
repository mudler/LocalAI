package openresponses

import (
	"context"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync/atomic"
	"time"

	"github.com/labstack/echo/v4"
	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/core/http/auth"
	"github.com/mudler/LocalAI/core/schema"
	"github.com/mudler/LocalAI/core/services/testutil"
	"github.com/mudler/LocalAI/pkg/sqlitedb"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"gorm.io/gorm"
)

func durableEvent(seq int, typ string) *schema.ORStreamEvent {
	return &schema.ORStreamEvent{Type: typ, SequenceNumber: seq}
}

func durableResponse(id, status string) *schema.ORResponseResource {
	return &schema.ORResponseResource{ID: id, Object: "response", CreatedAt: time.Now().Unix(), Status: status, Model: "m",
		Output: []schema.ORItemField{{Type: "message", ID: "msg_" + id, Role: "assistant"}}}
}

func rowOf(db *gorm.DB, id string) responseRow {
	var row responseRow
	ExpectWithOffset(1, db.Where("id = ?", id).Take(&row).Error).To(Succeed())
	return row
}

func eventCount(db *gorm.DB, id string) int {
	var n int64
	ExpectWithOffset(1, db.Model(&responseEventRow{}).Where("response_id = ?", id).Count(&n).Error).To(Succeed())
	return int(n)
}

// The store contract: what a caller can observe must not depend on whether the
// store is a map or a table. view returns the store a reader uses; for the
// table store that is a second store on the same database, which is what a
// restart or another replica looks like.
func storeContract(newStore func() *ResponseStore, view func(*ResponseStore) *ResponseStore) {
	startBackground := func(s *ResponseStore, id, owner string) (cancelled *atomic.Bool) {
		cancelled = &atomic.Bool{}
		s.StoreBackgroundOwned(id, &schema.OpenResponsesRequest{Model: "m", Input: "hi"},
			durableResponse(id, schema.ORStatusQueued), func() { cancelled.Store(true) }, true, owner)
		Expect(s.UpdateStatus(id, schema.ORStatusInProgress, nil)).To(Succeed())
		return cancelled
	}

	It("returns what was stored, with its owner", func() {
		s := newStore()
		s.StoreOwned("r1", &schema.OpenResponsesRequest{Model: "m", Input: "hi"}, durableResponse("r1", schema.ORStatusCompleted), "alice")
		got, err := view(s).Get("r1")
		Expect(err).NotTo(HaveOccurred())
		Expect(got.Owner).To(Equal("alice"))
		Expect(got.Request.Model).To(Equal("m"))
		Expect(got.Items).To(HaveKey("msg_r1"))
		Expect(accessAllowed(got, "alice")).To(BeTrue())
		Expect(accessAllowed(got, "bob")).To(BeFalse())
		_, err = view(s).Get("missing")
		Expect(err).To(HaveOccurred())
	})

	It("serves events after a sequence number, in order", func() {
		s := newStore()
		startBackground(s, "r2", "")
		for i, typ := range []string{"response.created", "response.output_text.delta", "response.output_text.delta"} {
			Expect(s.AppendEvent("r2", durableEvent(i+1, typ))).To(Succeed())
		}
		Expect(s.AppendEventNext("r2", durableEvent(0, "response.completed"))).To(Succeed())
		Expect(s.UpdateResponse("r2", durableResponse("r2", schema.ORStatusCompleted))).To(Succeed())

		r := view(s)
		all, err := r.GetEventsAfter("r2", 0)
		Expect(err).NotTo(HaveOccurred())
		Expect(all).To(HaveLen(4))
		for i, e := range all {
			Expect(e.SequenceNumber).To(Equal(i + 1))
		}
		Expect(all[3].EventType).To(Equal("response.completed"))
		tail, err := r.GetEventsAfter("r2", 2)
		Expect(err).NotTo(HaveOccurred())
		Expect(tail).To(HaveLen(2))
		enabled, err := r.IsStreamEnabled("r2")
		Expect(err).NotTo(HaveOccurred())
		Expect(enabled).To(BeTrue())
		status, err := r.statusOf("r2")
		Expect(err).NotTo(HaveOccurred())
		Expect(status).To(Equal(schema.ORStatusCompleted))
	})

	It("reports ErrOffsetLost below the eviction watermark", func() {
		s := newStore()
		s.maxStreamEvents = 5
		startBackground(s, "r3", "")
		for i := 1; i <= 12; i++ {
			Expect(s.AppendEvent("r3", durableEvent(i, "response.output_item.done"))).To(Succeed())
		}
		Expect(s.UpdateResponse("r3", durableResponse("r3", schema.ORStatusCompleted))).To(Succeed())

		r := view(s)
		_, err := r.GetEventsAfter("r3", 3)
		Expect(err).To(MatchError(ErrOffsetLost))
		kept, err := r.GetEventsAfter("r3", 7)
		Expect(err).NotTo(HaveOccurred())
		Expect(kept).To(HaveLen(5))
		Expect(kept[0].SequenceNumber).To(Equal(8))
	})

	It("drops the events on ClearEvents and keeps the response", func() {
		s := newStore()
		startBackground(s, "r4", "")
		Expect(s.AppendEvent("r4", durableEvent(1, "response.created"))).To(Succeed())
		Expect(s.UpdateResponse("r4", durableResponse("r4", schema.ORStatusCompleted))).To(Succeed())
		Expect(s.ClearEvents("r4")).To(Succeed())
		events, err := view(s).GetEventsAfter("r4", 0)
		Expect(err).NotTo(HaveOccurred())
		Expect(events).To(BeEmpty())
		_, err = view(s).Get("r4")
		Expect(err).NotTo(HaveOccurred())
	})

	It("cancels a running response once and leaves a finished one alone", func() {
		s := newStore()
		cancelled := startBackground(s, "r5", "")
		got, err := s.Cancel("r5")
		Expect(err).NotTo(HaveOccurred())
		Expect(got.Status).To(Equal(schema.ORStatusCancelled))
		Expect(cancelled.Load()).To(BeTrue())
		stored, err := view(s).Get("r5")
		Expect(err).NotTo(HaveOccurred())
		Expect(stored.Response.Status).To(Equal(schema.ORStatusCancelled))

		s.StoreOwned("r6", &schema.OpenResponsesRequest{}, durableResponse("r6", schema.ORStatusCompleted), "")
		got, err = s.Cancel("r6")
		Expect(err).NotTo(HaveOccurred())
		Expect(got.Status).To(Equal(schema.ORStatusCompleted))
	})

	It("forgets a deleted response", func() {
		s := newStore()
		s.StoreOwned("r7", &schema.OpenResponsesRequest{}, durableResponse("r7", schema.ORStatusCompleted), "")
		s.Delete("r7")
		_, err := view(s).Get("r7")
		Expect(err).To(HaveOccurred())
	})
}

func tableSpecs(openDB func() *gorm.DB) {
	var db *gorm.DB

	newStore := func(token string, opts ...func(*DurableOptions)) *ResponseStore {
		o := DurableOptions{Token: token}
		for _, f := range opts {
			f(&o)
		}
		s := NewResponseStore(0)
		Expect(s.EnableDurable(db, o)).To(Succeed())
		DeferCleanup(func() { _ = s.Close() })
		return s
	}
	slowBatch := func(o *DurableOptions) { o.BatchEvery = time.Hour }

	startRunning := func(s *ResponseStore, id, owner string) *atomic.Bool {
		cancelled := &atomic.Bool{}
		s.StoreBackgroundOwned(id, &schema.OpenResponsesRequest{Model: "m"}, durableResponse(id, schema.ORStatusQueued),
			func() { cancelled.Store(true) }, true, owner)
		Expect(s.UpdateStatus(id, schema.ORStatusInProgress, nil)).To(Succeed())
		return cancelled
	}

	BeforeEach(func() {
		db = openDB()
	})

	Describe("the store contract on the table store", func() {
		var n int
		storeContract(func() *ResponseStore {
			n++
			return newStore("writer-" + string(rune('a'+n)))
		}, func(*ResponseStore) *ResponseStore {
			n++
			return newStore("reader-" + string(rune('a'+n)))
		})
	})

	It("serves a resume from a new store on the same database after a restart", func() {
		a := newStore("a")
		startRunning(a, "r1", "alice")
		Expect(a.AppendEvent("r1", durableEvent(1, "response.created"))).To(Succeed())
		Expect(a.AppendEvent("r1", durableEvent(2, "response.output_text.delta"))).To(Succeed())
		// A crash: no Close, no flush. The non-delta event is already written;
		// the delta waits for its batch and is the loss the design accepts.
		restarted := newStore("a-after")
		events, err := restarted.GetEventsAfter("r1", 0)
		Expect(err).NotTo(HaveOccurred())
		Expect(events).To(HaveLen(1))
		Expect(events[0].SequenceNumber).To(Equal(1))
		got, err := restarted.Get("r1")
		Expect(err).NotTo(HaveOccurred())
		Expect(got.Owner).To(Equal("alice"))
	})

	It("lets replica B resume a response that replica A runs", func() {
		a := newStore("a")
		b := newStore("b")
		startRunning(a, "r1", "alice")
		Expect(a.AppendEvent("r1", durableEvent(1, "response.created"))).To(Succeed())

		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		signal, err := b.SubscribeEvents(ctx, "r1")
		Expect(err).NotTo(HaveOccurred())

		Expect(a.AppendEvent("r1", durableEvent(2, "response.output_item.done"))).To(Succeed())
		Eventually(signal, 5*time.Second).Should(Receive())
		got, err := b.GetEventsAfter("r1", 1)
		Expect(err).NotTo(HaveOccurred())
		Expect(got).To(HaveLen(1))
		Expect(got[0].SequenceNumber).To(Equal(2))

		status, err := b.statusOf("r1")
		Expect(err).NotTo(HaveOccurred())
		Expect(status).To(Equal(schema.ORStatusInProgress))
		Expect(a.UpdateResponse("r1", durableResponse("r1", schema.ORStatusCompleted))).To(Succeed())
		status, _ = b.statusOf("r1")
		Expect(status).To(Equal(schema.ORStatusCompleted))
	})

	Describe("batched writes", func() {
		It("holds deltas back and writes them on the timer", func() {
			a := newStore("a", func(o *DurableOptions) { o.BatchEvery = 100 * time.Millisecond })
			startRunning(a, "r1", "")
			Expect(a.AppendEvent("r1", durableEvent(1, "response.output_text.delta"))).To(Succeed())
			Expect(eventCount(db, "r1")).To(Equal(0))
			Eventually(func() int { return eventCount(db, "r1") }, 5*time.Second, 20*time.Millisecond).Should(Equal(1))
		})

		It("writes at once on an event that is not a delta", func() {
			a := newStore("a", slowBatch)
			startRunning(a, "r1", "")
			Expect(a.AppendEvent("r1", durableEvent(1, "response.output_text.delta"))).To(Succeed())
			Expect(eventCount(db, "r1")).To(Equal(0))
			Expect(a.AppendEvent("r1", durableEvent(2, "response.output_item.done"))).To(Succeed())
			Expect(eventCount(db, "r1")).To(Equal(2))
			Expect(rowOf(db, "r1").LastSeq).To(Equal(2))
		})

		It("writes when the batch is full", func() {
			a := newStore("a", slowBatch, func(o *DurableOptions) { o.BatchEvents = 3 })
			startRunning(a, "r1", "")
			for i := 1; i <= 3; i++ {
				Expect(a.AppendEvent("r1", durableEvent(i, "response.output_text.delta"))).To(Succeed())
			}
			Expect(eventCount(db, "r1")).To(Equal(3))
		})

		It("writes the waiting deltas before an end status is visible", func() {
			a := newStore("a", slowBatch)
			startRunning(a, "r1", "")
			Expect(a.AppendEvent("r1", durableEvent(1, "response.output_text.delta"))).To(Succeed())
			Expect(a.UpdateResponse("r1", durableResponse("r1", schema.ORStatusCompleted))).To(Succeed())
			Expect(eventCount(db, "r1")).To(Equal(1))
			Expect(rowOf(db, "r1").Status).To(Equal(schema.ORStatusCompleted))
		})

		It("writes what waits when the store closes", func() {
			a := newStore("a", slowBatch)
			startRunning(a, "r1", "")
			Expect(a.AppendEvent("r1", durableEvent(1, "response.output_text.delta"))).To(Succeed())
			Expect(a.Close()).To(Succeed())
			Expect(eventCount(db, "r1")).To(Equal(1))
		})

		It("keeps the table within the caps of the buffer", func() {
			a := newStore("a")
			a.maxStreamEvents = 5
			startRunning(a, "r1", "")
			for i := 1; i <= 12; i++ {
				Expect(a.AppendEvent("r1", durableEvent(i, "response.output_item.done"))).To(Succeed())
			}
			Expect(eventCount(db, "r1")).To(Equal(5))
			Expect(rowOf(db, "r1").DroppedThrough).To(Equal(7))
		})
	})

	Describe("the lease", func() {
		It("is renewed by the owner on its tick", func() {
			a := newStore("a")
			startRunning(a, "r1", "")
			Expect(db.Model(&responseRow{}).Where("id = ?", "r1").Update("lease_until", 1).Error).To(Succeed())
			a.leaseTick(context.Background())
			Expect(rowOf(db, "r1").LeaseUntil).To(BeNumerically(">", nowMs()))
		})

		It("ends a run whose lease ran out as failed with server_restarted, and tells a tailing client", func() {
			a := newStore("a")
			b := newStore("b")
			startRunning(a, "dead", "alice")
			startRunning(a, "alive", "alice")
			Expect(a.AppendEvent("dead", durableEvent(1, "response.created"))).To(Succeed())
			Expect(db.Model(&responseRow{}).Where("id = ?", "dead").Update("lease_until", 1).Error).To(Succeed())

			b.SweepOnce(context.Background())

			got, err := b.Get("dead")
			Expect(err).NotTo(HaveOccurred())
			Expect(got.Response.Status).To(Equal(schema.ORStatusFailed))
			Expect(got.Response.Error).NotTo(BeNil())
			Expect(got.Response.Error.Code).To(Equal(ErrorCodeServerRestarted))
			events, err := b.GetEventsAfter("dead", 1)
			Expect(err).NotTo(HaveOccurred())
			Expect(events).To(HaveLen(1))
			Expect(events[0].EventType).To(Equal("response.failed"))
			Expect(events[0].SequenceNumber).To(Equal(2))
			Expect(rowOf(db, "alive").Status).To(Equal(schema.ORStatusInProgress))

			// A second sweep has nothing to do.
			n, err := b.db().sweepDead(context.Background(), "")
			Expect(err).NotTo(HaveOccurred())
			Expect(n).To(BeZero())
		})

		It("ends a run as cancelled when a cancel was requested before the owner died", func() {
			a := newStore("a")
			b := newStore("b")
			startRunning(a, "r1", "")
			_, err := b.Cancel("r1")
			Expect(err).NotTo(HaveOccurred())
			Expect(db.Model(&responseRow{}).Where("id = ?", "r1").Update("lease_until", 1).Error).To(Succeed())
			b.SweepOnce(context.Background())
			Expect(rowOf(db, "r1").Status).To(Equal(schema.ORStatusCancelled))
		})

		It("ends every run of another process at a standalone start, and none of its own", func() {
			old := newStore("old")
			startRunning(old, "left-behind", "")
			fresh := newStore("fresh")
			startRunning(fresh, "mine", "")
			n, err := fresh.SweepOrphans(context.Background())
			Expect(err).NotTo(HaveOccurred())
			Expect(n).To(Equal(1))
			Expect(rowOf(db, "left-behind").Status).To(Equal(schema.ORStatusFailed))
			Expect(rowOf(db, "mine").Status).To(Equal(schema.ORStatusInProgress))
		})

		It("stops the generation of a run the sweeper ended, and writes no more for it", func() {
			a := newStore("a")
			b := newStore("b")
			cancelled := startRunning(a, "r1", "")
			Expect(db.Model(&responseRow{}).Where("id = ?", "r1").Update("lease_until", 1).Error).To(Succeed())
			b.SweepOnce(context.Background())
			a.leaseTick(context.Background())
			Expect(cancelled.Load()).To(BeTrue())
			Expect(a.UpdateResponse("r1", durableResponse("r1", schema.ORStatusCompleted))).To(Succeed())
			Expect(rowOf(db, "r1").Status).To(Equal(schema.ORStatusFailed))
		})
	})

	Describe("cancel_requested", func() {
		It("reaches the owner through the table when the bus message is lost", func() {
			a := newStore("a")
			b := newStore("b")
			cancelled := startRunning(a, "r1", "alice")
			got, err := b.Cancel("r1")
			Expect(err).NotTo(HaveOccurred())
			Expect(got.Status).To(Equal(schema.ORStatusCancelled))
			Expect(rowOf(db, "r1").CancelRequested).To(BeTrue())
			Expect(cancelled.Load()).To(BeFalse())

			a.leaseTick(context.Background())
			Expect(cancelled.Load()).To(BeTrue())
			Expect(rowOf(db, "r1").Status).To(Equal(schema.ORStatusCancelled))
		})
	})

	Describe("expiry", func() {
		It("gives a row the default 7 days when no TTL is set", func() {
			a := newStore("a")
			a.StoreOwned("r1", &schema.OpenResponsesRequest{}, durableResponse("r1", schema.ORStatusCompleted), "")
			week := (7 * 24 * time.Hour).Milliseconds()
			Expect(rowOf(db, "r1").ExpiresAt).To(BeNumerically("~", nowMs()+week, 60_000))
		})

		It("uses the configured TTL when there is one", func() {
			a := newStore("a")
			a.SetTTL(time.Hour)
			a.StoreOwned("r1", &schema.OpenResponsesRequest{}, durableResponse("r1", schema.ORStatusCompleted), "")
			Expect(rowOf(db, "r1").ExpiresAt).To(BeNumerically("~", nowMs()+time.Hour.Milliseconds(), 60_000))
		})

		It("deletes expired finished rows with their events, and keeps running ones", func() {
			a := newStore("a")
			startRunning(a, "old", "")
			Expect(a.AppendEvent("old", durableEvent(1, "response.created"))).To(Succeed())
			Expect(a.UpdateResponse("old", durableResponse("old", schema.ORStatusCompleted))).To(Succeed())
			startRunning(a, "running", "")
			Expect(db.Model(&responseRow{}).Where("id IN ?", []string{"old", "running"}).Update("expires_at", 1000).Error).To(Succeed())

			a.SweepOnce(context.Background())

			var n int64
			Expect(db.Model(&responseRow{}).Where("id = ?", "old").Count(&n).Error).To(Succeed())
			Expect(n).To(BeZero())
			Expect(eventCount(db, "old")).To(BeZero())
			Expect(rowOf(db, "running").Status).To(Equal(schema.ORStatusInProgress))
		})

		It("reports an expired row as expired", func() {
			a := newStore("a")
			a.StoreOwned("r1", &schema.OpenResponsesRequest{}, durableResponse("r1", schema.ORStatusCompleted), "")
			Expect(db.Model(&responseRow{}).Where("id = ?", "r1").Update("expires_at", 1000).Error).To(Succeed())
			_, err := newStore("b").Get("r1")
			Expect(err).To(MatchError(ContainSubstring("expired")))
		})
	})

	Describe("with replicated metadata", func() {
		var bus *testutil.FakeBus

		replica := func(id string) *ResponseStore {
			s := newStore(id)
			Expect(s.EnableDistributed(context.Background(), bus, id)).To(Succeed())
			return s
		}

		BeforeEach(func() {
			bus = testutil.NewFakeBus()
		})

		It("writes the table through the replicated map, once", func() {
			a := replica("a")
			startRunning(a, "r1", "alice")
			Expect(a.metadataViaSync()).To(BeTrue())
			Expect(a.AppendEvent("r1", durableEvent(1, "response.output_text.delta"))).To(Succeed())
			Expect(a.UpdateResponse("r1", durableResponse("r1", schema.ORStatusCompleted))).To(Succeed())
			row := rowOf(db, "r1")
			Expect(row.Status).To(Equal(schema.ORStatusCompleted))
			Expect(row.OwnerInstance).To(Equal("a"))
			Expect(row.Owner).To(Equal("alice"))
			// The waiting delta was written before the end state.
			Expect(eventCount(db, "r1")).To(Equal(1))
		})

		It("starts a late replica with the rows of the table", func() {
			a := replica("a")
			a.StoreOwned("r1", &schema.OpenResponsesRequest{Model: "m"}, durableResponse("r1", schema.ORStatusCompleted), "alice")
			late := replica("late")
			v, ok := late.syncMap().Get("r1")
			Expect(ok).To(BeTrue())
			Expect(v.Owner).To(Equal("alice"))
			Expect(v.OwnerReplica).To(Equal("a"))
		})

		It("lets replica B resume and cancel a response that replica A runs", func() {
			a := replica("a")
			b := replica("b")
			cancelled := startRunning(a, "r1", "alice")
			Expect(a.AppendEvent("r1", durableEvent(1, "response.created"))).To(Succeed())

			got, err := b.GetEventsAfter("r1", 0)
			Expect(err).NotTo(HaveOccurred())
			Expect(got).To(HaveLen(1))
			stored, err := b.Get("r1")
			Expect(err).NotTo(HaveOccurred())
			Expect(accessAllowed(stored, "bob")).To(BeFalse())

			resp, err := b.Cancel("r1")
			Expect(err).NotTo(HaveOccurred())
			Expect(resp.Status).To(Equal(schema.ORStatusCancelled))
			Expect(cancelled.Load()).To(BeTrue())
			Expect(rowOf(db, "r1").Status).To(Equal(schema.ORStatusCancelled))
		})

		It("removes the row when a response is deleted", func() {
			a := replica("a")
			a.StoreOwned("r1", &schema.OpenResponsesRequest{}, durableResponse("r1", schema.ORStatusCompleted), "")
			a.Delete("r1")
			var n int64
			Expect(db.Model(&responseRow{}).Where("id = ?", "r1").Count(&n).Error).To(Succeed())
			Expect(n).To(BeZero())
		})
	})

	It("finds an item of a stored response after a restart", func() {
		a := newStore("a")
		a.StoreOwned("r1", &schema.OpenResponsesRequest{}, durableResponse("r1", schema.ORStatusCompleted), "")
		item, id, err := newStore("b").FindItem("msg_r1", "")
		Expect(err).NotTo(HaveOccurred())
		Expect(id).To(Equal("r1"))
		Expect(item.ID).To(Equal("msg_r1"))
	})

	Describe("the owner check on every read path", func() {
		var (
			global *ResponseStore
			owner  *ResponseStore
		)

		BeforeEach(func() {
			global = GetGlobalStore()
			Expect(global.EnableDurable(db, DurableOptions{Token: "reader"})).To(Succeed())
			DeferCleanup(func() {
				global.mu.Lock()
				global.dur = nil
				global.responses = map[string]*StoredResponse{}
				global.mu.Unlock()
			})
			owner = newStore("owner")
			startRunning(owner, "r1", "alice")
			Expect(owner.AppendEvent("r1", durableEvent(1, "response.created"))).To(Succeed())
			Expect(owner.UpdateResponse("r1", durableResponse("r1", schema.ORStatusCompleted))).To(Succeed())
		})

		call := func(handler echo.HandlerFunc, method, target, caller string) *httptest.ResponseRecorder {
			e := echo.New()
			req := httptest.NewRequest(method, target, nil)
			rec := httptest.NewRecorder()
			c := e.NewContext(req, rec)
			c.SetParamNames("id")
			c.SetParamValues("r1")
			if caller != "" {
				c.Set("auth_user", &auth.User{ID: caller})
			}
			Expect(handler(c)).To(Succeed())
			return rec
		}

		It("answers GET only to the owner", func() {
			Expect(call(GetResponseEndpoint(), http.MethodGet, "/v1/responses/r1", "bob").Code).To(Equal(404))
			Expect(call(GetResponseEndpoint(), http.MethodGet, "/v1/responses/r1", "alice").Code).To(Equal(200))
		})

		It("resumes the stream only for the owner", func() {
			bob := call(GetResponseEndpoint(), http.MethodGet, "/v1/responses/r1?stream=true&starting_after=0", "bob")
			Expect(bob.Code).To(Equal(404))
			Expect(bob.Body.String()).NotTo(ContainSubstring("response.created"))
			alice := call(GetResponseEndpoint(), http.MethodGet, "/v1/responses/r1?stream=true&starting_after=0", "alice")
			Expect(alice.Code).To(Equal(200))
			Expect(alice.Body.String()).To(ContainSubstring("response.created"))
			Expect(alice.Body.String()).To(ContainSubstring("[DONE]"))
		})

		It("cancels only for the owner", func() {
			Expect(call(CancelResponseEndpoint(), http.MethodPost, "/v1/responses/r1/cancel", "bob").Code).To(Equal(404))
			Expect(rowOf(db, "r1").CancelRequested).To(BeFalse())
		})

		It("resolves an item reference only for the owner", func() {
			_, _, err := global.FindItem("msg_r1", "bob")
			Expect(err).To(HaveOccurred(), "bob read an item of alice's response")
			item, id, err := global.FindItem("msg_r1", "alice")
			Expect(err).NotTo(HaveOccurred())
			Expect(id).To(Equal("r1"))
			Expect(item.ID).To(Equal("msg_r1"))
			ref := []any{map[string]any{"type": "item_reference", "id": "msg_r1"}}
			_, err = convertORInputToMessages(ref, &config.ModelConfig{}, "bob")
			Expect(err).To(HaveOccurred())
			_, err = convertORInputToMessages(ref, &config.ModelConfig{}, "alice")
			Expect(err).NotTo(HaveOccurred())
		})

		It("treats the item id as text, not as a pattern", func() {
			_, _, err := global.FindItem("msg_r%", "alice")
			Expect(err).To(HaveOccurred())
			_, _, err = global.FindItem("msg__1", "alice")
			Expect(err).To(HaveOccurred())
		})

		It("refuses previous_response_id to a caller who is not the owner", func() {
			_, err := resolvePreviousResponseMessages(global, "r1", &config.ModelConfig{}, "bob")
			Expect(err).To(HaveOccurred())
			var notFound *previousResponseNotFoundError
			Expect(err).To(BeAssignableToTypeOf(notFound))
		})

		It("tails a response of another replica to its end", func() {
			startRunning(owner, "r2", "alice")
			Expect(owner.AppendEvent("r2", durableEvent(1, "response.created"))).To(Succeed())

			e := echo.New()
			req := httptest.NewRequest(http.MethodGet, "/v1/responses/r2?stream=true&starting_after=0", nil)
			rec := httptest.NewRecorder()
			c := e.NewContext(req, rec)
			c.SetParamNames("id")
			c.SetParamValues("r2")
			c.Set("auth_user", &auth.User{ID: "alice"})
			done := make(chan struct{})
			go func() {
				defer GinkgoRecover()
				defer close(done)
				Expect(GetResponseEndpoint()(c)).To(Succeed())
			}()
			Consistently(done, 400*time.Millisecond).ShouldNot(BeClosed())
			Expect(owner.AppendEvent("r2", durableEvent(2, "response.output_item.done"))).To(Succeed())
			Expect(owner.UpdateResponse("r2", durableResponse("r2", schema.ORStatusCompleted))).To(Succeed())
			Eventually(done, 5*time.Second).Should(BeClosed())
			body := rec.Body.String()
			Expect(strings.Index(body, "response.created")).To(BeNumerically(">=", 0))
			Expect(body).To(ContainSubstring("response.output_item.done"))
			Expect(body).To(HaveSuffix("data: [DONE]\n\n"))
		})
	})
}

func (s *ResponseStore) db() *durableBackend { return s.durable() }

var _ = Describe("ResponseStore contract on the memory store", func() {
	storeContract(func() *ResponseStore { return NewResponseStore(0) }, func(s *ResponseStore) *ResponseStore { return s })
})

var _ = Describe("ResponseStore on SQLite", func() {
	tableSpecs(func() *gorm.DB {
		db, err := sqlitedb.Open("file:"+filepath.Join(GinkgoT().TempDir(), "responses.db")+"?_busy_timeout=5000&_txlock=immediate", &gorm.Config{})
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(func() {
			if sqlDB, err := db.DB(); err == nil {
				_ = sqlDB.Close()
			}
		})
		return db
	})
})

// The PostgreSQL run starts one container for the group, as the other
// database tests of the repo do.
var _ = Describe("ResponseStore on PostgreSQL", Ordered, func() {
	var pg *gorm.DB
	BeforeAll(func() { pg = testutil.SetupTestDB() })
	tableSpecs(func() *gorm.DB {
		_ = pg.Exec("DELETE FROM open_response_events").Error
		_ = pg.Exec("DELETE FROM open_responses").Error
		return pg
	})
})
