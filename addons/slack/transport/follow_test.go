// SPDX-License-Identifier: MIT
package transport_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"time"

	"github.com/mudler/LocalAI/addons/slack/slacktest"
	"github.com/mudler/LocalAI/addons/slack/transport"
	"github.com/mudler/LocalAI/pkg/addons/contract"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("which threads the bot follows", func() {
	ctx := context.Background()
	const channel = "C1"

	// bot is a runtime over f that keeps its threads in dir, and a function
	// that polls it with the follow mode and returns what it hands over.
	type bot struct {
		rt   *transport.Runtime
		poll func(n int) []contract.ConnectorMessage
	}
	start := func(f *slacktest.Server, dir, mode string) bot {
		rt := transport.New(apiOf(f))
		rt.Socket.IdleAfter = time.Minute
		rt.Socket.StateDir = dir
		DeferCleanup(rt.Close)
		var offset int64 = 1
		poll := func(n int) []contract.ConnectorMessage {
			var got []contract.ConnectorMessage
			deadline := time.Now().Add(3 * time.Second)
			if n < 0 {
				deadline = time.Now().Add(700 * time.Millisecond) // wait for nothing
			}
			for first := true; first || (time.Now().Before(deadline) && len(got) < n); first = false {
				out, err := rt.Connector(ctx, contract.ConnectorRequest{Op: "poll", Kind: "slack", Token: secret, Offset: offset, TimeoutSeconds: 0,
					Options: map[string]string{"followThreads": mode}})
				Expect(err).NotTo(HaveOccurred())
				Expect(out.Error).To(BeNil())
				for _, u := range out.Updates {
					got = append(got, u)
					offset = max(offset, u.Cursor+1)
				}
			}
			return got
		}
		poll(0)
		Eventually(func() bool { return f.Connected(appToken) }, 5*time.Second).Should(BeTrue())
		return bot{rt: rt, poll: poll}
	}
	// silent polls for a while and expects nothing.
	silent := func(b bot) {
		Expect(b.poll(-1)).To(BeEmpty())
	}

	It("own: follows the thread its top-level mention started", func() {
		f := fake()
		b := start(f, GinkgoT().TempDir(), "own")
		mention := slacktest.Mention(channel, "U1", "UBOT", "help", "")
		f.Push(appToken, mention)
		Expect(b.poll(1)).To(HaveLen(1))
		f.Push(appToken, slacktest.Reply(channel, "U1", "thanks, one more thing", mention["ts"].(string)))
		got := b.poll(1)
		Expect(got).To(HaveLen(1))
		Expect([]any{got[0].Mention, got[0].Thread}).To(Equal([]any{true, mention["ts"]}))
	})

	It("own: answers a mention inside a thread that other people started, and does not follow it", func() {
		f := fake()
		b := start(f, GinkgoT().TempDir(), "own")
		f.Push(appToken, slacktest.Mention(channel, "U1", "UBOT", "what do you think?", "100.000001"))
		got := b.poll(1)
		Expect(got).To(HaveLen(1))
		Expect([]any{got[0].Mention, got[0].Thread}).To(Equal([]any{true, "100.000001"}))
		f.Push(appToken, slacktest.Reply(channel, "U2", "people talking to each other", "100.000001"))
		silent(b)
	})

	It("any: follows a thread once the bot is mentioned in it", func() {
		f := fake()
		b := start(f, GinkgoT().TempDir(), "any")
		f.Push(appToken, slacktest.Mention(channel, "U1", "UBOT", "hi", "100.000001"))
		Expect(b.poll(1)).To(HaveLen(1))
		f.Push(appToken, slacktest.Reply(channel, "U2", "and me", "100.000001"))
		Expect(b.poll(1)).To(HaveLen(1))
	})

	It("never: every message needs a mention", func() {
		f := fake()
		b := start(f, GinkgoT().TempDir(), "never")
		mention := slacktest.Mention(channel, "U1", "UBOT", "hi", "")
		f.Push(appToken, mention)
		Expect(b.poll(1)).To(HaveLen(1))
		f.Push(appToken, slacktest.Reply(channel, "U1", "no mention", mention["ts"].(string)))
		silent(b)
	})

	It("takes an unknown or missing mode as own", func() {
		f := fake()
		b := start(f, GinkgoT().TempDir(), "sometimes")
		f.Push(appToken, slacktest.Mention(channel, "U1", "UBOT", "hi", "100.000001"))
		Expect(b.poll(1)).To(HaveLen(1))
		f.Push(appToken, slacktest.Reply(channel, "U2", "x", "100.000001"))
		silent(b)
	})

	It("direct messages are not affected", func() {
		f := fake()
		b := start(f, GinkgoT().TempDir(), "never")
		f.Push(appToken, slacktest.DM("D1", "U1", "hello"))
		Expect(b.poll(1)).To(HaveLen(1))
	})

	It("keeps the followed threads across a restart", func() {
		f := fake()
		dir := GinkgoT().TempDir()
		b := start(f, dir, "own")
		mention := slacktest.Mention(channel, "U1", "UBOT", "help", "")
		f.Push(appToken, mention)
		Expect(b.poll(1)).To(HaveLen(1))
		path := transport.StateFileOf(dir, transport.ParseTokens(secret))
		Expect(path).To(HavePrefix(dir))
		raw, err := os.ReadFile(path)
		Expect(err).NotTo(HaveOccurred())
		Expect(string(raw)).NotTo(ContainSubstring("SECRET"), "no token in the file")
		info, _ := os.Stat(path)
		Expect(info.Mode().Perm()).To(Equal(os.FileMode(0o600)))
		b.rt.Close() // the add-on stops
		// The fake lists the old link until its handler sees the close. A
		// reply pushed before that goes to the closing socket and is lost, and
		// start would also take the old link for the new one.
		Eventually(func() bool { return f.Connected(appToken) }, 5*time.Second).Should(BeFalse())

		b2 := start(f, dir, "own")
		f.Push(appToken, slacktest.Reply(channel, "U1", "after the restart", mention["ts"].(string)))
		got := b2.poll(1)
		Expect(got).To(HaveLen(1))
		Expect(got[0].Mention).To(BeTrue())
		// A thread nobody started is still ignored.
		f.Push(appToken, slacktest.Reply(channel, "U1", "elsewhere", "100.000009"))
		silent(b2)
	})

	It("forgets a thread after its life, also when the file is old, and ignores a damaged file", func() {
		f := fake()
		dir := GinkgoT().TempDir()
		path := transport.StateFileOf(dir, transport.ParseTokens(secret))
		old := time.Now().Add(-transport.ThreadLife - time.Hour).Unix()
		fresh := time.Now().Add(-time.Hour).Unix()
		raw, _ := json.Marshal(map[string]any{"threads": []map[string]any{
			{"channel": channel, "thread": "100.000001", "at": old},
			{"channel": channel, "thread": "100.000002", "at": fresh},
		}})
		Expect(os.WriteFile(path, raw, 0o600)).To(Succeed())
		b := start(f, dir, "own")
		f.Push(appToken, slacktest.Reply(channel, "U1", "to the old thread", "100.000001"))
		silent(b)
		f.Push(appToken, slacktest.Reply(channel, "U1", "to the fresh thread", "100.000002"))
		Expect(b.poll(1)).To(HaveLen(1))

		bad := fake()
		dir2 := GinkgoT().TempDir()
		Expect(os.WriteFile(transport.StateFileOf(dir2, transport.ParseTokens(secret)), []byte("{not json"), 0o600)).To(Succeed())
		b2 := start(bad, dir2, "own")
		bad.Push(appToken, slacktest.Reply(channel, "U1", "x", "100.000002"))
		silent(b2)
	})

	It("works with no state directory, and leaves no other file in it", func() {
		f := fake()
		b := start(f, "", "own")
		mention := slacktest.Mention(channel, "U1", "UBOT", "help", "")
		f.Push(appToken, mention)
		Expect(b.poll(1)).To(HaveLen(1))
		f.Push(appToken, slacktest.Reply(channel, "U1", "again", mention["ts"].(string)))
		Expect(b.poll(1)).To(HaveLen(1))

		dir := GinkgoT().TempDir()
		f2 := fake()
		b2 := start(f2, dir, "own")
		f2.Push(appToken, slacktest.Mention(channel, "U1", "UBOT", "help", ""))
		Expect(b2.poll(1)).To(HaveLen(1))
		entries, _ := os.ReadDir(dir)
		Expect(entries).To(HaveLen(1), "the temporary file is renamed away")
		Expect(filepath.Ext(entries[0].Name())).To(Equal(".json"))
	})
})
