package nodes

import (
	"encoding/json"
	"fmt"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/mudler/LocalAI/core/services/messaging"
)

// decodeStagingEvents extracts every StagingProgressEvent the fake messaging
// client captured, in publish order.
func decodeStagingEvents(mc *fakeMessagingClient) []StagingProgressEvent {
	mc.mu.Lock()
	defer mc.mu.Unlock()
	var out []StagingProgressEvent
	for _, p := range mc.published {
		var evt StagingProgressEvent
		if err := json.Unmarshal(p.Data, &evt); err != nil {
			continue
		}
		if evt.ModelID == "" {
			continue
		}
		out = append(out, evt)
	}
	return out
}

var _ = Describe("StagingTracker cross-replica broadcast", func() {
	Context("when a publisher is wired (distributed mode)", func() {
		It("broadcasts staging progress so a peer replica surfaces an op it did not originate", func() {
			mc := &fakeMessagingClient{}
			origin := NewStagingTracker()
			origin.SetPublisher(mc)

			origin.Start("model-x", "worker-1", 1)
			origin.UpdateFile("model-x", "weights.gguf", 1, 5<<30, 10<<30, "100 MiB/s")

			events := decodeStagingEvents(mc)
			Expect(events).ToNot(BeEmpty(), "writes must be broadcast over NATS")
			Expect(mc.published[0].Subject).To(Equal(messaging.SubjectStagingProgress("model-x")))

			// A peer replica that never ran the op merges the broadcast.
			peer := NewStagingTracker()
			for _, evt := range events {
				peer.ApplyRemote(evt)
			}

			all := peer.GetAll()
			Expect(all).To(HaveKey("model-x"))
			Expect(all["model-x"].NodeName).To(Equal("worker-1"))
			Expect(all["model-x"].FileName).To(Equal("weights.gguf"))
			Expect(all["model-x"].TotalBytes).To(Equal(int64(10 << 30)))
		})

		It("removes the op from the peer when the origin completes it", func() {
			mc := &fakeMessagingClient{}
			origin := NewStagingTracker()
			origin.SetPublisher(mc)

			origin.Start("model-x", "worker-1", 1)
			origin.Complete("model-x")

			peer := NewStagingTracker()
			for _, evt := range decodeStagingEvents(mc) {
				peer.ApplyRemote(evt)
			}
			Expect(peer.GetAll()).ToNot(HaveKey("model-x"))
		})

		It("does not let a peer broadcast clobber an op this replica is itself running", func() {
			local := NewStagingTracker()
			local.Start("model-x", "worker-local", 2)
			local.UpdateFile("model-x", "weights.gguf", 1, 9<<30, 10<<30, "")

			// A stray/older remote event for the SAME modelID must not overwrite
			// the authoritative local state, nor delete it.
			local.ApplyRemote(StagingProgressEvent{
				ModelID: "model-x",
				Status:  &StagingStatus{ModelID: "model-x", NodeName: "worker-other", FileName: "stale.gguf"},
			})
			local.ApplyRemote(StagingProgressEvent{ModelID: "model-x", Done: true})

			all := local.GetAll()
			Expect(all).To(HaveKey("model-x"))
			Expect(all["model-x"].NodeName).To(Equal("worker-local"))
			Expect(all["model-x"].FileName).To(Equal("weights.gguf"))
		})
	})

	Context("when no publisher is wired (standalone mode)", func() {
		It("does not broadcast", func() {
			mc := &fakeMessagingClient{}
			t := NewStagingTracker()
			t.Start("model-x", "worker-1", 1)
			t.UpdateFile("model-x", "weights.gguf", 1, 1<<30, 10<<30, "")
			Expect(mc.published).To(BeEmpty())
		})
	})
})

var _ = Describe("SubjectStagingProgress", func() {
	It("namespaces by model id and matches the wildcard prefix", func() {
		Expect(messaging.SubjectStagingProgress("model-x")).To(Equal("staging.model-x.progress"))
		Expect(messaging.SubjectStagingProgressWildcard).To(Equal("staging.*.progress"))
	})
})

