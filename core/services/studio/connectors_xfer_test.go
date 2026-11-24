package studio_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"io"
	"strings"

	"github.com/mudler/LocalAI/addons/slack/slacktest"
	slack "github.com/mudler/LocalAI/addons/slack/transport"
	"github.com/mudler/LocalAI/core/services/addons/hostops"
	"github.com/mudler/LocalAI/core/services/studio"
	"github.com/mudler/LocalAI/core/services/studio/connectors"
	"github.com/mudler/LocalAI/core/services/studio/studiotest"
	"github.com/mudler/LocalAI/core/services/studio/workspace"
	"github.com/mudler/LocalAI/tests/studiobot"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// A file moves by handle, whatever its size, through the real
// Telegram add-on, the real gateway and the real file operations, in both
// directions. The scripted Bot API holds the platform side.

var yes = true

// pdfOf is a PDF header and then n bytes, so the sniffed type is a PDF.
func pdfOf(n int) []byte {
	b := bytes.Repeat([]byte("0123456789abcdef"), n/16+1)[:n]
	copy(b, "%PDF-1.7\n")
	return b
}

var _ = Describe("connector files by handle", func() {
	var (
		sh    *shared
		bot   *scriptedBot
		table *hostops.Transfers
	)
	BeforeEach(func() {
		sh, bot = newShared(), newScriptedBot()
		table = sh.transfers
	})

	rig := func(script func(workspace.Actor, string) []studiotest.ToolStep, in studio.ConnectorInput) *connRig {
		r := newConnRig(sh, connStore(), bot, script, nil)
		in.Allow, in.Reply = []string{"11"}, connectors.ReplyOff
		r.bind(in)
		return r
	}
	document := func(id string, m botMedia) {
		bot.media[id] = m
		bot.message(1, 11, map[string]any{"document": map[string]any{"file_id": id, "file_unique_id": "u" + id}, "caption": "read this"})
	}

	It("reads a 6 MB PDF that the sender uploads, with the bytes outside the envelope", func() {
		pdf := pdfOf(6 << 20)
		r := rig(connSay("Read it."), studio.ConnectorInput{})
		document("d1", botMedia{Path: "documents/file_1.pdf", Data: pdf})
		waitFor("the answer", func() bool { return len(bot.answers()) == 1 })
		Expect(bot.answers()[0]).To(Equal("Read it."))
		c := onlyChat(r)
		f := chatFiles(r.svc, alice, c.ID)["document-ud1.pdf"]
		Expect(f.Size).To(BeEquivalentTo(len(pdf)))
		rd, _, err := sh.files.Store().Open(context.Background(), workspace.Actor{Principal: alice}, f.Path)
		Expect(err).NotTo(HaveOccurred())
		got, err := io.ReadAll(rd)
		Expect(err).NotTo(HaveOccurred())
		_ = rd.Close()
		Expect(sha256.Sum256(got)).To(Equal(sha256.Sum256(pdf)))
		Expect(table.Live()).To(BeZero())
		Expect(r.logs.String()).To(ContainSubstring("connector file audit"))
		Expect(r.logs.String()).NotTo(ContainSubstring("document-ud1"), "the audit holds no name")
	})

	It("tells a sender whose file passes the platform limit the number and the layer", func() {
		rig(connSay("never"), studio.ConnectorInput{})
		document("big", botMedia{Path: "documents/big.pdf", Size: 21 << 20, Data: pdfOf(64)})
		waitFor("the sentence", func() bool { return len(bot.answers()) >= 1 })
		Expect(bot.answers()[0]).To(And(ContainSubstring("too large"), ContainSubstring("20 MiB"), ContainSubstring("limit of Telegram")))
	})

	It("tells a sender whose file passes the limit that the owner set for the bot", func() {
		two := int64(2 << 20)
		rig(connSay("never"), studio.ConnectorInput{FileReceiveMax: &two})
		document("d2", botMedia{Path: "documents/file_2.pdf", Data: pdfOf(6 << 20)})
		waitFor("the sentence", func() bool { return len(bot.answers()) >= 1 })
		Expect(bot.answers()[0]).To(And(ContainSubstring("too large"), ContainSubstring("2 MiB"), ContainSubstring("limit set for this bot")))
		Expect(table.Live()).To(BeZero(), "the partial file is dropped with the call")
	})

	It("sends a 6 MB file that the agent made, pulled by the add-on in chunks", func() {
		big := strings.Repeat("0123456789abcdef", (6<<20)/16)
		rig(func(_ workspace.Actor, text string) []studiotest.ToolStep {
			return []studiotest.ToolStep{write("/out/data.csv", big), attach("/out/data.csv"), {Say: "Done."}}
		}, studio.ConnectorInput{SendFiles: &yes})
		bot.say(1, 11, "Ada", "ada", "make it")
		waitFor("the file", func() bool { return len(bot.sentFiles()) == 1 })
		f := bot.sentFiles()[0]
		Expect(f.Name).To(Equal("data.csv"))
		Expect(f.Method).To(Equal("sendDocument"))
		Expect(string(f.Data)).To(Equal(big))
		Expect(table.Live()).To(BeZero())
	})

	It("keeps a file over the send limit in the chat and says so", func() {
		one := int64(1 << 20)
		big := strings.Repeat("0123456789abcdef", (3<<20)/16)
		rig(func(_ workspace.Actor, text string) []studiotest.ToolStep {
			return []studiotest.ToolStep{write("/out/data.csv", big), {Say: "Done."}}
		}, studio.ConnectorInput{SendFiles: &yes, FileSendMax: &one})
		bot.say(1, 11, "Ada", "ada", "make it")
		waitFor("the answer and the note", func() bool { return len(bot.answers()) >= 2 })
		Expect(bot.sentFiles()).To(BeEmpty())
		Expect(strings.Join(bot.answers(), "\n")).To(And(ContainSubstring("data.csv is too large to send here"), ContainSubstring("limit set for this bot")))
	})

	Describe("with Slack", func() {
		var (
			f *slacktest.Server
			r *connRig
		)
		slackRig := func(script func(workspace.Actor, string) []studiotest.ToolStep, in studio.ConnectorInput) {
			f = slacktest.New()
			DeferCleanup(f.Close)
			f.AddApp(slacktest.App{Bot: slackBot, AppToken: slackApp, UserID: "UBOT"})
			f.AddUser("U1", "ada", "Ada")
			rt := slack.New(&slack.API{BaseURL: f.URL, FilesHost: f.FilesHost})
			DeferCleanup(rt.Close)
			r = newConnRigWith(sh, connStore(), studiobot.NewTransport(studiobot.Slack(rt)), script, nil)
			in.Kind, in.Token, in.Allow = "slack", slackBot+" "+slackApp, []string{"U1"}
			r.bind(in)
			waitFor("the Socket Mode link", func() bool { return f.Connected(slackApp) })
		}

		It("reads a 6 MB PDF that a person shares", func() {
			pdf := pdfOf(6 << 20)
			slackRig(connSay("Read it."), studio.ConnectorInput{})
			f.AddFile("F1", "plan.pdf", "application/pdf", pdf)
			ev := slacktest.DM("D1", "U1", "read this")
			ev["files"] = []any{map[string]any{"id": "F1"}}
			f.Push(slackApp, ev)
			waitFor("the chat", func() bool { c, _ := r.svc.List(context.Background(), alice, 10); return len(c) == 1 })
			c := onlyChat(r)
			var up studio.ChatFile
			waitFor("the upload", func() bool { up = chatFiles(r.svc, alice, c.ID)["plan.pdf"]; return up.Size > 0 })
			Expect(up.Size).To(BeEquivalentTo(len(pdf)))
			Expect(table.Live()).To(BeZero())
		})

		It("uploads a 6 MB file that the agent made, streamed with its length", func() {
			big := strings.Repeat("0123456789abcdef", (6<<20)/16)
			slackRig(func(workspace.Actor, string) []studiotest.ToolStep {
				return []studiotest.ToolStep{write("/out/data.csv", big), {Say: "Made."}}
			}, studio.ConnectorInput{SendFiles: &yes})
			f.Push(slackApp, slacktest.DM("D1", "U1", "make"))
			waitFor("the upload", func() bool { return len(f.Uploads(slackBot)) == 1 })
			up := f.Uploads(slackBot)[0]
			Expect(up.Name).To(Equal("data.csv"))
			Expect(up.Length).To(Equal(len(big)))
			Expect(string(up.Data)).To(Equal(big))
			Expect(up.Complete).To(BeTrue())
		})
	})
})
