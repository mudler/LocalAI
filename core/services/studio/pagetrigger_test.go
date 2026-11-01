package studio_test

import (
	"context"
	"strings"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/mudler/LocalAI/core/services/studio"
	"github.com/mudler/LocalAI/core/services/studio/pages"
	"github.com/mudler/LocalAI/core/services/studio/studiotest"
	"github.com/mudler/LocalAI/core/services/studio/triggers"
	"github.com/mudler/LocalAI/core/services/studio/workspace"
)

// A page feeds a file trigger: what the bridge's files.write stores under a
// watched folder of the page starts a run, and a page that writes again and
// again cannot start runs faster than the trigger's gap.
var _ = Describe("a page feeds a file trigger", func() {
	It("a bridge write under a watched folder starts a run and a loop of writes waits for the gap", func() {
		sh := newShared()
		ctx := context.Background()
		Expect(sh.files.EnsureHome(ctx, alice)).To(Succeed())
		act := sh.files.Actor(alice)
		dir, _ := sh.files.Abs(alice, "/pages/ask")
		_, err := sh.files.Store().Put(ctx, act, dir+"/index.html", strings.NewReader("<p>ask</p>"), workspace.PutOptions{})
		Expect(err).NotTo(HaveOccurred())
		_, err = sh.files.Store().Mkdir(ctx, act, dir+"/requests")
		Expect(err).NotTo(HaveOccurred())

		r := sh.rig(func(workspace.Actor, string) []studiotest.ToolStep {
			return []studiotest.ToolStep{{Tool: "write_file", Args: `{"path":"/pages/ask/answers.json","content":"{}"}`}, {Say: "answered"}}
		}, studio.TriggerOptions{})
		tr := r.schedule(studio.TriggerInput{Kind: triggers.KindFile, Folder: "/pages/ask/requests", Quiet: time.Second, Name: "Page requests"})
		r.pass()

		bridge := pages.New(sh.files)
		_, err = bridge.Write(ctx, alice, "ask", "requests/1.json", []byte(`{"text":"hello"}`), 0)
		Expect(err).NotTo(HaveOccurred())
		r.pass()
		sh.clock.Advance(2 * time.Second)
		r.pass()
		run := r.waitEnded(tr.ID)
		Expect(run.Status).To(Equal(triggers.StatusDone), "run = %+v", run)
		Expect(run.File).To(Equal(dir+"/requests/1.json"), "run = %+v", run)
		Expect(r.h.Sent(r.chatOf(run).ID)[0]).To(ContainSubstring("/pages/ask/requests/1.json"))

		// The page reacts to the answer and writes a request again, three
		// times in a few seconds: one more run, after the gap.
		for i := range 3 {
			_, err = bridge.Write(ctx, alice, "ask", "requests/1.json", []byte(`{"text":"again `+string(rune('a'+i))+`"}`), 0)
			Expect(err).NotTo(HaveOccurred())
			r.pass()
			sh.clock.Advance(2 * time.Second)
			r.pass()
		}
		runs, _ := r.host.TriggerRuns(ctx, alice, tr.ID, 0)
		Expect(runs).To(HaveLen(1), "a write inside the gap started a run")
		sh.clock.Advance(time.Minute)
		r.pass()
		Expect(r.waitEnded(tr.ID).File).To(Equal(dir + "/requests/1.json"))
		runs, _ = r.host.TriggerRuns(ctx, alice, tr.ID, 0)
		Expect(runs).To(HaveLen(2), "the waiting write did not run once")
	})
})
