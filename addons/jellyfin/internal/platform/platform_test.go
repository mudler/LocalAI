// SPDX-License-Identifier: MIT
package platform_test

import (
	"context"
	"testing"

	"github.com/mudler/LocalAI/addons/internal/connector"
	"github.com/mudler/LocalAI/addons/jellyfin/internal/platform"
	"github.com/mudler/LocalAI/pkg/addons/contract"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func TestPlatform(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Jellyfin add-on connector route")
}

var _ = Describe("the connector route", func() {
	serve := func(token, op string) contract.ConnectorResponse {
		out, err := connector.Serve(context.Background(), platform.Platform{}, contract.ConnectorRequest{Op: op, Kind: "jellyfin", Token: token})
		Expect(err).NotTo(HaveOccurred())
		return out
	}
	It("accepts a plausible key on me without making a request", func() {
		Expect(serve("0123abcd", contract.ConnectorOpMe).Error).To(BeNil())
	})
	It("refuses a key that cannot be one", func() {
		for _, k := range []string{"a b", `a"b`} {
			out := serve(k, contract.ConnectorOpMe)
			Expect(out.Error).NotTo(BeNil())
			Expect(out.Error.Code).To(Equal(contract.ConnectorUnauthorized))
		}
	})
	It("polls without messages and sends nothing", func() {
		Expect(serve("k", contract.ConnectorOpPoll).Updates).To(BeEmpty())
		out, err := connector.Serve(context.Background(), platform.Platform{}, contract.ConnectorRequest{Op: contract.ConnectorOpSend, Kind: "jellyfin", Token: "k", Chat: "c", Text: "hi"})
		Expect(err).NotTo(HaveOccurred())
		Expect(out.Error).NotTo(BeNil())
	})
})
