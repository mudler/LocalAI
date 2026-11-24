// SPDX-License-Identifier: MIT
package tools_test

import (
	"github.com/mudler/LocalAI/addons/ssh/internal/sshclient"
	"github.com/mudler/LocalAI/addons/ssh/internal/tools"
	"github.com/mudler/LocalAI/addons/ssh/sshtest"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("get-info", func() {
	var f *fixture
	BeforeEach(func() { f = newFixture(sshtest.Options{}) })
	AfterEach(func() { f.srv.Close() })

	It("checks the connection and returns the pinned fingerprint, uname and user", func() {
		r, err := f.read(tools.ToolGetInfo, nil)
		Expect(err).NotTo(HaveOccurred())
		Expect(r.Source).To(Equal("ssh"))
		Expect(r.Untrusted).To(BeTrue())
		Expect(r.Data).To(HaveKeyWithValue("connected", true))
		Expect(r.Data).To(HaveKeyWithValue("hostKeyFingerprint", sshclient.FingerprintOfPin(sshclient.PinHex(f.srv.HostKey()))))
		Expect(r.Data["uname"]).To(ContainSubstring("Linux testhost"))
		Expect(r.Data).To(HaveKeyWithValue("user", user))
		Expect(r.Data).To(HaveKeyWithValue("login", "password"))
		Expect(f.srv.Commands()).To(Equal([]string{"uname -a", "id -un"}))
	})

	It("works with a private key", func() {
		pub, pemKey := sshtest.NewClientKey()
		f.srv.Close()
		f = newFixture(sshtest.Options{Key: pub})
		f.cred = pemKey
		r, err := f.read(tools.ToolGetInfo, nil)
		Expect(err).NotTo(HaveOccurred())
		Expect(r.Data).To(HaveKeyWithValue("login", "key"))
		Expect(r.Data).To(HaveKeyWithValue("user", user))
	})

	It("refuses a host that is not the one the host allowed", func() {
		_, err := f.read(tools.ToolGetInfo, map[string]any{"host": "deploy@other.example"})
		Expect(err).To(MatchError(ContainSubstring("host is not the one the host allowed")))
		Expect(f.srv.Logins()).To(Equal(0))
	})

	It("refuses a call with no credential, and a credential that does not parse", func() {
		f.cred = ""
		_, err := f.read(tools.ToolGetInfo, nil)
		Expect(err).To(MatchError(ContainSubstring("no credential")))
		f.cred = "nocolon"
		_, err = f.read(tools.ToolGetInfo, nil)
		Expect(err).To(MatchError(ContainSubstring("private key")))
	})

	It("refuses a password that belongs to another user than the address", func() {
		f.cred = "someone-else:" + pass
		_, err := f.read(tools.ToolGetInfo, nil)
		Expect(err).To(MatchError(ContainSubstring("not the user of the connection address")))
		Expect(f.srv.Logins()).To(Equal(0))
	})

	It("refuses a connection with no pinned host key and never connects", func() {
		f.host = f.addr("")
		_, err := f.read(tools.ToolGetInfo, nil)
		Expect(err).To(MatchError(ContainSubstring("no host key is pinned")))
		Expect(f.srv.Logins()).To(Equal(0))
		Expect(f.srv.Refused()).To(Equal(0))
	})

	It("refuses a changed host key with a clear message and no accept-anyway route", func() {
		f.srv.RotateHostKey()
		_, err := f.read(tools.ToolGetInfo, nil)
		Expect(err).To(MatchError(ContainSubstring("the host key changed")))
		Expect(err).To(MatchError(ContainSubstring("Nothing was sent")))
		Expect(f.srv.Refused()).To(Equal(0))
		Expect(f.srv.Logins()).To(Equal(0))
		// Declared tools hold nothing that could accept a key on the model's behalf.
		Expect(tools.Declared()).To(ConsistOf("get-info", "run", "scan-host-key"))
		Expect(tools.ReadTools()).NotTo(ContainElement("scan-host-key"))
	})

	It("says a wrong password was refused", func() {
		f.cred = user + ":not-the-password"
		_, err := f.read(tools.ToolGetInfo, nil)
		Expect(err).To(MatchError(ContainSubstring("refused the login")))
		Expect(err.Error()).NotTo(ContainSubstring("not-the-password"))
	})

	It("says the host is unreachable", func() {
		f.srv.Close()
		_, err := f.read(tools.ToolGetInfo, nil)
		Expect(err).To(MatchError(ContainSubstring("could not be reached")))
	})

	It("refuses an unknown tool and unknown fields", func() {
		_, err := f.read("shell", nil)
		Expect(err).To(MatchError(ContainSubstring("unknown tool")))
		_, err = f.read(tools.ToolGetInfo, map[string]any{"command": "id"})
		Expect(err).To(MatchError(ContainSubstring("arguments do not match")))
	})
})

var _ = Describe("scan-host-key", func() {
	var f *fixture
	BeforeEach(func() { f = newFixture(sshtest.Options{}) })
	AfterEach(func() { f.srv.Close() })

	It("returns the host key of an address without sending a credential", func() {
		r, err := f.read(tools.ToolScanHostKey, map[string]any{"address": f.addr("")})
		Expect(err).NotTo(HaveOccurred())
		pin := sshclient.PinHex(f.srv.HostKey())
		Expect(r.Data).To(HaveKeyWithValue("pin", pin))
		Expect(r.Data).To(HaveKeyWithValue("fingerprint", sshclient.FingerprintOfPin(pin)))
		Expect(r.Data).To(HaveKeyWithValue("keyType", "ssh-ed25519"))
		Expect(f.srv.Logins()).To(Equal(0))
		Expect(f.srv.Refused()).To(Equal(0))
	})

	It("refuses a bad address and an address that already has a pin", func() {
		_, err := f.read(tools.ToolScanHostKey, map[string]any{"address": "nope"})
		Expect(err).To(MatchError(ContainSubstring("address is not valid")))
		_, err = f.read(tools.ToolScanHostKey, map[string]any{"address": f.host})
		Expect(err).To(MatchError(ContainSubstring("without a host key pin")))
	})
})
