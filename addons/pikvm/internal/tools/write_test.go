// SPDX-License-Identifier: MIT
package tools_test

import (
	"context"
	"encoding/json"
	"strings"

	"github.com/mudler/LocalAI/addons/pikvm/internal/tools"
	"github.com/mudler/LocalAI/addons/pikvm/pikvmtest"
	"github.com/mudler/LocalAI/pkg/addons/contract"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var ctx = context.Background()

// preview and apply run the two steps the host runs for a confirmed change.
func (f *fixture) preview(op, tool string, args map[string]any) (contract.Preview, error) {
	return f.rt.Preview(ctx, f.env(op, tool, args))
}

func (f *fixture) apply(op, tool string, args map[string]any, p contract.Preview) (contract.Receipt, error) {
	return f.rt.Apply(ctx, f.env(op, tool, args), p.Revision)
}

func sampleOf(p contract.Preview) map[string]any {
	var s struct {
		Sample map[string]any `json:"sample"`
	}
	Expect(json.Unmarshal(p.Summary, &s)).To(Succeed())
	return s.Sample
}

var _ = Describe("power-action", func() {
	var (
		srv *pikvmtest.Server
		f   *fixture
	)
	BeforeEach(func() {
		srv = pikvmtest.New(devUser, devPass)
		f = newFixture(srv, GinkgoT().TempDir())
	})
	AfterEach(func() { srv.Close() })

	It("previews the current power state and what will happen, and sends nothing", func() {
		p, err := f.preview("op1", tools.ToolPowerAction, map[string]any{"command": "off"})
		Expect(err).NotTo(HaveOccurred())
		s := sampleOf(p)
		Expect(s).To(HaveKeyWithValue("powerNow", "on"))
		Expect(s).To(HaveKeyWithValue("action", "off"))
		Expect(s["effect"]).To(ContainSubstring("power button"))
		Expect(srv.Writes()).To(Equal(0))
		noLeak(p.Summary)
	})

	It("applies a soft off with a short press of the power action, once", func() {
		args := map[string]any{"command": "off"}
		p, _ := f.preview("op1", tools.ToolPowerAction, args)
		rc, err := f.apply("op1", tools.ToolPowerAction, args, p)
		Expect(err).NotTo(HaveOccurred())
		Expect(rc.State).To(Equal(contract.ReceiptCommitted))
		Expect(srv.Actions()).To(Equal([]string{"off"}))
		Expect(srv.Power()).To(BeFalse())
		noLeak(rc.Result.Payload)
		// The same operation again answers from the journal.
		rc, err = f.apply("op1", tools.ToolPowerAction, args, p)
		Expect(err).NotTo(HaveOccurred())
		Expect(rc.State).To(Equal(contract.ReceiptCommitted))
		Expect(srv.Writes()).To(Equal(1))
	})

	It("starts a machine that is off, and resets a machine that is on", func() {
		srv.SetPower(false)
		args := map[string]any{"command": "on"}
		p, _ := f.preview("op1", tools.ToolPowerAction, args)
		_, err := f.apply("op1", tools.ToolPowerAction, args, p)
		Expect(err).NotTo(HaveOccurred())
		Expect(srv.Actions()).To(Equal([]string{"on"}))
		args = map[string]any{"command": "reset"}
		p, _ = f.preview("op2", tools.ToolPowerAction, args)
		_, err = f.apply("op2", tools.ToolPowerAction, args, p)
		Expect(err).NotTo(HaveOccurred())
		Expect(srv.Clicks()).To(Equal([]string{"reset"}))
	})

	It("refuses a no-op: on when it is on, off when it is off, a reset when it is off", func() {
		_, err := f.preview("a", tools.ToolPowerAction, map[string]any{"command": "on"})
		Expect(err).To(MatchError(ContainSubstring("already on")))
		srv.SetPower(false)
		_, err = f.preview("b", tools.ToolPowerAction, map[string]any{"command": "off"})
		Expect(err).To(MatchError(ContainSubstring("already off")))
		_, err = f.preview("c", tools.ToolPowerAction, map[string]any{"command": "reset"})
		Expect(err).To(MatchError(ContainSubstring("reset does nothing")))
	})

	It("rejects the operation as stale when the power state changed after the preview", func() {
		f.allow(true, false)
		args := map[string]any{"command": "long_press"}
		p, _ := f.preview("op1", tools.ToolPowerAction, args)
		srv.SetPower(false)
		rc, err := f.apply("op1", tools.ToolPowerAction, args, p)
		Expect(err).NotTo(HaveOccurred())
		Expect(rc.State).To(Equal(contract.ReceiptRejected))
		Expect(string(rc.Result.Payload)).To(ContainSubstring("stale"))
		Expect(srv.Writes()).To(Equal(0))
	})

	It("refuses at apply a soft action that the new power state makes pointless", func() {
		args := map[string]any{"command": "off"}
		p, _ := f.preview("op1", tools.ToolPowerAction, args)
		srv.SetPower(false)
		_, err := f.apply("op1", tools.ToolPowerAction, args, p)
		Expect(err).To(MatchError(ContainSubstring("already off")))
		Expect(srv.Writes()).To(Equal(0))
	})

	DescribeTable("refuses a hard action while the setting is off, and calls the device for nothing",
		func(action string) {
			_, err := f.preview("op1", tools.ToolPowerAction, map[string]any{"command": action})
			Expect(err).To(MatchError(ContainSubstring("hard power action")))
			Expect(srv.Calls()).To(BeEmpty())
			// Apply refuses it too, even with a revision from elsewhere.
			_, err = f.rt.Apply(ctx, f.env("op1", tools.ToolPowerAction, map[string]any{"command": action}), contract.Revision{Schema: 1, Data: 7})
			Expect(err).To(HaveOccurred())
			Expect(srv.Writes()).To(Equal(0))
		},
		Entry("off_hard", "off_hard"), Entry("reset_hard", "reset_hard"), Entry("long_press", "long_press"))

	It("sends the hard actions once the setting is on", func() {
		f.allow(true, false)
		for i, c := range []struct{ action, kind string }{{"reset_hard", "power"}, {"long_press", "click"}, {"off_hard", "power"}} {
			srv.SetPower(true)
			args := map[string]any{"command": c.action}
			op := "op" + string(rune('a'+i))
			p, err := f.preview(op, tools.ToolPowerAction, args)
			Expect(err).NotTo(HaveOccurred())
			Expect(sampleOf(p)).To(HaveKeyWithValue("hard", true))
			_, err = f.apply(op, tools.ToolPowerAction, args, p)
			Expect(err).NotTo(HaveOccurred())
		}
		Expect(srv.Actions()).To(Equal([]string{"reset_hard", "off_hard"}))
		Expect(srv.Clicks()).To(Equal([]string{"power_long"}))
	})

	It("refuses an unknown action, a disabled ATX and a busy device", func() {
		_, err := f.preview("a", tools.ToolPowerAction, map[string]any{"command": "format"})
		Expect(err).To(HaveOccurred())
		srv.SetATX(false, false)
		_, err = f.preview("b", tools.ToolPowerAction, map[string]any{"command": "off"})
		Expect(err).To(MatchError(ContainSubstring("not enabled")))
		srv.SetATX(true, true)
		_, err = f.preview("c", tools.ToolPowerAction, map[string]any{"command": "off"})
		Expect(err).To(MatchError(ContainSubstring("busy")))
	})

	It("leaves the outcome unknown when the connection drops, and never sends it again", func() {
		srv.DropWrites()
		args := map[string]any{"command": "off"}
		p, _ := f.preview("op1", tools.ToolPowerAction, args)
		_, err := f.apply("op1", tools.ToolPowerAction, args, p)
		Expect(err).To(HaveOccurred())
		rc, err := f.rt.Receipt(ctx, "op1")
		Expect(err).NotTo(HaveOccurred())
		Expect(rc.State).To(Equal(contract.ReceiptUnknown))
		// A retry of the same operation does not reach the device again.
		before := len(srv.Calls())
		rc, err = f.apply("op1", tools.ToolPowerAction, args, p)
		Expect(err).NotTo(HaveOccurred())
		Expect(rc.State).To(Equal(contract.ReceiptUnknown))
		Expect(srv.Calls()).To(HaveLen(before))
		Expect(srv.Actions()).To(HaveLen(1))
	})

	It("keeps an unknown outcome across a restart", func() {
		dir := GinkgoT().TempDir()
		f = newFixture(srv, dir)
		srv.DropWrites()
		args := map[string]any{"command": "off"}
		p, _ := f.preview("op1", tools.ToolPowerAction, args)
		_, _ = f.apply("op1", tools.ToolPowerAction, args, p)
		f2 := newFixture(srv, dir)
		rc, _ := f2.apply("op1", tools.ToolPowerAction, args, p)
		Expect(rc.State).To(Equal(contract.ReceiptUnknown))
		Expect(srv.Actions()).To(HaveLen(1))
	})

	It("tells a definite refusal of the device from an unknown outcome", func() {
		args := map[string]any{"command": "off"}
		p, _ := f.preview("op1", tools.ToolPowerAction, args)
		srv.SetATX(true, true)
		// busy now: the re-check refuses before anything is sent.
		_, err := f.apply("op1", tools.ToolPowerAction, args, p)
		Expect(err).To(HaveOccurred())
		Expect(srv.Writes()).To(Equal(0))
		rc, _ := f.rt.Receipt(ctx, "op1")
		Expect(rc.State).To(Equal(contract.ReceiptUnknown), "nothing was started, and the add-on has no receipt")
	})

	It("declares approval always for every write tool in its manifest", func() {
		Expect(tools.WriteTools()).To(ConsistOf("power-action", "type-text", "send-key"))
	})
})

var _ = Describe("type-text", func() {
	var (
		srv *pikvmtest.Server
		f   *fixture
	)
	BeforeEach(func() {
		srv = pikvmtest.New(devUser, devPass)
		f = newFixture(srv, GinkgoT().TempDir())
	})
	AfterEach(func() { srv.Close() })

	It("is refused while typing is off, before the device is called", func() {
		_, err := f.preview("op1", tools.ToolTypeText, map[string]any{"text": "ls -l"})
		Expect(err).To(MatchError(ContainSubstring("keyboard input is off")))
		Expect(srv.Calls()).To(BeEmpty())
	})

	Context("with typing on", func() {
		BeforeEach(func() { f.allow(false, true) })

		It("previews the exact text and types it once through the print endpoint", func() {
			text := "echo hello\nuname -a\t# done"
			args := map[string]any{"text": text}
			p, err := f.preview("op1", tools.ToolTypeText, args)
			Expect(err).NotTo(HaveOccurred())
			s := sampleOf(p)
			Expect(s).To(HaveKeyWithValue("text", text))
			Expect(s).To(HaveKeyWithValue("lines", float64(2)))
			rc, err := f.apply("op1", tools.ToolTypeText, args, p)
			Expect(err).NotTo(HaveOccurred())
			Expect(rc.State).To(Equal(contract.ReceiptCommitted))
			Expect(srv.Typed()).To(Equal([]string{text}))
			// The receipt does not echo the text.
			Expect(string(rc.Result.Payload)).NotTo(ContainSubstring("echo hello"))
			Expect(string(rc.Result.Payload)).To(ContainSubstring(`"chars":26`))
		})

		It("keeps the limit: 200 characters by default, and the ceiling of 1000 for a setting", func() {
			_, err := f.preview("a", tools.ToolTypeText, map[string]any{"text": strings.Repeat("ab ", 67)})
			Expect(err).To(MatchError(ContainSubstring("at most 200")))
			_, err = f.preview("b", tools.ToolTypeText, map[string]any{"text": strings.Repeat("ab ", 66)})
			Expect(err).NotTo(HaveOccurred())
			Expect(f.rt.Settings).NotTo(BeNil())
			res, err := f.rt.Read(ctx, f.env("c", tools.ToolSetSettings, map[string]any{"maxTypeChars": 1001, "device": f.dev}))
			Expect(err).To(HaveOccurred(), "%v", res)
			_, err = f.rt.Read(ctx, f.env("d", tools.ToolSetSettings, map[string]any{"maxTypeChars": 1000}))
			Expect(err).NotTo(HaveOccurred())
			_, err = f.preview("e", tools.ToolTypeText, map[string]any{"text": strings.Repeat("ab ", 333)})
			Expect(err).NotTo(HaveOccurred())
			_, err = f.preview("f", tools.ToolTypeText, map[string]any{"text": strings.Repeat("ab ", 334)})
			Expect(err).To(HaveOccurred())
		})

		DescribeTable("refuses what it must not type, without echoing the text",
			func(text, why string) {
				_, err := f.preview("op1", tools.ToolTypeText, map[string]any{"text": text})
				Expect(err).To(HaveOccurred())
				Expect(err.Error()).To(ContainSubstring(why))
				Expect(err.Error()).NotTo(ContainSubstring("hunter2"))
				Expect(err.Error()).NotTo(ContainSubstring("BEGIN"))
				Expect(srv.Writes()).To(Equal(0))
			},
			Entry("an escape character", "ls\x1b[31m hunter2", "control character"),
			Entry("a carriage return", "a\rb hunter2", "control character"),
			Entry("a NUL", "a\x00b", "control character"),
			Entry("an empty text", "", "empty"),
			Entry("a private key header", "-----BEGIN OPENSSH PRIVATE KEY-----\nb3Blbn", "private key"),
			Entry("a very long base64 blob", strings.Repeat("QUJD", 30), "private key, a token"),
			Entry("a cloud access key", "export K=AKIAABCDEFGHIJKLMNOP", "private key, a token"),
			Entry("a password list", "username: bob\npassword: hunter2\nurl: x\n", "list of passwords"),
		)

		It("refuses when the keyboard is not connected to the target", func() {
			srv.SetKeyboard(false)
			_, err := f.preview("op1", tools.ToolTypeText, map[string]any{"text": "ls"})
			Expect(err).To(MatchError(ContainSubstring("not connected")))
		})

		It("rejects the operation as stale when the text of the stored call is not the previewed one", func() {
			p, _ := f.preview("op1", tools.ToolTypeText, map[string]any{"text": "ls"})
			rc, err := f.apply("op1", tools.ToolTypeText, map[string]any{"text": "rm -rf /"}, p)
			Expect(err).NotTo(HaveOccurred())
			Expect(rc.State).To(Equal(contract.ReceiptRejected))
			Expect(srv.Typed()).To(BeEmpty())
		})

		It("refuses the apply when typing was switched off after the preview", func() {
			args := map[string]any{"text": "ls"}
			p, _ := f.preview("op1", tools.ToolTypeText, args)
			f.allow(false, false)
			_, err := f.apply("op1", tools.ToolTypeText, args, p)
			Expect(err).To(HaveOccurred())
			Expect(srv.Typed()).To(BeEmpty())
		})

		It("leaves the outcome unknown when the connection drops", func() {
			srv.DropWrites()
			args := map[string]any{"text": "ls"}
			p, _ := f.preview("op1", tools.ToolTypeText, args)
			_, err := f.apply("op1", tools.ToolTypeText, args, p)
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).NotTo(ContainSubstring("ls"))
			rc, _ := f.apply("op1", tools.ToolTypeText, args, p)
			Expect(rc.State).To(Equal(contract.ReceiptUnknown))
			Expect(srv.Typed()).To(HaveLen(1))
		})
	})
})

