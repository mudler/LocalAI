//go:build linux || darwin

package runner_test

import (
	"context"
	"io"
	"strings"

	"github.com/mudler/LocalAI/core/services/studio/bgjob"
	"github.com/mudler/LocalAI/core/services/studio/sandbox"
	"github.com/mudler/LocalAI/core/services/studio/sandbox/runner"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("the final checkpoint of a background job", func() {
	It("commits what the job wrote, and does nothing for a sandbox that is gone", func() {
		r := newJobRig(4)
		req := r.request("c1")
		done := make(chan struct{})
		release, err := r.m.StartJob(r.ctx, req, func(sb sandbox.Sandbox) (bgjob.EndFunc, error) {
			proc, err := sb.Exec(context.Background(), sandbox.Cmd{Args: []string{"sh", "-c", "echo made > out.txt"}, Dir: runner.GuestHome})
			if err != nil {
				return nil, err
			}
			go func() { _, _ = proc.Wait(); close(done) }()
			return func(bgjob.EndReason) {}, nil
		})
		Expect(err).NotTo(HaveOccurred())
		Eventually(done).Should(BeClosed())
		Expect(r.m.CheckpointJob(r.ctx, req, "Job job-abcdef: build")).To(Succeed())
		release()
		rc, _, err := r.st.Open(r.ctx, admin, "/users/alice/out.txt")
		Expect(err).NotTo(HaveOccurred())
		b, _ := io.ReadAll(rc)
		_ = rc.Close()
		Expect(strings.TrimSpace(string(b))).To(Equal("made"))

		Expect(r.m.CheckpointJob(r.ctx, r.request("nochat"), "x")).To(Succeed(), "no sandbox, nothing to commit")
		Expect(r.m.CheckpointJob(r.ctx, runner.Request{User: "alice"}, "x")).To(MatchError(runner.ErrInvalid))
	})
})
