package application

import (
	"context"
	"strings"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/mudler/LocalAI/core/services/studio"
	"github.com/mudler/LocalAI/core/services/studio/hostexec"
)

var _ = Describe("the audit of the jobs of the Sandbox", func() {
	It("keeps the start, input and kill under Sandbox kinds, never as a command on this machine", func() {
		store, err := hostexec.NewJSONAudit("")
		Expect(err).NotTo(HaveOccurred())
		audit := sandboxJobAudit(func() hostexec.AuditStore { return store })
		at := time.Now().UTC()
		audit(context.Background(), studio.SandboxJobEvent{Kind: "job_start", User: "u1", Chat: "c1", Addon: "sandbox", Text: "make all", At: at})
		audit(context.Background(), studio.SandboxJobEvent{Kind: "job_input", User: "u1", Chat: "c1", Addon: "sandbox", Job: "job-aaaaab", Text: strings.Repeat("x", 10), At: at})
		audit(context.Background(), studio.SandboxJobEvent{Kind: "job_kill", User: "u1", Chat: "c1", Addon: "sandbox", Job: "job-aaaaab", At: at})
		audit(context.Background(), studio.SandboxJobEvent{Kind: "unknown", User: "u1", Chat: "c1", At: at})

		got, err := store.Events(context.Background(), 10)
		Expect(err).NotTo(HaveOccurred())
		Expect(got).To(HaveLen(3))
		kinds := []string{}
		for _, ev := range got {
			kinds = append(kinds, ev.Kind)
			Expect(ev.User).To(Equal("u1"))
		}
		Expect(kinds).To(ConsistOf(hostexec.KindSandboxJobStart, hostexec.KindSandboxJobInput, hostexec.KindSandboxJobKill))
		for _, k := range kinds {
			Expect(len(k)).To(BeNumerically("<=", 16), "fits the Kind column")
			Expect(k).NotTo(Equal(hostexec.KindRun))
		}
	})
})
