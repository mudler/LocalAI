package studio_test

import (
	"errors"
	"net/http"
	"slices"
	"strings"
	"sync/atomic"
	"time"

	"github.com/mudler/LocalAI/core/services/studio"
	"github.com/mudler/LocalAI/core/services/studio/connectors"
	"github.com/mudler/LocalAI/core/services/studio/studiotest"
	"github.com/mudler/LocalAI/core/services/studio/workspace"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// One visible message per answer: a preview that an edit cannot update stays
// pending for the answer to replace or delete, and the reasoning of the model
// is never a preview.

// settled waits until the bot has sent n messages and the turn is over (no
// message is deleted or sent after a short quiet period), then returns.
func (b *scriptedBot) quiet() {
	time.Sleep(250 * time.Millisecond)
}

// shownTo is what the sender reads in the chat: the sent messages with their
// latest edit, without those that were deleted.
func (b *scriptedBot) shownTo() []string {
	ids := func() []int64 { b.mu.Lock(); defer b.mu.Unlock(); return slices.Clone(b.sentIDs) }()
	texts := b.answers()
	b.mu.Lock()
	deleted := slices.Clone(b.deleted)
	b.mu.Unlock()
	var out []string
	for i, id := range ids {
		if slices.Contains(deleted, id) {
			continue
		}
		t := texts[i]
		if e := b.lastEdit(id); e != "" {
			t = e
		}
		out = append(out, t)
	}
	return out
}

func (b *scriptedBot) deletes() []int64 {
	b.mu.Lock()
	defer b.mu.Unlock()
	return slices.Clone(b.deleted)
}

var _ = Describe("connectors: one message per answer", func() {
	editGone := func() (int, string) {
		return http.StatusBadRequest, `{"ok":false,"error_code":400,"description":"Bad Request: message to edit not found"}`
	}
	// answerAfter streams a half answer, waits for release, then finishes it.
	answerAfter := func(release <-chan struct{}) func(workspace.Actor, string) []studiotest.ToolStep {
		return func(workspace.Actor, string) []studiotest.ToolStep {
			return []studiotest.ToolStep{{Say: "I'm an automated triage bot for the"}, {Block: release}, {Say: " team."}}
		}
	}

	It("deletes the preview when no edit can update it, and shows the answer once", func() {
		sh, bot := newShared(), newScriptedBot()
		bot.setFail("editMessageText", editGone)
		release := make(chan struct{})
		r := newConnRig(sh, connStore(), bot, answerAfter(release), nil)
		r.bind(studio.ConnectorInput{Allow: []string{"-600"}, Reply: connectors.ReplyStream})
		bot.mention(1, -600, 21, "Ann", "who are you")
		waitFor("the preview", func() bool { return len(bot.answers()) == 1 })
		time.Sleep(300 * time.Millisecond)
		close(release)
		waitFor("the answer", func() bool { return len(bot.answers()) == 2 })
		bot.quiet()
		Expect(bot.shownTo()).To(Equal([]string{"I'm an automated triage bot for the team."}), "one message, no cursor")
		Expect(bot.deletes()).To(Equal([]int64{bot.firstID()}))
	})

	It("keeps editing the preview after a failed edit, and the answer replaces it", func() {
		sh, bot := newShared(), newScriptedBot()
		var calls atomic.Int32
		bot.setFail("editMessageText", func() (int, string) {
			if calls.Add(1) <= 2 {
				return http.StatusInternalServerError, `{"ok":false,"error_code":500,"description":"Internal Server Error"}`
			}
			return 0, ""
		})
		release := make(chan struct{})
		r := newConnRig(sh, connStore(), bot, answerAfter(release), nil)
		r.bind(studio.ConnectorInput{Allow: []string{"-600"}, Reply: connectors.ReplyStream})
		bot.mention(1, -600, 21, "Ann", "who are you")
		waitFor("the preview", func() bool { return len(bot.answers()) == 1 })
		time.Sleep(300 * time.Millisecond)
		close(release)
		bot.quiet()
		waitFor("the answer in the preview", func() bool { return strings.HasSuffix(bot.lastEdit(bot.firstID()), "team.") })
		Expect(bot.shownTo()).To(Equal([]string{"I'm an automated triage bot for the team."}))
		Expect(bot.deletes()).To(BeEmpty())
	})

	It("waits for a rate limit and then replaces the preview", func() {
		sh, bot := newShared(), newScriptedBot()
		var calls atomic.Int32
		bot.setFail("editMessageText", func() (int, string) {
			if calls.Add(1) <= 2 {
				return http.StatusTooManyRequests, `{"ok":false,"error_code":429,"description":"Too Many Requests: retry after 1","parameters":{"retry_after":1}}`
			}
			return 0, ""
		})
		release := make(chan struct{})
		r := newConnRig(sh, connStore(), bot, answerAfter(release), nil)
		r.bind(studio.ConnectorInput{Allow: []string{"-600"}, Reply: connectors.ReplyStream})
		bot.mention(1, -600, 21, "Ann", "who are you")
		waitFor("the preview", func() bool { return len(bot.answers()) == 1 })
		time.Sleep(200 * time.Millisecond)
		close(release)
		waitFor("the answer in the preview", func() bool { return strings.HasSuffix(bot.lastEdit(bot.firstID()), "team.") })
		bot.quiet()
		Expect(bot.shownTo()).To(Equal([]string{"I'm an automated triage bot for the team."}))
		Expect(bot.deletes()).To(BeEmpty())
	})

	It("never shows the reasoning of the model in a working message", func() {
		sh, bot := newShared(), newScriptedBot()
		thought, release := make(chan struct{}), make(chan struct{})
		script := func(workspace.Actor, string) []studiotest.ToolStep {
			return []studiotest.ToolStep{{Think: "The user is asking about a duplicate "}, {Think: "so my last answer"}, {Block: thought},
				{Say: "Sorry about that."}, {Block: release}, {Say: " Fixed."}}
		}
		r := newConnRig(sh, connStore(), bot, script, nil)
		r.bind(studio.ConnectorInput{Allow: []string{"-600"}, Reply: connectors.ReplyStream})
		bot.mention(1, -600, 21, "Ann", "duplicated answer?")
		waitFor("the working message", func() bool { return len(bot.answers()) == 1 })
		time.Sleep(300 * time.Millisecond)
		id := bot.firstID()
		Expect(bot.answers()[0]).NotTo(ContainSubstring("user is asking"))
		for _, e := range bot.editsOf(id) {
			Expect(e.Text).NotTo(ContainSubstring("user is asking"))
			Expect(e.Text).NotTo(ContainSubstring("my last answer"))
		}
		close(thought)
		waitFor("the answer so far", func() bool { return bot.lastEdit(id) == "Sorry about that. ▉" })
		close(release)
		waitFor("the whole answer", func() bool { return bot.lastEdit(id) == "Sorry about that. Fixed." })
		for _, e := range bot.editsOf(id) {
			Expect(e.Text).NotTo(ContainSubstring("user is asking"))
		}
		Expect(bot.shownTo()).To(Equal([]string{"Sorry about that. Fixed."}))
	})

	It("keeps the words before a tool call out of the answer", func() {
		sh, bot := newShared(), newScriptedBot()
		release := make(chan struct{})
		script := func(workspace.Actor, string) []studiotest.ToolStep {
			return []studiotest.ToolStep{{Say: "Let me look. "}, {Tool: "write_file", Args: `{"path":"/notes/a.md","content":"x"}`}, {Block: release}, {Say: "Here it is."}}
		}
		r := newConnRig(sh, connStore(), bot, script, nil)
		r.bind(studio.ConnectorInput{Allow: []string{"-600"}, Reply: connectors.ReplyStream, SendFiles: &filesOff})
		bot.mention(1, -600, 21, "Ann", "look")
		waitFor("the working message", func() bool { return len(bot.answers()) == 1 })
		close(release)
		waitFor("the answer", func() bool { return strings.HasPrefix(bot.lastEdit(bot.firstID()), "Here it is") })
		bot.quiet()
		Expect(bot.shownTo()).To(HaveLen(1))
		Expect(bot.deletes()).To(BeEmpty())
	})

	It("replaces the preview with a failure line when the turn fails", func() {
		sh, bot := newShared(), newScriptedBot()
		release := make(chan struct{})
		script := func(workspace.Actor, string) []studiotest.ToolStep {
			return []studiotest.ToolStep{{Say: "Half of it"}, {Block: release}, {Err: errors.New("the model went away")}}
		}
		r := newConnRig(sh, connStore(), bot, script, nil)
		r.bind(studio.ConnectorInput{Allow: []string{"-600"}, Reply: connectors.ReplyStream})
		bot.mention(1, -600, 21, "Ann", "go")
		waitFor("the working message", func() bool { return len(bot.answers()) == 1 })
		close(release)
		waitFor("a line that is not the preview", func() bool {
			e := bot.lastEdit(bot.firstID())
			return e != "" && !strings.HasSuffix(e, "▉")
		})
		bot.quiet()
		Expect(bot.shownTo()).To(HaveLen(1))
		Expect(bot.shownTo()[0]).NotTo(ContainSubstring("▉"))
	})

	It("removes the preview when the answer cannot be sent at all", func() {
		sh, bot := newShared(), newScriptedBot()
		release := make(chan struct{})
		r := newConnRig(sh, connStore(), bot, answerAfter(release), nil)
		r.bind(studio.ConnectorInput{Allow: []string{"-600"}, Reply: connectors.ReplyStream})
		bot.mention(1, -600, 21, "Ann", "who are you")
		waitFor("the preview", func() bool { return len(bot.answers()) == 1 })
		// From now on every edit and every send fails, except the failure line.
		var edits atomic.Int32
		bot.setFail("sendMessage", func() (int, string) {
			return http.StatusBadRequest, `{"ok":false,"error_code":400,"description":"Bad Request: chat not found"}`
		})
		bot.setFail("editMessageText", func() (int, string) {
			edits.Add(1)
			return http.StatusBadRequest, `{"ok":false,"error_code":400,"description":"Bad Request: message to edit not found"}`
		})
		close(release)
		waitFor("the preview is deleted", func() bool { return len(bot.deletes()) == 1 })
		Expect(bot.deletes()[0]).To(Equal(bot.firstID()))
	})

	It("shows two quick turns of one chat as two answers, each in one message", func() {
		sh, bot := newShared(), newScriptedBot()
		release := make(chan struct{})
		var turn atomic.Int32
		script := func(workspace.Actor, string) []studiotest.ToolStep {
			if turn.Add(1) == 1 {
				return []studiotest.ToolStep{{Say: "First, "}, {Block: release}, {Say: "done."}}
			}
			return []studiotest.ToolStep{{Say: "Second."}}
		}
		r := newConnRig(sh, connStore(), bot, script, nil)
		r.bind(studio.ConnectorInput{Allow: []string{"11"}, Reply: connectors.ReplyProgress, SendFiles: &filesOff})
		bot.say(1, 11, "Ada", "ada", "one")
		waitFor("the first working message", func() bool { return len(bot.answers()) == 1 })
		bot.say(2, 11, "Ada", "ada", "two")
		time.Sleep(200 * time.Millisecond)
		close(release)
		DeferCleanup(func() {
			GinkgoWriter.Printf("shown %q answers %q deletes %v edits %+v\n", bot.shownTo(), bot.answers(), bot.deletes(), bot.editsOf(bot.firstID()))
		})
		waitFor("both answers", func() bool {
			s := bot.shownTo()
			return len(s) == 2 && s[0] == "First, done." && s[1] == "Second."
		})
		bot.quiet()
		Expect(bot.shownTo()).To(Equal([]string{"First, done.", "Second."}))
		Expect(bot.deletes()).To(BeEmpty())
	})
})
