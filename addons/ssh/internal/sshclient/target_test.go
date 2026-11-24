// SPDX-License-Identifier: MIT
package sshclient_test

import (
	"errors"
	"strings"

	"github.com/mudler/LocalAI/addons/ssh/internal/sshclient"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var pin = strings.Repeat("ab", 32)

var _ = Describe("ParseTarget", func() {
	DescribeTable("reads an address",
		func(in, key, wantPin string) {
			t, err := sshclient.ParseTarget(in)
			Expect(err).NotTo(HaveOccurred())
			Expect(t.Key()).To(Equal(key))
			Expect(t.Pin).To(Equal(wantPin))
		},
		Entry("default port", "root@Example.COM", "root@example.com:22", ""),
		Entry("a port and a pin", "deploy@10.0.0.5:2222#sha256="+strings.ToUpper(pin), "deploy@10.0.0.5:2222", pin),
		Entry("an IPv6 address with a port", "u@[::1]:2200", "u@[::1]:2200", ""),
		Entry("an IPv6 address without a port", "u@[2001:db8::1]", "u@[2001:db8::1]:22", ""),
	)

	DescribeTable("refuses an address",
		func(in string) {
			_, err := sshclient.ParseTarget(in)
			Expect(err).To(MatchError(sshclient.ErrBadAddress))
		},
		Entry("with no user", "example.com"),
		Entry("with an empty user", "@example.com"),
		Entry("with a user that has a space", "a b@example.com"),
		Entry("with a bare IPv6 address", "u@::1"),
		Entry("with a port that is not a number", "u@h:ssh"),
		Entry("with a port out of range", "u@h:70000"),
		Entry("with a port that has a zero in front", "u@h:022"),
		Entry("with a path", "u@h/etc"),
		Entry("with a short pin", "u@h#sha256=abcd"),
		Entry("with another fragment", "u@h#md5="+pin),
		Entry("with a space in the host", "u@a b"),
	)

	It("shows a pin the way ssh-keygen does", func() {
		fp := sshclient.FingerprintOfPin(pin)
		Expect(fp).To(HavePrefix("SHA256:"))
		Expect(fp).NotTo(HaveSuffix("="))
		Expect(sshclient.FingerprintOfPin("zz")).To(BeEmpty())
	})
})

func errorsAs(err error, target any) bool { return errors.As(err, target) }