var _ = Describe("Load recovery staging broadcast", func() {
	It("ignores an old completion and out-of-order same-generation byte tick", func() {
		tracker := NewStagingTracker()
		now := time.Now()
		tracker.ApplyRemote(StagingProgressEvent{ModelID: "m", Status: &StagingStatus{ModelID: "m", Generation: "new", UpdatedAt: now, BytesSent: 80}})
		tracker.ApplyRemote(StagingProgressEvent{ModelID: "m", Generation: "old", Done: true})
		tracker.ApplyRemote(StagingProgressEvent{ModelID: "m", Status: &StagingStatus{ModelID: "m", Generation: "new", UpdatedAt: now.Add(-time.Second), BytesSent: 10}})
		Expect(tracker.Get("m").BytesSent).To(Equal(int64(80)))
	})
	It("broadcasts generation identity through completion", func() {
		mc := &fakeMessagingClient{}
		tracker := NewStagingTracker()
		tracker.SetPublisher(mc)
		tracker.Start("m", "node", 1, "generation")
		tracker.UpdateFile("m", "weights", 1, 20, 100, "")
		tracker.Complete("m")
		events := decodeStagingEvents(mc)
		Expect(events).To(HaveLen(3))
		Expect(events[0].Status.Generation).To(Equal("generation"))
		Expect(events[1].Status.Generation).To(Equal("generation"))
		Expect(events[2].Generation).To(Equal("generation"))
		Expect(events[2].Done).To(BeTrue())
	})

	It("does not let a delayed older generation overwrite newer mirrored progress", func() {
		tracker := NewStagingTracker()
		for _, entry := range []struct {
			generation string
			started    string
			bytes      int
		}{{"new", "2026-10-05T10:00:00Z", 80}, {"old", "2026-10-04T10:00:00Z", 10}} {
			raw, err := json.Marshal(map[string]any{"model_id": "model", "status": map[string]any{"model_id": "model", "generation": entry.generation, "started_at": entry.started, "bytes_sent": entry.bytes}})
			Expect(err).NotTo(HaveOccurred())
			var event StagingProgressEvent
			Expect(json.Unmarshal(raw, &event)).To(Succeed())
			tracker.ApplyRemote(event)
		}
		Expect(tracker.Get("model").BytesSent).To(Equal(int64(80)))
	})
})

var _ = Describe("Load recovery generation cache", func() {
	It("selects exact generations independently of clocks and retains completion fences", func() {
		tracker := NewStagingTracker()
		now := time.Now()
		send := func(gen string, start, update time.Time, bytes int64) {
			tracker.ApplyRemote(StagingProgressEvent{ModelID: "m", Status: &StagingStatus{ModelID: "m", Generation: gen, StartedAt: start, UpdatedAt: update, BytesSent: bytes}})
		}
		tracker.Start("m", "old-local", 1, "local")
		send("current", now.Add(-time.Hour), now, 80)
		send("current", now, now.Add(-time.Second), 20)
		send("old", now.Add(time.Hour), now.Add(time.Hour), 10)
		Expect(tracker.GetGeneration("m", "current").BytesSent).To(Equal(int64(80)))
		Expect(tracker.Get("m").Generation).To(Equal("local"))
		tracker.ApplyRemote(StagingProgressEvent{ModelID: "m", Generation: "current", Done: true})
		send("current", now, now.Add(time.Hour), 99)
		Expect(tracker.GetGeneration("m", "current")).To(BeNil())
		Expect(tracker.GetGeneration("m", "old").BytesSent).To(Equal(int64(10)))
	})
	It("orders legacy sessions and ticks without mixing generation-bearing progress", func() {
		tracker := NewStagingTracker()
		now := time.Now()
		send := func(start, update time.Time, bytes int64) {
			tracker.ApplyRemote(StagingProgressEvent{ModelID: "m", Status: &StagingStatus{ModelID: "m", StartedAt: start, UpdatedAt: update, BytesSent: bytes}})
		}
		send(now, now, 20)
		send(now, now.Add(time.Second), 80)
		send(now, now, 10)
		send(now.Add(-time.Hour), now.Add(time.Hour), 5)
		Expect(tracker.Get("m").BytesSent).To(Equal(int64(80)))
		send(now.Add(time.Second), now.Add(time.Second), 1)
		Expect(tracker.Get("m").BytesSent).To(Equal(int64(1)))
		tracker.ApplyRemote(StagingProgressEvent{ModelID: "m", Done: true})
		Expect(tracker.Get("m")).To(BeNil())
	})
	It("bounds hints and tombstones and expires them by receiver time", func() {
		tracker := NewStagingTracker()
		now := time.Now()
		for i := 0; i < stagingCacheLimit+1; i++ {
			gen := fmt.Sprint(i)
			tracker.ApplyRemote(StagingProgressEvent{ModelID: "m", Status: &StagingStatus{Generation: gen, UpdatedAt: now.Add(time.Hour)}})
			tracker.ApplyRemote(StagingProgressEvent{ModelID: "done", Generation: gen, Done: true})
		}
		Expect(tracker.generations).To(HaveLen(stagingCacheLimit))
		Expect(tracker.completed).To(HaveLen(stagingCacheLimit))
		tracker.mu.Lock()
		tracker.pruneRemote(now.Add(stagingDoneTTL + time.Minute))
		tracker.mu.Unlock()
		Expect(tracker.generations).To(BeEmpty())
		Expect(tracker.completed).To(BeEmpty())
		Expect(tracker.GetAll()).To(BeEmpty())
	})
})
