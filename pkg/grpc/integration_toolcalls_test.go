package grpc

import (
	"context"
	"strings"

	"github.com/mudler/LocalAI/pkg/grpc/base"
	pb "github.com/mudler/LocalAI/pkg/grpc/proto"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// toolCallStreamer simulates what a cloud-proxy translate backend
// emits: a sequence of *pb.Reply chunks carrying content + tool_call
// deltas + final usage tokens. The replies are sent in the same order
// and shape the cloud-proxy OpenAI translator produces.
type toolCallStreamer struct {
	base.SingleThread
}

func (*toolCallStreamer) Predict(*pb.PredictOptions) (string, error) {
	return "", nil
}

func (*toolCallStreamer) PredictStream(*pb.PredictOptions, chan string) error {
	return nil
}

func (*toolCallStreamer) PredictRich(*pb.PredictOptions) (*pb.Reply, error) {
	return &pb.Reply{
		Message:      []byte("done"),
		PromptTokens: 11,
		Tokens:       4,
		ChatDeltas: []*pb.ChatDelta{{
			ToolCalls: []*pb.ToolCallDelta{{
				Index: 0, Id: "call_finalize", Name: "submit", Arguments: `{"ok":true}`,
			}},
		}},
	}, nil
}

func (*toolCallStreamer) PredictStreamRich(_ *pb.PredictOptions, out chan<- *pb.Reply) error {
	// Chunk 1: opening text delta.
	out <- &pb.Reply{
		Message:    []byte("Looking up "),
		ChatDeltas: []*pb.ChatDelta{{Content: "Looking up "}},
	}
	// Chunk 2: tool call announcement (id + name).
	out <- &pb.Reply{
		ChatDeltas: []*pb.ChatDelta{{
			ToolCalls: []*pb.ToolCallDelta{{
				Index: 0, Id: "call_x", Name: "search",
			}},
		}},
	}
	// Chunks 3-4: argument fragments (consumer concatenates by index).
	out <- &pb.Reply{
		ChatDeltas: []*pb.ChatDelta{{
			ToolCalls: []*pb.ToolCallDelta{{
				Index: 0, Arguments: `{"q":"`,
			}},
		}},
	}
	out <- &pb.Reply{
		ChatDeltas: []*pb.ChatDelta{{
			ToolCalls: []*pb.ToolCallDelta{{
				Index: 0, Arguments: `weather"}`,
			}},
		}},
	}
	// Chunk 5: usage tokens (final chunk pattern from OpenAI stream).
	out <- &pb.Reply{Tokens: 17}
	return nil
}

var _ AIModelRich = &toolCallStreamer{}

var _ = Describe("Cloud-proxy translate-mode integration (gRPC + tool calls)", func() {
	// This test simulates what the OpenAI chat endpoint does after
	// ModelInference returns: it walks the per-chunk TokenUsage.ChatDeltas
	// and assembles tool calls indexed by ToolCallDelta.Index. Verifies
	// that the rich gRPC path delivers everything the consumer needs.
	It("delivers tool-call deltas through PredictStream end-to-end", func() {
		addr := "test://translate-integration-stream"
		Provide(addr, &toolCallStreamer{})
		c := NewClient(addr, true, nil, false)

		type accumulator struct {
			text   strings.Builder
			toolID string
			name   string
			args   strings.Builder
			tokens int32
		}
		var acc accumulator

		err := c.PredictStream(context.Background(), &pb.PredictOptions{}, func(reply *pb.Reply) {
			if msg := reply.GetMessage(); len(msg) > 0 {
				acc.text.Write(msg)
			}
			if reply.GetTokens() > 0 && len(reply.GetChatDeltas()) == 0 {
				acc.tokens = reply.GetTokens()
				return
			}
			for _, cd := range reply.GetChatDeltas() {
				for _, tc := range cd.GetToolCalls() {
					if tc.GetId() != "" {
						acc.toolID = tc.GetId()
					}
					if tc.GetName() != "" {
						acc.name = tc.GetName()
					}
					acc.args.WriteString(tc.GetArguments())
				}
			}
		})
		Expect(err).NotTo(HaveOccurred())

		// Text content survived the wire.
		Expect(acc.text.String()).To(Equal("Looking up "))
		// Tool call id + name landed on the first announcing chunk.
		Expect(acc.toolID).To(Equal("call_x"))
		Expect(acc.name).To(Equal("search"))
		// Argument fragments assembled in order.
		Expect(acc.args.String()).To(Equal(`{"q":"weather"}`))
		// Final usage frame propagated.
		Expect(acc.tokens).To(BeEquivalentTo(17))
	})

	It("delivers complete tool-call results through non-streaming Predict", func() {
		addr := "test://translate-integration-predict"
		Provide(addr, &toolCallStreamer{})
		c := NewClient(addr, true, nil, false)

		reply, err := c.Predict(context.Background(), &pb.PredictOptions{})
		Expect(err).NotTo(HaveOccurred())
		Expect(string(reply.GetMessage())).To(Equal("done"))
		Expect(reply.GetPromptTokens()).To(BeEquivalentTo(11))
		Expect(reply.GetTokens()).To(BeEquivalentTo(4))
		Expect(reply.GetChatDeltas()).To(HaveLen(1))
		tcs := reply.GetChatDeltas()[0].GetToolCalls()
		Expect(tcs).To(HaveLen(1))
		Expect(tcs[0].GetId()).To(Equal("call_finalize"))
		Expect(tcs[0].GetName()).To(Equal("submit"))
		Expect(tcs[0].GetArguments()).To(Equal(`{"ok":true}`))
	})
})
