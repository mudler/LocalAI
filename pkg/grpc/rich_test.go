package grpc

import (
	"context"
	"errors"

	"github.com/mudler/LocalAI/pkg/grpc/base"
	pb "github.com/mudler/LocalAI/pkg/grpc/proto"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// richBackend implements AIModel + AIModelRich. The legacy methods
// return scripted errors so a test that touches them by accident
// (instead of taking the rich path) fails loudly rather than silently
// returning empty content.
type richBackend struct {
	base.SingleThread

	predictRich       func(*pb.PredictOptions) (*pb.Reply, error)
	predictStreamRich func(*pb.PredictOptions, chan<- *pb.Reply) error
}

func (r *richBackend) Predict(*pb.PredictOptions) (string, error) {
	return "", errors.New("richBackend: legacy Predict should not have been called")
}

func (r *richBackend) PredictStream(*pb.PredictOptions, chan string) error {
	return errors.New("richBackend: legacy PredictStream should not have been called")
}

func (r *richBackend) PredictRich(opts *pb.PredictOptions) (*pb.Reply, error) {
	return r.predictRich(opts)
}

func (r *richBackend) PredictStreamRich(opts *pb.PredictOptions, out chan<- *pb.Reply) error {
	return r.predictStreamRich(opts, out)
}

var _ AIModelRich = (*richBackend)(nil)

var _ = Describe("AIModelRich dispatch", func() {
	It("server.Predict routes through PredictRich when implemented", func() {
		addr := "test://rich-predict"
		Provide(addr, &richBackend{
			predictRich: func(*pb.PredictOptions) (*pb.Reply, error) {
				return &pb.Reply{
					Message:      []byte("hello"),
					PromptTokens: 5,
					Tokens:       7,
					ChatDeltas: []*pb.ChatDelta{{
						ToolCalls: []*pb.ToolCallDelta{{
							Index: 0, Id: "call_1", Name: "ping", Arguments: "{}",
						}},
					}},
				}, nil
			},
		})
		c := NewClient(addr, true, nil, false)

		reply, err := c.Predict(context.Background(), &pb.PredictOptions{})
		Expect(err).NotTo(HaveOccurred())
		Expect(string(reply.GetMessage())).To(Equal("hello"))
		// Rich fields survive the RPC marshal/unmarshal — proves the
		// server used PredictRich, not the legacy (string, error)
		// wrapper which would have lost everything except Message.
		Expect(reply.GetPromptTokens()).To(BeEquivalentTo(5))
		Expect(reply.GetTokens()).To(BeEquivalentTo(7))
		Expect(reply.GetChatDeltas()).To(HaveLen(1))
		Expect(reply.GetChatDeltas()[0].GetToolCalls()).To(HaveLen(1))
		Expect(reply.GetChatDeltas()[0].GetToolCalls()[0].GetName()).To(Equal("ping"))
	})

	It("server.PredictStream routes through PredictStreamRich when implemented", func() {
		addr := "test://rich-stream"
		Provide(addr, &richBackend{
			predictStreamRich: func(_ *pb.PredictOptions, out chan<- *pb.Reply) error {
				out <- &pb.Reply{
					Message:    []byte("hi"),
					ChatDeltas: []*pb.ChatDelta{{Content: "hi"}},
				}
				out <- &pb.Reply{
					ChatDeltas: []*pb.ChatDelta{{ToolCalls: []*pb.ToolCallDelta{{
						Index: 0, Id: "call_x", Name: "search",
					}}}},
				}
				out <- &pb.Reply{Tokens: 9}
				return nil
			},
		})
		c := NewClient(addr, true, nil, false)

		var collected []*pb.Reply
		err := c.PredictStream(context.Background(), &pb.PredictOptions{}, func(r *pb.Reply) {
			collected = append(collected, r)
		})
		Expect(err).NotTo(HaveOccurred())
		Expect(collected).To(HaveLen(3))
		Expect(string(collected[0].GetMessage())).To(Equal("hi"))
		Expect(collected[1].GetChatDeltas()).To(HaveLen(1))
		Expect(collected[1].GetChatDeltas()[0].GetToolCalls()).To(HaveLen(1))
		Expect(collected[2].GetTokens()).To(BeEquivalentTo(9))
	})

	It("falls back to legacy Predict when AIModelRich is not implemented", func() {
		// Use a non-Rich model (just base.SingleThread embedded in a
		// minimal wrapper). The legacy wrapper path stringifies the
		// reply, so ChatDeltas are lost — the fallback is the contract
		// for backends that haven't migrated.
		addr := "test://legacy-predict"
		Provide(addr, &legacyOnlyBackend{response: "legacy hello"})
		c := NewClient(addr, true, nil, false)

		reply, err := c.Predict(context.Background(), &pb.PredictOptions{})
		Expect(err).NotTo(HaveOccurred())
		Expect(string(reply.GetMessage())).To(Equal("legacy hello"))
		Expect(reply.GetChatDeltas()).To(BeEmpty())
	})
})

// legacyOnlyBackend implements AIModel but NOT AIModelRich.
type legacyOnlyBackend struct {
	base.SingleThread
	response string
}

func (l *legacyOnlyBackend) Predict(*pb.PredictOptions) (string, error) {
	return l.response, nil
}
