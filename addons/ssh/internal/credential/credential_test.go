// SPDX-License-Identifier: MIT
package credential_test

import (
	"strings"

	"github.com/mudler/LocalAI/addons/ssh/internal/credential"
	"github.com/mudler/LocalAI/addons/ssh/sshtest"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("Parse", func() {
	It("reads user:password and splits at the first colon", func() {
		c, err := credential.Parse("deploy:pa:ss:word")
		Expect(err).NotTo(HaveOccurred())
		Expect(c.Kind).To(Equal(credential.KindPassword))
		Expect(c.User).To(Equal("deploy"))
		Expect(c.Auth()).To(HaveLen(2))
	})

	It("reads an OpenSSH private key", func() {
		_, key := sshtest.NewClientKey()
		c, err := credential.Parse(key)
		Expect(err).NotTo(HaveOccurred())
		Expect(c.Kind).To(Equal(credential.KindKey))
		Expect(c.User).To(BeEmpty())
		Expect(c.Auth()).To(HaveLen(1))
	})

	It("reads a PKCS#8 PEM private key, with spaces around it", func() {
		_, key := sshtest.NewClientKeyPKCS8()
		c, err := credential.Parse("\n  " + key + "\n")
		Expect(err).NotTo(HaveOccurred())
		Expect(c.Kind).To(Equal(credential.KindKey))
	})

	It("refuses a key with a passphrase, and says why", func() {
		_, err := credential.Parse(sshtest.NewEncryptedClientKey("hunter2hunter2"))
		Expect(err).To(MatchError(credential.ErrPassphrase))
	})

	It("refuses a key that is not one", func() {
		_, err := credential.Parse("-----BEGIN OPENSSH PRIVATE KEY-----\nnot a key\n-----END OPENSSH PRIVATE KEY-----")
		Expect(err).To(MatchError(credential.ErrBadKey))
	})

	DescribeTable("refuses what is neither a key nor user:password",
		func(secret string, want error) {
			_, err := credential.Parse(secret)
			Expect(err).To(MatchError(want))
		},
		Entry("empty", "", credential.ErrEmpty),
		Entry("blank", "  \n", credential.ErrEmpty),
		Entry("no colon", "justonestring", credential.ErrFormat),
		Entry("no password", "user:", credential.ErrFormat),
		Entry("no user", ":secret", credential.ErrFormat),
		Entry("a control character", "user:pass\nword", credential.ErrControl),
		Entry("too long", "user:"+strings.Repeat("x", credential.MaxLength), credential.ErrFormat),
	)
})

var _ = Describe("Scrub", func() {
	It("removes the password and the whole secret", func() {
		c, _ := credential.Parse("deploy:S3cretPass!")
		Expect(c.Scrub("pw=S3cretPass! and deploy:S3cretPass!")).To(Equal("pw=[redacted] and [redacted]"))
		Expect(c.Appears("echo S3cretPass!")).To(BeTrue())
	})

	It("removes a private key whole, by line and joined", func() {
		_, key := sshtest.NewClientKey()
		c, _ := credential.Parse(key)
		lines := strings.Split(strings.TrimSpace(key), "\n")
		body := strings.Join(lines[1:len(lines)-1], "")
		for _, leak := range []string{key, lines[1], body} {
			out := c.Scrub("before " + leak + " after")
			Expect(out).NotTo(ContainSubstring(lines[1]))
			Expect(out).To(ContainSubstring("[redacted]"))
			Expect(c.Appears(leak)).To(BeTrue())
		}
	})

	It("scrubs a short password from output but does not refuse every command for it", func() {
		c, _ := credential.Parse("deploy:ab")
		Expect(c.Scrub("a b ab")).To(Equal("a b [redacted]"))
		Expect(c.Appears("echo ab")).To(BeFalse())
	})
})
