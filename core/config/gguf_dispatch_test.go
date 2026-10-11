// SPDX-License-Identifier: MIT
package config

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/mudler/LocalAI/pkg/diagnostics"
	"github.com/mudler/LocalAI/pkg/grpc"
	pb "github.com/mudler/LocalAI/pkg/grpc/proto"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	grpclib "google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"time"
)

type diagnosticMetadataBackend struct {
	grpc.Backend
	calls int
	reply *pb.ModelMetadataResponse
	err   error
}

func (b *diagnosticMetadataBackend) ModelMetadata(context.Context, *pb.ModelOptions, ...grpclib.CallOption) (*pb.ModelMetadataResponse, error) {
	b.calls++
	time.Sleep(time.Millisecond)
	return b.reply, b.err
}

var _ = Describe("Metadata dispatch regression", func() {
	It("classifies context and gRPC cancellation without extra calls", func() {
		for _, err := range []error{context.Canceled, context.DeadlineExceeded, status.Error(codes.Canceled, "private"), status.Error(codes.DeadlineExceeded, "private"), fmt.Errorf("wrapped: %w", status.Error(codes.Canceled, "private"))} {
			var events []diagnostics.Event
			ctx := diagnostics.NewRecorder(func(e diagnostics.Event) { events = append(events, e) }).Request(context.Background())
			b := &diagnosticMetadataBackend{err: err}
			DetectThinkingSupportFromBackend(ctx, &ModelConfig{}, b, &pb.ModelOptions{})
			diagnostics.FlushDispatch(ctx)
			ends := 0
			for _, e := range events {
				if e.Phase == diagnostics.PhaseMetadataDecision && e.State == diagnostics.StateEnd {
					ends++
					Expect(e.Outcome).To(Equal(diagnostics.OutcomeCanceled), "error: %v", err)
					Expect(e.Count).To(Equal(1))
				}
			}
			Expect(ends).To(Equal(1))
			Expect(b.calls).To(Equal(1))
		}
	})

	It("distinguishes marker presence from response emptiness without caching", func() {
		for _, reply := range []*pb.ModelMetadataResponse{nil, {}, {SupportsThinking: true}, {RenderedTemplate: "unrelated-sentinel"}, {MediaMarker: "private-marker"}} {
			for _, enabled := range []bool{false, true} {
				var events []diagnostics.Event
				ctx := context.Background()
				if enabled {
					ctx = diagnostics.NewRecorder(func(e diagnostics.Event) { events = append(events, e) }).Request(ctx)
				}
				b := &diagnosticMetadataBackend{reply: reply}
				cfg := &ModelConfig{}
				for range 2 {
					DetectThinkingSupportFromBackend(ctx, cfg, b, &pb.ModelOptions{})
				}
				diagnostics.FlushDispatch(ctx)
				want := diagnostics.Phase("metadata_marker_absent")
				if reply.GetMediaMarker() != "" {
					want = "metadata_marker_present"
				}
				markers := 0
				for _, e := range events {
					if e.Phase == "metadata_marker_absent" || e.Phase == "metadata_marker_present" {
						markers++
						Expect(e.Phase).To(Equal(want))
					}
				}
				if enabled {
					Expect(markers).To(Equal(2))
					resultPhase := diagnostics.PhaseMetadataEmpty
					if reply != nil && (reply.SupportsThinking || reply.RenderedTemplate != "" || reply.MediaMarker != "") {
						resultPhase = diagnostics.PhaseMetadataNonempty
					}
					results := 0
					for _, e := range events {
						if e.Phase == resultPhase {
							results++
						}
					}
					Expect(results).To(Equal(2))
				} else {
					Expect(events).To(BeEmpty())
				}
				encoded, err := json.Marshal(events)
				Expect(err).NotTo(HaveOccurred())
				Expect(string(encoded)).NotTo(ContainSubstring("private-marker"))
				Expect(string(encoded)).NotTo(ContainSubstring("unrelated-sentinel"))
				Expect(b.calls).To(Equal(2))
				Expect(cfg.MediaMarker).To(Equal(reply.GetMediaMarker()))
			}
		}
	})
})

var _ = Describe("Metadata dispatch", func() {
	It("separates decisions and results without caching empty results or claiming RPC events", func() {
		for _, tc := range []struct {
			reply   *pb.ModelMetadataResponse
			err     error
			phase   diagnostics.Phase
			outcome diagnostics.Outcome
		}{
			{nil, nil, diagnostics.PhaseMetadataEmpty, diagnostics.OutcomeOK},
			{&pb.ModelMetadataResponse{}, nil, diagnostics.PhaseMetadataEmpty, diagnostics.OutcomeOK},
			{&pb.ModelMetadataResponse{MediaMarker: "sentinel"}, nil, diagnostics.PhaseMetadataNonempty, diagnostics.OutcomeOK},
			{nil, errors.New("private error"), "", diagnostics.OutcomeError},
			{nil, context.Canceled, "", diagnostics.OutcomeCanceled},
		} {
			var events []diagnostics.Event
			ctx := diagnostics.NewRecorder(func(e diagnostics.Event) { events = append(events, e) }).Request(context.Background())
			b := &diagnosticMetadataBackend{reply: tc.reply, err: tc.err}
			cfg := &ModelConfig{}
			for range 2 {
				DetectThinkingSupportFromBackend(ctx, cfg, b, &pb.ModelOptions{})
			}
			diagnostics.FlushDispatch(ctx)
			Expect(b.calls).To(Equal(2))
			var ends, results int
			for _, e := range events {
				Expect(e.Phase).NotTo(Equal(diagnostics.PhaseMetadataRPC))
				if e.Phase == diagnostics.PhaseMetadataDecision && e.State == diagnostics.StateEnd {
					ends++
					Expect(e.Outcome).To(Equal(tc.outcome))
					Expect(e.Elapsed).To(BeNumerically(">=", time.Millisecond))
				}
				if e.Phase == tc.phase {
					results++
				}
			}
			Expect(ends).To(Equal(2))
			if tc.phase != "" {
				Expect(results).To(Equal(2))
			}
		}
	})
})
