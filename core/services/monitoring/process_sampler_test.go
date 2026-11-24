package monitoring

import (
	"os/exec"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("LocalProcessSampler", func() {
	var (
		sampler *LocalProcessSampler
		cmd     *exec.Cmd
		pid     int32
	)

	BeforeEach(func() {
		sampler = NewLocalProcessSampler()
		cmd = exec.Command("sleep", "30")
		Expect(cmd.Start()).To(Succeed())
		pid = int32(cmd.Process.Pid)
	})

	AfterEach(func() {
		_ = cmd.Process.Kill()
		_, _ = cmd.Process.Wait()
	})

	It("reads resident memory and start time of a live process", func() {
		proc, err := sampler.Sample(pid)
		Expect(err).ToNot(HaveOccurred())
		Expect(proc.PID).To(Equal(pid))
		Expect(proc.RSSBytes).To(BeNumerically(">", 0))
		Expect(proc.StartedAt).To(BeTemporally("~", time.Now(), time.Minute))
	})

	It("leaves CPU unset on the first reading and reports it once there is a delta", func() {
		first, err := sampler.Sample(pid)
		Expect(err).ToNot(HaveOccurred())
		Expect(first.CPUPercent).To(BeNil())

		second, err := sampler.Sample(pid)
		Expect(err).ToNot(HaveOccurred())
		Expect(second.CPUPercent).ToNot(BeNil())
		Expect(*second.CPUPercent).To(BeNumerically(">=", 0))
	})

	It("starts a PID over after Retain drops it", func() {
		_, err := sampler.Sample(pid)
		Expect(err).ToNot(HaveOccurred())

		sampler.Retain(map[int32]struct{}{})

		again, err := sampler.Sample(pid)
		Expect(err).ToNot(HaveOccurred())
		Expect(again.CPUPercent).To(BeNil())
	})

	It("keeps a PID that is still live across Retain", func() {
		_, err := sampler.Sample(pid)
		Expect(err).ToNot(HaveOccurred())

		sampler.Retain(map[int32]struct{}{pid: {}})

		again, err := sampler.Sample(pid)
		Expect(err).ToNot(HaveOccurred())
		Expect(again.CPUPercent).ToNot(BeNil())
	})

	It("fails for a process that has exited", func() {
		Expect(cmd.Process.Kill()).To(Succeed())
		// Wait reaps it, so /proc no longer has the PID.
		_, _ = cmd.Process.Wait()

		_, err := sampler.Sample(pid)
		Expect(err).To(HaveOccurred())
	})
})
