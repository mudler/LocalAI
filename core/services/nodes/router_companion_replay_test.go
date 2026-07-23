package nodes

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	pb "github.com/mudler/LocalAI/pkg/grpc/proto"
)

// The reconciler's scale-up path replays a stored ModelOptions proto blob and
// never runs grpcModelOpts, so a managed companion option (e.g. longcat-video's
// base_model) is present only if it was captured in that blob. A blob stored
// before the companion resolved permanently lacks it, and the replica loads
// without the companion — the backend then fetches its own wrong default and
// fails "base_model must point to a LongCat-Video checkpoint". applyCompanionOptions
// re-derives the companion from the CURRENT config and injects any that are
// missing, so a remote replica gets the same option a fresh grpcModelOpts would.
var _ = Describe("reconciler companion option re-derivation", func() {
	It("injects a companion option the stored blob is missing", func() {
		router := &SmartRouter{
			companionOptionsFor: func(_ string) []string {
				return []string{"base_model:.artifacts/huggingface/deadbeef/snapshot"}
			},
		}
		// A blob captured before the companion resolved: no base_model.
		opts := &pb.ModelOptions{Options: []string{"attention_backend:sdpa"}}

		router.applyCompanionOptions("longcat-video-avatar-1.5", opts)

		Expect(opts.Options).To(ContainElement("base_model:.artifacts/huggingface/deadbeef/snapshot"))
		Expect(opts.Options).To(ContainElement("attention_backend:sdpa"))
	})

	It("never overrides a companion option the blob already carries", func() {
		router := &SmartRouter{
			companionOptionsFor: func(_ string) []string {
				return []string{"base_model:.artifacts/huggingface/managed/snapshot"}
			},
		}
		// An author-pinned (or already-captured) base_model must win.
		opts := &pb.ModelOptions{Options: []string{"base_model:/opt/checkouts/LongCat-Video"}}

		router.applyCompanionOptions("longcat-video-avatar-1.5", opts)

		Expect(opts.Options).To(ContainElement("base_model:/opt/checkouts/LongCat-Video"))
		base := 0
		for _, o := range opts.Options {
			if len(o) >= len("base_model:") && o[:len("base_model:")] == "base_model:" {
				base++
			}
		}
		Expect(base).To(Equal(1))
	})

	It("is a no-op when no resolver is wired (single-node)", func() {
		router := &SmartRouter{}
		opts := &pb.ModelOptions{Options: []string{"attention_backend:sdpa"}}
		router.applyCompanionOptions("m", opts)
		Expect(opts.Options).To(Equal([]string{"attention_backend:sdpa"}))
	})
})