var _ = Describe("send-key", func() {
	var (
		srv *pikvmtest.Server
		f   *fixture
	)
	BeforeEach(func() {
		srv = pikvmtest.New(devUser, devPass)
		f = newFixture(srv, GinkgoT().TempDir())
	})
	AfterEach(func() { srv.Close() })

	It("is refused while typing is off", func() {
		_, err := f.preview("op1", tools.ToolSendKey, map[string]any{"keys": []string{"enter"}})
		Expect(err).To(MatchError(ContainSubstring("keyboard input is off")))
	})

	Context("with typing on", func() {
		BeforeEach(func() { f.allow(false, true) })

		It("presses single keys and chords in order", func() {
			args := map[string]any{"keys": []string{"Escape", "F2", "ctrl+alt+delete", "a", "Down", "5", "ctrl+c"}}
			p, err := f.preview("op1", tools.ToolSendKey, args)
			Expect(err).NotTo(HaveOccurred())
			Expect(sampleOf(p)["keys"]).To(Equal([]any{"escape", "f2", "ctrl+alt+delete", "a", "down", "5", "ctrl+c"}))
			rc, err := f.apply("op1", tools.ToolSendKey, args, p)
			Expect(err).NotTo(HaveOccurred())
			Expect(rc.State).To(Equal(contract.ReceiptCommitted))
			Expect(srv.Keys()).To(Equal([]string{"Escape", "F2", "KeyA", "ArrowDown", "Digit5"}))
			Expect(srv.Shortcuts()).To(Equal([]string{"ControlLeft,AltLeft,Delete", "ControlLeft,KeyC"}))
		})

		DescribeTable("refuses anything outside the allowlist",
			func(key string) {
				_, err := f.preview("op1", tools.ToolSendKey, map[string]any{"keys": []string{key}})
				Expect(err).To(MatchError(ContainSubstring("not allowed")))
				Expect(srv.Writes()).To(Equal(0))
			},
			Entry("a Windows chord", "win+r"), Entry("meta", "meta"), Entry("super chord", "super+l"), Entry("ctrl+alt+f1", "ctrl+alt+f1"),
			Entry("ctrl+v", "ctrl+v"), Entry("alt+f4", "alt+f4"), Entry("delete alone", "delete"), Entry("power", "power"),
			Entry("f13", "f13"), Entry("a raw kvmd name", "MetaLeft"), Entry("a space", "space"), Entry("a list in one string", "a,b"), Entry("empty", ""))

		It("limits the number of keys and refuses an empty list", func() {
			_, err := f.preview("a", tools.ToolSendKey, map[string]any{"keys": []string{}})
			Expect(err).To(HaveOccurred())
			_, err = f.preview("b", tools.ToolSendKey, map[string]any{"keys": []string{"a", "a", "a", "a", "a", "a", "a", "a", "a"}})
			Expect(err).To(HaveOccurred())
		})

		It("does not accept text in a key call", func() {
			_, err := f.preview("a", tools.ToolSendKey, map[string]any{"keys": []string{"a"}, "text": "x"})
			Expect(err).To(HaveOccurred())
		})
	})
})

