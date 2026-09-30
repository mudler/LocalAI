package nodes

import (
	"context"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	grpc "github.com/mudler/LocalAI/pkg/grpc"
)

var _ = Describe("grpcModelProber", func() {
	It("builds its client through the factory with the node id and a non-parallel client", func() {
		f := &recordingFactory{next: func() grpc.Backend { return &fakeBackendClient{healthy: true} }}
		p := grpcModelProber{clients: f}

		out := p.Probe(context.Background(), "n1", "10.0.0.1:50052")

		Expect(f.calls()).To(Equal([]string{"n1@10.0.0.1:50052"}))
		Expect(f.parallelFlags()).To(Equal([]bool{false}))
		Expect(out).To(Equal(ProbeAlive))
	})

	DescribeTable("classifies the health answer",
		func(mk func() *fakeBackendClient, want ProbeOutcome) {
			f := &recordingFactory{next: func() grpc.Backend { return mk() }}
			Expect(grpcModelProber{clients: f}.Probe(context.Background(), "n1", "a:1")).To(Equal(want))
		},
		Entry("healthy", func() *fakeBackendClient { return &fakeBackendClient{healthy: true} }, ProbeAlive),
		Entry("answered but not healthy", func() *fakeBackendClient { return &fakeBackendClient{healthy: false} }, ProbeUnreachable),
		Entry("nothing listening", func() *fakeBackendClient {
			return &fakeBackendClient{err: status.Error(codes.Unavailable, "down")}
		}, ProbeUnreachable),
		Entry("no answer in time", func() *fakeBackendClient {
			return &fakeBackendClient{err: context.DeadlineExceeded}
		}, ProbeBusy),
	)
})
