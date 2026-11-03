package studio_test

import (
	"context"
	"fmt"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/mudler/LocalAI/core/services/studio"
	"github.com/mudler/LocalAI/core/services/studio/agents"
	"github.com/mudler/LocalAI/core/services/studio/files"
	"github.com/mudler/LocalAI/core/services/studio/session"
	"github.com/mudler/LocalAI/core/services/studio/triggers"
	"github.com/mudler/LocalAI/core/services/studio/workspace"
)

// Thirty-one disjoint shares exceed the budget even without any skill roots:
// one agent root and one reserved chat root already consume two slots.
func backgroundCapacityShares(f *files.Service) []workspace.Node {
	GinkgoHelper()
	ctx := context.Background()
	admin := workspace.Actor{Principal: files.AdminPrincipal}
	var roots []workspace.Node
	for i := 0; i < 31; i++ {
		n, err := f.Store().Mkdir(ctx, admin, fmt.Sprintf("/shared/Capacity%02d", i))
		Expect(err).NotTo(HaveOccurred())
		Expect(f.Store().Grant(ctx, admin, files.EveryoneGroup, n.Path, workspace.ModeRead)).To(Succeed())
		roots = append(roots, n)
	}
	return roots
}

var _ = Describe("background folder capacity", func() {
	It("answers a connector once with a fixed capacity refusal and keeps its binding enabled", func() {
		ctx := context.Background()
		sh, bot := newShared(), newScriptedBot()
		Expect(sh.files.EnsureHome(ctx, alice)).To(Succeed())
		_, err := sh.files.Store().Mkdir(ctx, sh.files.Actor(alice), session.HomePath(alice)+"/Work")
		Expect(err).NotTo(HaveOccurred())
		r := newConnRig(sh, connStore(), bot, connSay("Hello."), nil)
		r.agent, err = r.svc.SaveAgentWithFolder(ctx, alice, agents.Agent{Name: "Scoped", Model: "m"}, &studio.AgentFolderWrite{Path: "/Work", Mode: agents.FolderRead})
		Expect(err).NotTo(HaveOccurred())
		binding := r.bind(studio.ConnectorInput{Allow: []string{"11"}})
		backgroundCapacityShares(sh.files)
		bot.say(1, 11, "Ada", "ada", "hi")
		waitFor("the capacity answer", func() bool { return len(bot.answers()) >= 1 })
		Consistently(bot.answers, 300*time.Millisecond).Should(Equal([]string{"This agent cannot start because its file access exceeds the folder limit. Ask an administrator to reduce the shared folders available to you, then try again."}))
		chats, err := r.svc.List(ctx, alice, 10)
		Expect(err).NotTo(HaveOccurred())
		Expect(chats).To(BeEmpty())
		got, err := r.conns.GetConnector(ctx, alice, binding.ID)
		Expect(err).NotTo(HaveOccurred())
		Expect(got.Enabled).To(BeTrue())
		Expect(r.logs.String()).To(ContainSubstring("folder limit"))
	})

	It("fails a capacity-limited trigger without disabling it and succeeds on retry after shares change", func() {
		s := newScopedTrig(saySummary)
		tr := s.schedule(studio.TriggerInput{AgentID: s.agent.ID, Model: ""})
		roots := backgroundCapacityShares(s.sh.files)
		s.due()
		failed := s.waitEnded(tr.ID)
		Expect(failed.Status).To(Equal(triggers.StatusFailed))
		Expect(failed.Reason).To(ContainSubstring("exceeds the folder limit"))
		Expect(failed.Reason).NotTo(ContainSubstring("missing"))
		Expect(failed.ChatID).To(BeEmpty())
		got, err := s.host.GetTrigger(s.ctx, alice, tr.ID)
		Expect(err).NotTo(HaveOccurred())
		Expect(got.Enabled).To(BeTrue())
		chats, err := s.svc.List(s.ctx, alice, 10)
		Expect(err).NotTo(HaveOccurred())
		Expect(chats).To(BeEmpty())
		admin := workspace.Actor{Principal: files.AdminPrincipal}
		for _, n := range roots {
			Expect(s.sh.files.Store().Revoke(s.ctx, admin, files.EveryoneGroup, n.Path)).To(Succeed())
		}
		_, err = s.host.RunTrigger(s.ctx, alice, tr.ID)
		Expect(err).NotTo(HaveOccurred())
		done := s.waitEnded(tr.ID)
		Expect(done.ID).NotTo(Equal(failed.ID))
		Expect(done.Status).To(Equal(triggers.StatusDone))
		Expect(done.ChatID).NotTo(BeEmpty())
		got, err = s.host.GetTrigger(s.ctx, alice, tr.ID)
		Expect(err).NotTo(HaveOccurred())
		Expect(got.Enabled).To(BeTrue())
	})
})
