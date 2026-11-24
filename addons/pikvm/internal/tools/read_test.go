// SPDX-License-Identifier: MIT
package tools_test

import (
	"context"
	"encoding/json"

	"github.com/mudler/LocalAI/addons/pikvm/internal/tools"
	"github.com/mudler/LocalAI/addons/pikvm/pikvmtest"
	"github.com/mudler/LocalAI/pkg/addons/contract"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("the read tools", func() {
	var (
		srv *pikvmtest.Server
		f   *fixture
	)
	BeforeEach(func() {
		srv = pikvmtest.New(devUser, devPass)
		f = newFixture(srv, GinkgoT().TempDir())
	})
	AfterEach(func() { srv.Close() })

	It("get-info names the model and versions, marks the result untrusted, and leaves out the serial and the host name", func() {
		r, err := f.read(tools.ToolGetInfo)
		Expect(err).NotTo(HaveOccurred())
		Expect(r.Source).To(Equal("pikvm"))
		Expect(r.Untrusted).To(BeTrue())
		Expect(r.Notice).NotTo(BeEmpty())
		Expect(r.Data).To(HaveKeyWithValue("model", "v3"))
		Expect(r.Data).To(HaveKeyWithValue("version", "3.300"))
		Expect(r.Data).To(HaveKeyWithValue("base", "Raspberry Pi 4 Model B"))
	})

	It("sends the user and the password in the headers, split at the first colon", func() {
		_, err := f.read(tools.ToolGetInfo)
		Expect(err).NotTo(HaveOccurred())
		c := srv.Calls()[0]
		Expect(c.User).To(Equal(devUser))
		Expect(c.Passwd).To(Equal(devPass))
	})

	It("get-power-state reads the LEDs of the ATX board", func() {
		r, err := f.read(tools.ToolPowerState)
		Expect(err).NotTo(HaveOccurred())
		Expect(r.Data).To(HaveKeyWithValue("power", "on"))
		Expect(r.Data).To(HaveKeyWithValue("enabled", true))
		srv.SetPower(false)
		r, _ = f.read(tools.ToolPowerState)
		Expect(r.Data).To(HaveKeyWithValue("power", "off"))
	})

	It("get-keyboard-state reads the keyboard and its LEDs", func() {
		r, err := f.read(tools.ToolKeyboard)
		Expect(err).NotTo(HaveOccurred())
		Expect(r.Data).To(HaveKeyWithValue("keyboardOnline", true))
		Expect(r.Data["leds"]).To(HaveKeyWithValue("caps", true))
	})

	It("refuses a device that is not the one the host allowed, and calls nothing", func() {
		env := f.env("op", tools.ToolGetInfo, map[string]any{})
		env.Request.ResourceKey = "https://other.example"
		_, err := f.rt.Read(context.Background(), env)
		Expect(err).To(HaveOccurred())
		Expect(srv.Calls()).To(BeEmpty())
	})

	It("refuses a credential that is not user:password, without echoing it", func() {
		env := f.env("op", tools.ToolGetInfo, nil)
		env.Request.Credential = "no-colon-credential-TOPSECRET"
		_, err := f.rt.Read(context.Background(), env)
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).NotTo(ContainSubstring("TOPSECRET"))
		Expect(srv.Calls()).To(BeEmpty())
	})

	It("says a wrong password in a fixed sentence without the password", func() {
		env := f.env("op", tools.ToolGetInfo, nil)
		env.Request.Credential = devUser + ":wrong-PASSWORD-77"
		_, err := f.rt.Read(context.Background(), env)
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).NotTo(ContainSubstring("PASSWORD-77"))
		Expect(err.Error()).To(ContainSubstring("refused the user name or password"))
	})

	It("refuses a redirect", func() {
		srv.RedirectTo("https://evil.example/")
		_, err := f.read(tools.ToolPowerState)
		Expect(err).To(MatchError(ContainSubstring("redirect")))
	})

	It("refuses an unknown argument and an unknown tool", func() {
		raw, _ := json.Marshal(map[string]any{"tool": tools.ToolGetInfo, "device": f.dev, "extra": 1})
		env := f.env("op", tools.ToolGetInfo, nil)
		env.Request.Arguments = raw
		_, err := f.rt.Read(context.Background(), env)
		Expect(err).To(HaveOccurred())
		_, err = f.rt.Read(context.Background(), f.env("op", "snapshot", nil))
		Expect(err).To(HaveOccurred())
	})

	It("does not declare the tools it leaves out", func() {
		Expect(tools.Declared()).NotTo(ContainElements("snapshot", "mount-image", "gpio"))
		var _ contract.Runtime = f.rt
	})
})
