package studio_test

import (
	"context"
	"strings"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/mudler/LocalAI/core/services/studio"
	"github.com/mudler/LocalAI/core/services/studio/studiotest"
	"github.com/mudler/LocalAI/core/services/studio/workspace"
)

var _ = Describe("the promise guard", func() {
	ctx := context.Background()

	// promiseScript answers "promise" with the given words; the note of the
	// guard gets reply (or the same words again when reply is empty).
	promiseScript := func(words, reply string) func(workspace.Actor, string) []studiotest.ToolStep {
		return func(_ workspace.Actor, text string) []studiotest.ToolStep {
			switch {
			case strings.Contains(text, "System note"):
				if reply == "" {
					reply = words
				}
				return []studiotest.ToolStep{{Say: reply}}
			case text == "promise":
				return []studiotest.ToolStep{{Say: words}}
			}
			return []studiotest.ToolStep{{Say: "ok"}}
		}
	}
	settle := func(r *loopRig, chat string, want int) []string {
		until("the turns to end", func() bool { return len(r.h.Sent(chat)) >= want && !turn(r.svc, alice, chat).Running })
		time.Sleep(100 * time.Millisecond) // a guard turn would start by now
		idle(r.svc, alice, chat)
		return r.h.Sent(chat)
	}

	for _, words := range []string{
		"The download started. I'll check on it in a few minutes and move the file when it finishes.",
		"Ho avviato il download. Controllo tra poco.",
		"Ho avviato il download. Aspetto che finisca e poi lo sposto.",
	} {
		It("adds one note when the answer promises and nothing is armed: "+words, func() {
			r := newLoopRig(promiseScript(words, "I cannot watch it."), studio.LoopOptions{})
			chat := create(r.svc, alice)
			sent(r.svc, alice, chat, "promise")
			got := settle(r, chat, 2)
			Expect(got).To(HaveLen(2))
			Expect(got[1]).To(ContainSubstring("System note"))
			Expect(got[1]).To(ContainSubstring("Nothing is armed"))
			Expect(got[1]).To(ContainSubstring("start_loop"))
			Expect(got[1]).To(ContainSubstring("wait_for"))
			Expect(got[1]).To(ContainSubstring("spawn_agent with wait=false"))
			Expect(got[1]).To(ContainSubstring(words[strings.LastIndex(words, ". ")+2 : len(words)-1]))
			msgs, err := r.svc.Messages(ctx, alice, chat, 0, 100)
			Expect(err).NotTo(HaveOccurred())
			for _, m := range msgs {
				Expect(m.Text).NotTo(ContainSubstring("System note"), "the note is in the transcript")
			}
		})
	}

	It("never fires twice for one turn, even when the model promises again", func() {
		r := newLoopRig(promiseScript("I'll check on it again in a few minutes.", ""), studio.LoopOptions{})
		chat := create(r.svc, alice)
		sent(r.svc, alice, chat, "promise")
		got := settle(r, chat, 2)
		Expect(got).To(HaveLen(2), "the guard looped: %v", got)
	})

	It("fires again for the next turn of the person", func() {
		r := newLoopRig(promiseScript("I'll check on it in a few minutes.", "ok then"), studio.LoopOptions{})
		chat := create(r.svc, alice)
		sent(r.svc, alice, chat, "promise")
		settle(r, chat, 2)
		sent(r.svc, alice, chat, "promise")
		Expect(settle(r, chat, 4)).To(HaveLen(4))
	})

	It("stays quiet when a loop is armed in the same turn", func() {
		r := newLoopRig(func(_ workspace.Actor, text string) []studiotest.ToolStep {
			if text == "promise" {
				return []studiotest.ToolStep{{Tool: "start_loop", Args: armArgs}, {Say: "I'll check on it in a few minutes."}}
			}
			return []studiotest.ToolStep{{Say: "ok"}}
		}, studio.LoopOptions{})
		chat := create(r.svc, alice)
		sent(r.svc, alice, chat, "promise")
		Expect(settle(r, chat, 1)).To(HaveLen(1))
	})

	It("stays quiet when a wait is armed in the same turn", func() {
		src := &source{out: "x"}
		r := newLoopRig(func(_ workspace.Actor, text string) []studiotest.ToolStep {
			if text == "promise" {
				return []studiotest.ToolStep{{Tool: "wait_for", Args: `{"tool":"list_downloads",` + waitThen + `}`}, {Say: "I'll let you know when it finishes."}}
			}
			return []studiotest.ToolStep{{Say: "ok"}}
		}, studio.LoopOptions{}, src.tools("list_downloads"))
		chat := create(r.svc, alice)
		sent(r.svc, alice, chat, "promise")
		Expect(settle(r, chat, 1)).To(HaveLen(1))
	})

	It("stays quiet when a sub-agent runs in the background", func() {
		release := make(chan struct{})
		defer close(release)
		r := newLoopRig(func(_ workspace.Actor, text string) []studiotest.ToolStep {
			switch text {
			case "promise":
				return []studiotest.ToolStep{{Tool: "spawn_agent", Args: `{"task":"long job","wait":false}`}, {Say: "I'll wait for it and then report."}}
			case "long job":
				return []studiotest.ToolStep{{Block: release}}
			}
			return []studiotest.ToolStep{{Say: "ok"}}
		}, studio.LoopOptions{})
		chat := create(r.svc, alice)
		sent(r.svc, alice, chat, "promise")
		Expect(settle(r, chat, 1)).To(HaveLen(1))
		for _, t := range r.h.Sent(chat) {
			Expect(t).NotTo(ContainSubstring("System note"))
		}
	})

	for _, words := range []string{
		"Do you want me to check on it again later?",
		"I can check on it later if you like.",
		"The download is complete and the file is moved.",
	} {
		It("stays quiet for: "+words, func() {
			r := newLoopRig(promiseScript(words, "x"), studio.LoopOptions{})
			chat := create(r.svc, alice)
			sent(r.svc, alice, chat, "promise")
			Expect(settle(r, chat, 1)).To(HaveLen(1))
		})
	}

	It("does not guard the final report of a sub-agent", func() {
		r := newLoopRig(func(_ workspace.Actor, text string) []studiotest.ToolStep {
			switch text {
			case "promise":
				return []studiotest.ToolStep{{Tool: "spawn_agent", Args: `{"task":"look it up","wait":true}`}, {Say: "done"}}
			case "look it up":
				return []studiotest.ToolStep{{Say: "I'll check on it in a few minutes."}}
			}
			return []studiotest.ToolStep{{Say: "ok"}}
		}, studio.LoopOptions{})
		chat := create(r.svc, alice)
		sent(r.svc, alice, chat, "promise")
		settle(r, chat, 1)
		kids := childrenOf(r.svc, alice, chat)
		Expect(kids).To(HaveLen(1))
		time.Sleep(100 * time.Millisecond)
		Expect(r.h.Sent(kids[0].ID)).To(HaveLen(1), "the sub-agent got a note")
	})

	It("applies to a chat of the system too", func() {
		r := newLoopRig(promiseScript("I'll check on it in a few minutes.", "ok then"), studio.LoopOptions{})
		chat := create(r.svc, alice)
		Expect(r.svc.SetOriginForTest(ctx, alice, chat, "trigger")).To(Succeed())
		sent(r.svc, alice, chat, "promise")
		got := settle(r, chat, 2)
		Expect(got).To(HaveLen(2))
		Expect(got[1]).To(ContainSubstring("start_loop"))
	})
})
