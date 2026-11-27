// SPDX-License-Identifier: MIT
package platform_test

import (
	"context"
	"strings"
	"time"

	"github.com/mudler/LocalAI/addons/internal/connector"
	"github.com/mudler/LocalAI/addons/kubernetes/internal/platform"
	"github.com/mudler/LocalAI/pkg/addons/contract"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

const good = `apiVersion: v1
kind: Config
clusters:
- name: c
  cluster:
    server: https://cluster.example:6443
contexts:
- name: dev
  context: {cluster: c, user: u}
users:
- name: u
  user:
    token: kube-token-very-secret-0123
`

var _ = Describe("the connector route", func() {
	p := platform.Platform{}
	ctx := context.Background()

	It("serves the one kind and carries no messages", func() {
		Expect(p.Kind()).To(Equal("kubernetes"))
		_, err := p.Send(ctx, contract.ConnectorRequest{})
		Expect(err).To(MatchError(platform.ErrNoMessages))
		Expect(p.Edit(ctx, contract.ConnectorRequest{})).To(MatchError(platform.ErrNoMessages))
	})

	It("answers a poll after the wait and returns nothing", func() {
		msgs, err := p.Poll(ctx, "t", 0, 0)
		Expect(err).NotTo(HaveOccurred())
		Expect(msgs).To(BeEmpty())
		c, cancel := context.WithTimeout(ctx, 20*time.Millisecond)
		defer cancel()
		_, err = p.Poll(c, "t", 0, 30)
		Expect(err).To(HaveOccurred())
	})

	It("accepts a kubeconfig that the add-on can use and refuses an unsafe one", func() {
		b, err := p.Me(ctx, good)
		Expect(err).NotTo(HaveOccurred())
		Expect(b.ID).NotTo(BeEmpty())
		for _, bad := range []string{"", "not a kubeconfig", strings.Replace(good, "token: kube-token-very-secret-0123", "exec: {command: aws}", 1)} {
			_, err := p.Me(ctx, bad)
			Expect(err).To(MatchError(connector.ErrUnauthorized), bad)
		}
	})
})
