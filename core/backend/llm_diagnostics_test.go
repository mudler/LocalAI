// SPDX-License-Identifier: MIT
package backend

import (
	"context"
	d "github.com/mudler/LocalAI/pkg/diagnostics"
	pb "github.com/mudler/LocalAI/pkg/grpc/proto"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"sync"
)

var _ = Describe("Frontend callback observation", func() {
	It("preserves tokens, empty structured deltas, return values, and usage with diagnostics on or off", func() {
		for _, enabled := range []bool{false, true} {
			var events []d.Event
			ctx := context.Background()
			if enabled {
				ctx = d.NewRecorder(func(e d.Event) { events = append(events, e) }).Request(ctx)
			}
			usage := TokenUsage{Prompt: 4, Completion: 7, ChatDeltas: []*pb.ChatDelta{{ReasoningContent: "private"}}}
			var tokens []string
			callback := observeTokenCallback(ctx, func(token string, got TokenUsage) bool {
				tokens = append(tokens, token)
				Expect(got).To(Equal(usage))
				return token != ""
			})
			Expect(callback("prefix", usage)).To(BeTrue())
			Expect(callback("", usage)).To(BeFalse())
			Expect(callback("text", usage)).To(BeTrue())
			Expect(tokens).To(Equal([]string{"prefix", "", "text"}))
			d.FlushDispatch(ctx)
			if enabled {
				Expect(events).To(HaveLen(2))
				Expect(events[0].Phase).To(Equal(d.PhaseFrontendCallback))
			} else {
				Expect(events).To(BeEmpty())
			}
		}
	})
	It("records once across concurrent callback wrappers", func() {
		var events []d.Event
		ctx := d.NewRecorder(func(e d.Event) { events = append(events, e) }).Request(context.Background())
		var wg sync.WaitGroup
		for i := 0; i < 20; i++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				observeTokenCallback(ctx, func(string, TokenUsage) bool { return true })("", TokenUsage{})
			}()
		}
		wg.Wait()
		d.FlushDispatch(ctx)
		Expect(events).To(HaveLen(2))
		Expect(events[0].Phase).To(Equal(d.PhaseFrontendCallback))
	})
})
