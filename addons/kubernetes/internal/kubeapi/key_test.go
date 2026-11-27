// SPDX-License-Identifier: MIT
package kubeapi_test

import (
	"errors"
	"strings"

	"github.com/mudler/LocalAI/addons/kubernetes/internal/kubeapi"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("the key of a connection", func() {
	It("round-trips a key", func() {
		c, err := kubeapi.ParseKey("https://cluster.example:6443#context=prod")
		Expect(err).NotTo(HaveOccurred())
		Expect(c.Server).To(Equal("https://cluster.example:6443"))
		Expect(c.Context).To(Equal("prod"))
		Expect(c.Key()).To(Equal("https://cluster.example:6443#context=prod"))
	})

	It("lower-cases the host and drops one trailing slash", func() {
		c, err := kubeapi.ParseKey("https://Cluster.Example:6443/#context=Prod")
		Expect(err).NotTo(HaveOccurred())
		Expect(c.Key()).To(Equal("https://cluster.example:6443#context=Prod"))
	})

	It("refuses http, paths, queries, empty context, control chars and long keys", func() {
		for _, k := range []string{
			"http://cluster.example:6443#context=prod",
			"https://cluster.example:6443/api#context=prod",
			"https://cluster.example:6443?x=1#context=prod",
			"https://cluster.example:6443?#context=prod",
			"https://user@cluster.example:6443#context=prod",
			"https://cluster.example:6443#context=",
			"https://cluster.example:6443",
			"https://cluster.example:6443#ctx=prod",
			"https://cluster.example:6443#context=a\x01b",
			"https://cluster.example:6443#context=a b",
			"https://#context=prod",
			"https://cluster.example:6443#context=" + strings.Repeat("a", kubeapi.MaxKey),
		} {
			_, err := kubeapi.ParseKey(k)
			Expect(errors.Is(err, kubeapi.ErrBadKey)).To(BeTrue(), k)
		}
		// A bare ? or # is a query or a fragment too, as the page reads it.
		for _, s := range []string{"https://cluster.example?", "https://cluster.example#", "https://cluster.example:6443/?", "https://cluster.example/#"} {
			_, err := kubeapi.NormalizeServer(s)
			Expect(err).To(HaveOccurred(), s)
		}
	})

	It("writes one server one way: lower case, no default port, no trailing dot or slash", func() {
		for _, in := range []string{"https://Cluster.Example", "https://cluster.example:443", "https://cluster.example.:443/", "https://CLUSTER.example./"} {
			s, err := kubeapi.NormalizeServer(in)
			Expect(err).NotTo(HaveOccurred(), in)
			Expect(s).To(Equal("https://cluster.example"), in)
		}
		s, err := kubeapi.NormalizeServer("https://[2001:DB8::1]:443")
		Expect(err).NotTo(HaveOccurred())
		Expect(s).To(Equal("https://[2001:db8::1]"))
		s, err = kubeapi.NormalizeServer("https://cluster.example:6443/")
		Expect(err).NotTo(HaveOccurred())
		Expect(s).To(Equal("https://cluster.example:6443"))
	})

	It("refuses a loopback, link-local, unspecified or multicast address", func() {
		for _, in := range []string{"https://127.0.0.1:6443", "https://127.1.2.3", "https://[::1]:6443", "https://169.254.169.254",
			"https://[fe80::1]:6443", "https://0.0.0.0:6443", "https://[::]:443", "https://224.0.0.1", "https://[::ffff:127.0.0.1]:6443",
			"https://[64:ff9b::7f00:1]", "https://[64:ff9b::a9fe:a9fe]", "https://0.1.2.3"} {
			_, err := kubeapi.NormalizeServer(in)
			Expect(err).To(HaveOccurred(), in)
			_, err = kubeapi.ParseKey(in + "#context=dev")
			Expect(err).To(HaveOccurred(), in)
		}
		for _, in := range []string{"https://10.0.0.1:6443", "https://192.168.1.10:6443", "https://cluster.example:6443"} {
			_, err := kubeapi.NormalizeServer(in)
			Expect(err).NotTo(HaveOccurred(), in)
		}
	})

	It("writes an IP address in its canonical form", func() {
		for in, want := range map[string]string{
			"https://[2001:db8::1]":          "https://[2001:db8::1]",
			"https://[2001:DB8:0:0::1]":      "https://[2001:db8::1]",
			"https://[2001:0db8::0001]":      "https://[2001:db8::1]",
			"https://[2001:db8:0:0:0:0:0:1]": "https://[2001:db8::1]",
			"https://[::ffff:10.0.0.1]":      "https://10.0.0.1",
			"https://[::ffff:a00:1]:6443":    "https://10.0.0.1:6443",
			"https://10.0.0.1":               "https://10.0.0.1",
			"https://[2001:db8::1]:0443":     "https://[2001:db8::1]",
		} {
			s, err := kubeapi.NormalizeServer(in)
			Expect(err).NotTo(HaveOccurred(), in)
			Expect(s).To(Equal(want), in)
		}
	})

	It("reads the port as a number", func() {
		for in, want := range map[string]string{
			"https://cluster.example:0443":  "https://cluster.example",
			"https://cluster.example:00443": "https://cluster.example",
			"https://cluster.example:06443": "https://cluster.example:6443",
			"https://cluster.example:80":    "https://cluster.example:80",
		} {
			s, err := kubeapi.NormalizeServer(in)
			Expect(err).NotTo(HaveOccurred(), in)
			Expect(s).To(Equal(want), in)
		}
		for _, in := range []string{"https://cluster.example:0", "https://cluster.example:65536", "https://cluster.example:+443", "https://cluster.example:99999999999999999999"} {
			_, err := kubeapi.NormalizeServer(in)
			Expect(err).To(HaveOccurred(), in)
		}
	})

	It("is idempotent, also with many trailing dots", func() {
		for _, in := range []string{"https://x.example.", "https://x.example..", "https://X.Example...:0443/", "https://[2001:db8:0::1]", "https://[::ffff:10.0.0.1]:06443"} {
			a, err := kubeapi.NormalizeServer(in)
			Expect(err).NotTo(HaveOccurred(), in)
			b, err := kubeapi.NormalizeServer(a)
			Expect(err).NotTo(HaveOccurred(), a)
			Expect(b).To(Equal(a), in)
		}
		s, _ := kubeapi.NormalizeServer("https://x.example..")
		Expect(s).To(Equal("https://x.example"))
		_, err := kubeapi.NormalizeServer("https://...")
		Expect(err).To(HaveOccurred())
	})
})
