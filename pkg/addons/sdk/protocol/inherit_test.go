// SPDX-License-Identifier: MIT
package protocol

import (
	"bufio"
	"net"
	"os"
	"strings"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("Channels passed as named handles", func() {
	It("binds 127.0.0.1 and reports the address as one line", func() {
		r, w, err := os.Pipe()
		Expect(err).NotTo(HaveOccurred())
		defer func() { _ = r.Close() }()
		l, err := bindAndReport(w)
		Expect(err).NotTo(HaveOccurred())
		defer func() { _ = l.Close() }()
		Expect(w.Close()).To(Succeed())
		line, err := bufio.NewReader(r).ReadString('\n')
		Expect(err).NotTo(HaveOccurred())
		Expect(line).To(Equal(l.Addr().String() + "\n"))
		host, _, err := net.SplitHostPort(strings.TrimSpace(line))
		Expect(err).NotTo(HaveOccurred())
		Expect(host).To(Equal("127.0.0.1"))
		c, err := net.Dial("tcp4", strings.TrimSpace(line))
		Expect(err).NotTo(HaveOccurred())
		Expect(c.Close()).To(Succeed())
	})
	It("closes the listener when the report cannot be written", func() {
		r, w, err := os.Pipe()
		Expect(err).NotTo(HaveOccurred())
		Expect(r.Close()).To(Succeed())
		Expect(w.Close()).To(Succeed())
		l, err := bindAndReport(w)
		Expect(err).To(HaveOccurred())
		Expect(l).To(BeNil())
	})
	// A valid handle is opened by the reported-handle specs of the add-on
	// runtime, which start a real child.
	DescribeTable("refuses a handle that is not a positive decimal",
		func(s string) {
			_, err := fileFromHandle(s, "startup")
			Expect(err).To(MatchError(ContainSubstring("not valid")))
		},
		Entry("empty", ""),
		Entry("zero", "0"),
		Entry("negative", "-1"),
		Entry("hex", "0x10"),
		Entry("text", "pipe"),
	)
})
