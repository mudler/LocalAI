package nodes

import (
	"context"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/mudler/LocalAI/core/services/workerctl"
)

var _ = Describe("model.prefetch over the bus", func() {
	It("returns when the upgrade is cancelled, not when the worker answers", func() {
		release := make(chan struct{})
		DeferCleanup(func() { close(release) })
		bus := &releaseTestMessaging{onRequest: func() { <-release }}
		ctx, cancel := context.WithCancel(context.Background())
		done := make(chan error, 1)
		go func() {
			_, err := NATSModelFilePrefetcher{Bus: bus}.PrefetchModelFiles(ctx, "node-a",
				workerctl.ModelPrefetchRequest{Action: workerctl.PrefetchStage, ModelName: "tiny"}, 2*time.Hour)
			done <- err
		}()
		Consistently(done, "50ms").ShouldNot(Receive())
		cancel()
		Eventually(done, "1s").Should(Receive(MatchError(context.Canceled)))
	})

	It("does not send a request for an upgrade that is already cancelled", func() {
		bus := &releaseTestMessaging{}
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		_, err := NATSModelFilePrefetcher{Bus: bus}.PrefetchModelFiles(ctx, "node-a",
			workerctl.ModelPrefetchRequest{Action: workerctl.PrefetchStage, ModelName: "tiny"}, time.Minute)
		Expect(err).To(MatchError(context.Canceled))
		Expect(bus.requestCalled).To(BeFalse())
	})

	It("answers with the worker's reply", func() {
		bus := &releaseTestMessaging{replies: [][]byte{[]byte(`{"success":true,"placed":["tiny.gguf"]}`)}}
		reply, err := NATSModelFilePrefetcher{Bus: bus}.PrefetchModelFiles(context.Background(), "node-a",
			workerctl.ModelPrefetchRequest{Action: workerctl.PrefetchCommit, ModelName: "tiny"}, time.Minute)
		Expect(err).ToNot(HaveOccurred())
		Expect(reply.Placed).To(ConsistOf("tiny.gguf"))
	})
})
