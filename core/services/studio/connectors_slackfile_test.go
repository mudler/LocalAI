package studio_test

import (
	"context"
	"slices"

	"github.com/mudler/LocalAI/addons/slack/slacktest"
	slack "github.com/mudler/LocalAI/addons/slack/transport"
	"github.com/mudler/LocalAI/core/services/studio"
	"github.com/mudler/LocalAI/core/services/studio/connectors"
	"github.com/mudler/LocalAI/pkg/addons/contract"
	"github.com/mudler/LocalAI/tests/studiobot"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// A file sent in a channel of the allowlist (Slack through the fake). A listed
// person is always trusted; a member who is not listed is trusted only when the
// channel entry has the level "act", and is told why when it does not.
// newUntrustedSlackRig binds Slack like newSlackRigWith but pairs nobody: the
// other specs pair every sender of the rig, which hides the allowlist rules.
func newUntrustedSlackRig(in studio.ConnectorInput) (*slackRig, connectors.Binding) {
	f := slacktest.New()
	DeferCleanup(f.Close)
	f.AddApp(slacktest.App{Bot: slackBot, AppToken: slackApp, UserID: "UBOT"})
	f.AddUser("U1", "ada", "Ada")
	f.AddUser("U2", "bo", "Bo")
	f.AddUser("U3", "cy", "Cy")
	rt := slack.New(&slack.API{BaseURL: f.URL, FilesHost: f.FilesHost})
	DeferCleanup(rt.Close)
	r := newConnRigOpts(newShared(), connStore(), studiobot.NewTransport(studiobot.Slack(rt)), connSay("Got it."), nil, nil)
	r.untrusted = true
	in.Kind, in.Token = "slack", slackBot+" "+slackApp
	in.Caps = contract.UsualConnectorCapabilities() // what the page saves for a new connection
	b := r.bind(in)
	waitFor("the Socket Mode link", func() bool { return f.Connected(slackApp) })
	return &slackRig{connRig: r, slack: f}, b
}

var _ = Describe("connectors slack files in a channel", func() {
	push := func(r *slackRig, user, thread string) {
		r.slack.AddFile("F1", "notes.txt", "text/plain", []byte("the plan"))
		ev := slacktest.Mention("C1", user, "UBOT", "read this", thread)
		ev["files"] = []any{map[string]any{"id": "F1"}}
		r.slack.Push(slackApp, ev)
	}
	texts := func(r *slackRig) []string {
		var out []string
		for _, p := range r.answers() {
			out = append(out, p.Text)
		}
		return out
	}
	const answerOnly = "This channel is set to answer only, so I cannot do that for you here. Ask the owner to list you as a person, or to let everyone in this channel ask for actions."

	for _, allow := range [][]string{{"U1", "C1"}, {"U1"}, {"@ada", "C1"}} {
		It("reads the file of a listed person in a channel, allow = "+allow[0], func() {
			r, _ := newUntrustedSlackRig(studio.ConnectorInput{Allow: allow})
			push(r, "U1", "")
			waitFor("the answer", func() bool { return slices.Contains(texts(r), "Got it.") })
		})
	}

	It("refuses the file of a member of a channel that is set to answer only, and says why", func() {
		r, _ := newUntrustedSlackRig(studio.ConnectorInput{Allow: []string{"U1", "C1"}})
		push(r, "U2", "")
		waitFor("the refusal", func() bool { return slices.Contains(texts(r), answerOnly) })
		chats, _ := r.svc.List(context.Background(), alice, 10)
		Expect(chats).To(BeEmpty(), "a refused file starts no turn")
	})

	It("reads the file of a member of a channel that is set to members act, also in a thread", func() {
		r, _ := newUntrustedSlackRig(studio.ConnectorInput{Allow: []string{"U1", "C1"}, AllowTrust: map[string]string{"C1": "act"}})
		push(r, "U2", "")
		waitFor("the answer", func() bool { return slices.Contains(texts(r), "Got it.") })
		thread := r.answers()[0].Thread
		Expect(thread).NotTo(BeEmpty())
		r.slack.AddFile("F2", "more.txt", "text/plain", []byte("more"))
		ev := slacktest.Reply("C1", "U3", "and this one", thread)
		ev["files"] = []any{map[string]any{"id": "F2"}}
		r.slack.Push(slackApp, ev)
		waitFor("the answer to the thread reply", func() bool {
			n := 0
			for _, t := range texts(r) {
				if t == "Got it." {
					n++
				}
			}
			return n == 2
		})
	})

	It("does not let a channel entry trust a member who writes in private", func() {
		r, _ := newUntrustedSlackRig(studio.ConnectorInput{Allow: []string{"U1", "C1"}, AllowTrust: map[string]string{"C1": "act"}})
		r.slack.Push(slackApp, slacktest.DM("D2", "U2", "hello from outside"))
		Consistently(func() int { return len(r.answers()) }, "400ms", "50ms").Should(Equal(0))
	})
})
