package grpc

import (
	"context"

	"github.com/mudler/LocalAI/pkg/grpc/base"
	"github.com/mudler/LocalAI/pkg/grpc/grpcerrors"
	pb "github.com/mudler/LocalAI/pkg/grpc/proto"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// identityBackend records what it was loaded with and answers every inference
// RPC successfully. Any request that reaches it has passed the identity guard,
// so `served` is the signal for "the guard let this through".
type identityBackend struct {
	base.SingleThread

	loaded string
	served int
}

func (b *identityBackend) Load(opts *pb.ModelOptions) error {
	b.loaded = opts.Model
	return nil
}

func (b *identityBackend) Predict(*pb.PredictOptions) (string, error) {
	b.served++
	return "ok", nil
}

func (b *identityBackend) PredictStream(_ *pb.PredictOptions, ch chan string) error {
	b.served++
	ch <- "ok"
	close(ch)
	return nil
}

func (b *identityBackend) Embeddings(*pb.PredictOptions) ([]float32, error) {
	b.served++
	return []float32{1}, nil
}

func (b *identityBackend) TokenizeString(*pb.PredictOptions) (pb.TokenizationResponse, error) {
	b.served++
	return pb.TokenizationResponse{Length: 1}, nil
}

var _ AIModel = (*identityBackend)(nil)

// callAll exercises the four PredictOptions RPCs and returns the first error.
// All four share one guard, so all four must behave identically.
func callAll(c Backend, in *pb.PredictOptions) []error {
	ctx := context.Background()
	errs := []error{}

	_, err := c.Predict(ctx, in)
	errs = append(errs, err)

	errs = append(errs, c.PredictStream(ctx, in, func(*pb.Reply) {}))

	_, err = c.Embeddings(ctx, in)
	errs = append(errs, err)

	_, err = c.TokenizeString(ctx, in)
	errs = append(errs, err)

	return errs
}

var _ = Describe("PredictOptions model identity guard", func() {
	newServed := func(addr, loadedModel string) (Backend, *identityBackend) {
		b := &identityBackend{}
		Provide(addr, b)
		c := NewClient(addr, true, nil, false)
		_, err := c.LoadModel(context.Background(), &pb.ModelOptions{Model: loadedModel})
		Expect(err).ToNot(HaveOccurred())
		Expect(b.loaded).To(Equal(loadedModel))
		return c, b
	}

	It("rejects every PredictOptions RPC when the identity names another model", func() {
		c, b := newServed("test://identity-mismatch", "a.gguf")

		for _, err := range callAll(c, &pb.PredictOptions{ModelIdentity: "b.gguf", Prompt: "hi"}) {
			Expect(err).To(HaveOccurred())
			Expect(grpcerrors.IsModelMismatch(err)).To(BeTrue(), "want a mismatch error, got %v", err)
			// The router reacts differently to the two signals, so a mismatch
			// must never be mistaken for a not-loaded.
			Expect(grpcerrors.IsModelNotLoaded(err)).To(BeFalse())
		}
		Expect(b.served).To(Equal(0), "no request may reach the model on a mismatch")
	})

	It("serves when the identity matches the loaded model", func() {
		c, b := newServed("test://identity-match", "a.gguf")

		for _, err := range callAll(c, &pb.PredictOptions{ModelIdentity: "a.gguf", Prompt: "hi"}) {
			Expect(err).ToNot(HaveOccurred())
		}
		Expect(b.served).To(Equal(4))
	})

	// Compatibility, old controller -> new backend. Every existing deployment
	// sends no identity, and tests/e2e-backends/backend_test.go drives real
	// backends with bare PredictOptions at 8+ call sites. Tightening this
	// breaks all of them, so it must fail here first.
	It("serves when the request carries no identity", func() {
		c, b := newServed("test://identity-empty-request", "a.gguf")

		for _, err := range callAll(c, &pb.PredictOptions{Prompt: "hi"}) {
			Expect(err).ToNot(HaveOccurred())
		}
		Expect(b.served).To(Equal(4))
	})

	// The backend side of the same rule: a model loaded without an identity
	// (an old controller did the load) cannot judge anything, so it must serve.
	It("serves when the backend has no recorded identity", func() {
		c, b := newServed("test://identity-empty-loaded", "")

		for _, err := range callAll(c, &pb.PredictOptions{ModelIdentity: "b.gguf", Prompt: "hi"}) {
			Expect(err).ToNot(HaveOccurred())
		}
		Expect(b.served).To(Equal(4))
	})
})