var _ = Describe("the settings tools of the page", func() {
	var (
		srv *pikvmtest.Server
		f   *fixture
	)
	BeforeEach(func() {
		srv = pikvmtest.New(devUser, devPass)
		f = newFixture(srv, GinkgoT().TempDir())
	})
	AfterEach(func() { srv.Close() })

	pageEnv := func(tool string, args map[string]any) contract.Envelope {
		args["tool"] = tool
		raw, _ := json.Marshal(args)
		return contract.Envelope{Request: contract.Request{AddonID: "pikvm", OperationID: "p", Arguments: raw}}
	}

	It("lets an administrator switch a device on, per device, and nobody else", func() {
		_, err := f.rt.Read(ctx, pageEnv(tools.ToolSetSettings, map[string]any{"device": srv.URL, "allowTyping": true}))
		Expect(err).NotTo(HaveOccurred())
		_, err = f.preview("op1", tools.ToolTypeText, map[string]any{"text": "ls"})
		Expect(err).NotTo(HaveOccurred())

		f.rt.Admin = admin(false)
		_, err = f.rt.Read(ctx, pageEnv(tools.ToolSetSettings, map[string]any{"device": srv.URL, "allowTyping": false}))
		Expect(err).To(MatchError(ContainSubstring("only an administrator")))
		f.rt.Admin = nil
		_, err = f.rt.Read(ctx, pageEnv(tools.ToolSetSettings, map[string]any{"device": srv.URL, "allowTyping": false}))
		Expect(err).To(HaveOccurred())
		res, err := f.rt.Read(ctx, pageEnv(tools.ToolGetSettings, map[string]any{}))
		Expect(err).NotTo(HaveOccurred())
		var r reply
		Expect(json.Unmarshal(res.Payload, &r)).To(Succeed())
		Expect(r.Data).To(HaveKeyWithValue("admin", false))
		Expect(r.Data["devices"]).To(HaveLen(1))
	})

	It("keeps one device's switch from opening another device", func() {
		other := pikvmtest.New(devUser, devPass)
		defer other.Close()
		_, err := f.rt.Read(ctx, pageEnv(tools.ToolSetSettings, map[string]any{"device": srv.URL, "allowHardPower": true}))
		Expect(err).NotTo(HaveOccurred())
		f2 := &fixture{srv: other, store: f.store, rt: f.rt, dev: other.URL}
		_, err = f2.preview("op1", tools.ToolPowerAction, map[string]any{"command": "off_hard"})
		Expect(err).To(MatchError(ContainSubstring("hard power action")))
	})

	It("refuses an address that is not a valid device address", func() {
		_, err := f.rt.Read(ctx, pageEnv(tools.ToolSetSettings, map[string]any{"device": "ftp://x", "allowTyping": true}))
		Expect(err).To(HaveOccurred())
	})
})
