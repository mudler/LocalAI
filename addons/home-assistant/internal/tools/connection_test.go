// SPDX-License-Identifier: MIT
package tools_test

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/mudler/LocalAI/addons/home-assistant/hatest"
	"github.com/mudler/LocalAI/addons/internal/protocol"
	"github.com/mudler/LocalAI/pkg/addons/contract"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// The login of every call comes from the connection: the host sets the token
// and the address in the request, and the model's arguments hold neither.
var _ = Describe("the connection as the only source of the login", func() {
	var (
		srv *hatest.Server
		g   *rig
	)
	BeforeEach(func() {
		srv = home()
		DeferCleanup(srv.Close)
		g = newRig(srv, "")
	})

	// bare is a call as a model makes it with one connection: the tool name and
	// nothing about the address or the login.
	bare := func(tool string, extra map[string]any) contract.Envelope {
		args := map[string]any{"tool": tool}
		for k, v := range extra {
			args[k] = v
		}
		raw, _ := json.Marshal(args)
		return contract.Envelope{Request: contract.Request{AddonID: "home-assistant", OperationID: "op-bare", Arguments: raw, Credential: token, ResourceKey: srv.URL}}
	}

	It("reads with the token and the address of the connection when the call names neither", func() {
		res, err := g.rt.Read(context.Background(), bare("get-config", nil))
		Expect(err).NotTo(HaveOccurred())
		Expect(string(res.Payload)).To(ContainSubstring("2026.11.0"))
		Expect(string(res.Payload)).NotTo(ContainSubstring(token))
		calls := srv.Calls()
		Expect(calls).NotTo(BeEmpty())
		for _, c := range calls {
			Expect(c.Token).To(Equal(token), "the fake Home Assistant saw the token of the connection in Authorization")
		}
	})

	It("works for every read tool without an instance", func() {
		for tool, extra := range map[string]map[string]any{
			"get-config": nil, "list-areas": nil, "list-services": nil,
			"search-entities": {"query": "kitchen"}, "get-state": {"entityId": "light.kitchen"},
		} {
			_, err := g.rt.Read(context.Background(), bare(tool, extra))
			Expect(err).NotTo(HaveOccurred(), tool)
		}
	})

	It("accepts an instance that is the address of the connection", func() {
		_, err := g.rt.Read(context.Background(), bare("get-config", map[string]any{"instance": srv.URL}))
		Expect(err).NotTo(HaveOccurred())
	})

	It("still refuses an instance that is not the address of the connection, before it calls anything", func() {
		before := len(srv.Calls())
		_, err := g.rt.Read(context.Background(), bare("get-config", map[string]any{"instance": "http://192.168.0.99:8123"}))
		Expect(errors.Is(err, protocol.ErrBadRequest)).To(BeTrue())
		Expect(err).To(MatchError(ContainSubstring("not the one the host allowed")))
		Expect(srv.Calls()).To(HaveLen(before))
	})

	It("never takes a token or an address from the arguments of the model", func() {
		for _, field := range []string{"token", "accessToken", "access_token", "credential", "authorization", "url", "address", "resourceKey"} {
			before := len(srv.Calls())
			_, err := g.rt.Read(context.Background(), bare("get-config", map[string]any{field: "http://192.168.0.99:8123"}))
			Expect(err).To(MatchError(ContainSubstring("do not match")), field)
			Expect(srv.Calls()).To(HaveLen(before), field)
		}
	})

	It("takes the address of a change from the connection and shows it", func() {
		env := bare("call-service", map[string]any{"domain": "light", "service": "turn_off", "entityIds": []string{"light.kitchen"}})
		p, err := g.rt.Preview(context.Background(), env)
		Expect(err).NotTo(HaveOccurred())
		Expect(string(p.Summary)).To(ContainSubstring(srv.URL))
	})

	Describe("when Home Assistant refuses the login of the connection", func() {
		It("tells the model with a typed 401 and a fixed sentence to give to the person", func() {
			env := bare("get-config", nil)
			env.Request.Credential = "rejected-token-value-0123456789"
			_, err := g.rt.Read(context.Background(), env)
			Expect(errors.Is(err, protocol.ErrBadRequest)).To(BeTrue())
			up, ok := contract.UpstreamOf(err)
			Expect(ok).To(BeTrue(), "an untyped refusal reaches the model as a bare invalid_request")
			Expect(up.Status).To(Equal(401))
			Expect(up.Hint).To(ContainSubstring("replace the token"))
			Expect(up.Hint).To(ContainSubstring("connection"))
			for _, s := range []string{err.Error(), up.Hint} {
				Expect(s).NotTo(ContainSubstring("rejected-token-value"))
				Expect(s).NotTo(ContainSubstring(token))
				Expect(s).NotTo(ContainSubstring(srv.URL))
			}
		})

		It("tells the model with a typed 403 when the token has no permission", func() {
			srv.Fail("/api/config", 403)
			_, err := g.rt.Read(context.Background(), bare("get-config", nil))
			up, ok := contract.UpstreamOf(err)
			Expect(ok).To(BeTrue())
			Expect(up.Status).To(Equal(403))
		})

		It("says there is no token when the connection has none, without calling Home Assistant", func() {
			env := bare("get-config", nil)
			env.Request.Credential = ""
			_, err := g.rt.Read(context.Background(), env)
			Expect(errors.Is(err, protocol.ErrBadRequest)).To(BeTrue())
			Expect(err).To(MatchError(ContainSubstring("no token")))
			Expect(srv.Calls()).To(BeEmpty())
		})
	})
})
