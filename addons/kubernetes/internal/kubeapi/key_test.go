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
	})
})
