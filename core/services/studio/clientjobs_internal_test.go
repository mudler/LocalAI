package studio

import (
	"context"
	"log/slog"

	"github.com/mudler/LocalAI/core/services/studio/builtins"
	"github.com/mudler/LocalAI/core/services/studio/jobs"
	"github.com/mudler/LocalAI/core/services/studio/session"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// A job that an add-on started for a paired client reads and reports only,
// unless the owner allows write and act tools for client jobs, and then it holds
// the grants of the task's agent. An ordinary job holds the same grants.
var _ = Describe("the action policy of a client job", func() {
	ctx := context.Background()
	write := builtins.ActTool{Name: "write_file", Class: builtins.ClassFiles}
	read := builtins.ActTool{Name: "read_file", Read: true}

	var (
		s     *Service
		allow bool
	)
	chat := func(id string) session.ChatRecord {
		return session.ChatRecord{ID: id, Principal: "alice", Origin: session.OriginJob, OriginID: "t"}
	}

	BeforeEach(func() {
		st, err := jobs.NewJSON("")
		Expect(err).NotTo(HaveOccurred())
		for _, j := range []jobs.Job{
			{ID: "cj", Owner: "alice", TaskID: "t", ClientID: "d1"},
			{ID: "own", Owner: "alice", TaskID: "t"},
		} {
			Expect(st.PutJob(ctx, j)).To(Succeed())
		}
		allow = false
		s = &Service{log: slog.Default()}
		s.jobs = &Jobs{o: JobOptions{Store: st, ClientAllowAllTools: func() bool { return allow }}}
	})

	It("reads and reports only by default", func() {
		pol, ok := s.actPolicy(ctx, chat("cj"))
		Expect(ok).To(BeTrue())
		Expect(pol.ReadOnly).To(BeTrue())
		Expect(pol.Decide(write).Allowed).To(BeFalse())
		Expect(pol.Decide(read).Allowed).To(BeTrue())
	})

	It("holds the grants of the task's agent, and no more, when the owner allows it", func() {
		allow = true
		// No agent: an ordinary job of this task holds nothing, so neither does
		// the client job.
		pol, _ := s.actPolicy(ctx, chat("cj"))
		Expect(pol.ReadOnly).To(BeFalse())
		Expect(pol.Held).To(BeEmpty())
		Expect(pol.Decide(write).Allowed).To(BeFalse(), "the agent holds no grant")
		Expect(pol.Decide(read).Allowed).To(BeTrue())
		allow = false
		pol, _ = s.actPolicy(ctx, chat("cj"))
		Expect(pol.ReadOnly).To(BeTrue(), "the setting is read each time")
	})

	It("does not touch an ordinary job, whatever the setting says", func() {
		for _, on := range []bool{false, true} {
			allow = on
			for _, id := range []string{"own", "missing"} {
				pol, _ := s.actPolicy(ctx, chat(id))
				Expect(pol.ReadOnly).To(BeFalse(), id)
				Expect(pol.Decide(write).Allowed).To(BeFalse(), id+": no agent grants, so no acts, as before")
			}
		}
	})
})
