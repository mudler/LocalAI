// SPDX-License-Identifier: MIT
package application

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"

	"github.com/mudler/LocalAI/core/http/auth"
	"github.com/mudler/LocalAI/core/services/addons"
	"github.com/mudler/LocalAI/core/services/addons/gateway"
	"github.com/mudler/LocalAI/core/services/studio"
	"github.com/mudler/LocalAI/core/services/studio/agents"
	"github.com/mudler/LocalAI/core/services/studio/connectors"
	"github.com/mudler/LocalAI/core/services/studio/provider"
	"github.com/mudler/LocalAI/core/services/studio/store"
	"github.com/mudler/LocalAI/core/services/studio/studiotest"
	"github.com/mudler/LocalAI/pkg/addons/contract"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

type ownerConnectorSource struct {
	calls           []contract.ConnectorRequest
	unavailable     bool
	callUnavailable bool
}

func (s *ownerConnectorSource) ConnectorKinds(context.Context) ([]addons.ConnectorKind, error) {
	if s.unavailable {
		return nil, gateway.ErrUnavailable
	}
	return []addons.ConnectorKind{{AddonID: "slack", ConnectorKind: contract.ConnectorKind{ID: "slack", Title: "Slack", Delivery: contract.DeliveryPush}}}, nil
}
func (s *ownerConnectorSource) Connector(_ context.Context, addon string, req contract.ConnectorRequest) (contract.ConnectorResponse, error) {
	Expect(addon).To(Equal("slack"))
	s.calls = append(s.calls, req)
	if s.callUnavailable {
		return contract.ConnectorResponse{}, gateway.ErrUnavailable
	}
	if req.Token == "refused" {
		return contract.ConnectorResponse{Error: &contract.ConnectorError{Code: contract.ConnectorUnauthorized}}, nil
	}
	return contract.ConnectorResponse{BotID: "bot", BotUsername: "owner-bot"}, nil
}

var _ = Describe("connector owner routing", func() {
	It("uses real peer admission, owner authority and stored credentials through a runtime-free frontend", func() {
		ctx := context.Background()
		keys := filepath.Join(GinkgoT().TempDir(), "keys")
		Expect(gateway.GenerateCarrierKeys(keys, []string{"127.0.0.1"})).To(Succeed())
		cfg, err := gateway.LoadCarrierConfig(keys, "owner", "https://127.0.0.1:1")
		Expect(err).NotTo(HaveOccurred())
		owner, err := gateway.NewCarrier(*cfg)
		Expect(err).NotTo(HaveOccurred())
		source := &ownerConnectorSource{}
		// The forwarder on the receiving gateway must never be followed by private
		// owner routes. Its deliberately unreachable address catches routing loops.
		ownerApp := &Application{}
		ownerGateway := &gateway.Gateway{Connectors: source, ConnectorAuthority: connectorAuthority{ownerApp}, Forward: owner, Revalidate: func(context.Context, contract.Actor) error { return gateway.ErrForbidden }}
		handler, err := owner.OwnerHandler(ownerGateway)
		Expect(err).NotTo(HaveOccurred())
		var wire []byte
		server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Method == http.MethodPost {
				wire, err = io.ReadAll(r.Body)
				Expect(err).NotTo(HaveOccurred())
				r.Body = io.NopCloser(bytes.NewReader(wire))
			}
			handler.ServeHTTP(w, r)
		}))
		server.TLS = owner.ServerTLS()
		server.StartTLS()
		DeferCleanup(server.Close)
		fc, err := gateway.LoadCarrierConfig(keys, "frontend", server.URL)
		Expect(err).NotTo(HaveOccurred())
		front, err := gateway.NewCarrier(*fc)
		Expect(err).NotTo(HaveOccurred())
		app := &Application{addonGateway: &gateway.Gateway{Forward: front}}
		tr := connectorTransport{app}
		kinds, err := tr.Kinds(ctx)
		Expect(err).NotTo(HaveOccurred())
		Expect(kinds).To(HaveLen(1))
		Expect(kinds[0].ID).To(Equal("slack"))
		chatStore, err := store.NewJSON("")
		Expect(err).NotTo(HaveOccurred())
		agentStore, err := agents.NewJSON("")
		Expect(err).NotTo(HaveOccurred())
		denyNamed := false
		svc, err := studio.New(studio.Deps{Store: chatStore, Agents: agentStore, Provider: provider.New(), Harness: studiotest.NewGateHarness(), StateDir: GinkgoT().TempDir(), Users: func(_ context.Context, id string) (*auth.User, error) {
			if id == "named" && denyNamed {
				return nil, studio.ErrNoUser
			}
			if id == "named" || id == legacyAPIKeyUserID {
				return &auth.User{ID: id}, nil
			}
			return nil, studio.ErrNoUser
		}})
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(func() { Expect(svc.Close(ctx)).To(Succeed()) })
		dir := GinkgoT().TempDir()
		bindings, err := connectors.NewJSON(dir)
		Expect(err).NotTo(HaveOccurred())
		makeConnectors := func() *studio.Connectors {
			c, e := studio.NewConnectors(svc, studio.ConnectorOptions{Store: bindings, Transport: tr, VerifyToken: studio.VerifyTokenWithMe(tr)})
			Expect(e).NotTo(HaveOccurred())
			DeferCleanup(c.Close)
			return c
		}
		ownerApp.studioConnectors = makeConnectors()
		client := makeConnectors()
		for _, user := range []string{"named", studio.AnonymousPrincipal, legacyAPIKeyUserID} {
			ag, e := svc.SaveAgent(ctx, user, agents.Agent{Name: "Support", Model: "fake"})
			Expect(e).NotTo(HaveOccurred())
			info, e := client.CreateConnector(ctx, user, studio.ConnectorInput{Kind: "slack", Name: "Slack", AgentID: ag.ID, Token: "stored-secret", AcceptAny: true, Enabled: true})
			Expect(e).NotTo(HaveOccurred())
			b, e := bindings.GetBinding(ctx, info.ID)
			Expect(e).NotTo(HaveOccurred())
			Expect(b.Token).NotTo(BeEmpty())
			scope := studio.BindingConnectorScope(b)
			out, e := tr.Call(ctx, scope, contract.ConnectorRequest{Op: contract.ConnectorOpMe, Kind: "slack", Token: "substituted-secret"})
			Expect(e).NotTo(HaveOccurred())
			Expect(out.BotID).To(Equal("bot"))
			Expect(string(wire)).NotTo(ContainSubstring("substituted-secret"))
			Expect(string(wire)).NotTo(ContainSubstring("stored-secret"))
			Expect(source.calls[len(source.calls)-1].Token).To(Equal("stored-secret"))
			for _, field := range []string{"principal", "binding", "addon", "kind", "agent"} {
				bad := scope
				switch field {
				case "principal":
					bad.Principal = "other"
				case "binding":
					bad.BindingID = "missing"
				case "addon":
					bad.AddonID = "other"
				case "kind":
					bad.Kind = "other"
				case "agent":
					bad.AgentID = "other"
				}
				before := len(source.calls)
				_, e = tr.Call(ctx, bad, contract.ConnectorRequest{Op: contract.ConnectorOpMe, Kind: bad.Kind})
				Expect(e).To(MatchError(connectors.ErrForbidden))
				Expect(source.calls).To(HaveLen(before))
			}
			verify := scope
			verify.Purpose = studio.ConnectorPurposeVerifyToken
			for _, op := range []string{contract.ConnectorOpSend, contract.ConnectorOpPoll, contract.ConnectorOpMedia} {
				_, e = tr.Call(ctx, verify, contract.ConnectorRequest{Op: op, Kind: "slack", Token: "prospective"})
				Expect(e).To(MatchError(connectors.ErrForbidden))
			}
			_, e = tr.Call(ctx, verify, contract.ConnectorRequest{Op: contract.ConnectorOpMe, Kind: "slack", Token: "prospective", Chat: "mixed"})
			Expect(e).To(MatchError(connectors.ErrForbidden))
			_, e = tr.Call(ctx, verify, contract.ConnectorRequest{Op: contract.ConnectorOpMe, Kind: "slack", Token: "replacement"})
			Expect(e).NotTo(HaveOccurred())
			Expect(source.calls[len(source.calls)-1].Token).To(Equal("replacement"))
			// Explicit platform refusal cannot save a partial binding.
			_, e = client.CreateConnector(ctx, user, studio.ConnectorInput{Kind: "slack", Name: "Rejected", AgentID: ag.ID, Token: "refused", AcceptAny: true})
			Expect(e).To(MatchError(studio.ErrTokenRefused))
			mine, e := bindings.ListBindings(ctx, user)
			Expect(e).NotTo(HaveOccurred())
			Expect(mine).To(HaveLen(1))
		}
		reloaded, e := connectors.NewJSON(dir)
		Expect(e).NotTo(HaveOccurred())
		all, e := reloaded.ListBindings(ctx, "named")
		Expect(e).NotTo(HaveOccurred())
		Expect(all).To(HaveLen(1))
		// Exercise service writes through the carrier, not just raw transport calls.
		agentID := all[0].AgentID
		input := studio.ConnectorInput{Kind: "slack", Name: "Changed", AgentID: agentID, Token: "replacement", AcceptAny: true, Enabled: true}
		before := len(source.calls)
		original := all[0]
		denyNamed = true
		_, createErr := client.CreateConnector(ctx, "named", input)
		_, updateErr := client.UpdateConnector(ctx, "named", original.ID, input)
		deniedStore, e := connectors.NewJSON(dir)
		Expect(e).NotTo(HaveOccurred())
		deniedBindings, e := deniedStore.ListBindings(ctx, "named")
		Expect(e).NotTo(HaveOccurred())
		unchanged, e := deniedStore.GetBinding(ctx, original.ID)
		Expect(e).NotTo(HaveOccurred())
		Expect(createErr).To(MatchError(connectors.ErrForbidden), "owner denial must stop create before persistence")
		Expect(updateErr).To(MatchError(connectors.ErrForbidden), "owner denial must stop token replacement")
		Expect(deniedBindings).To(HaveLen(1))
		Expect(unchanged).To(Equal(original))
		Expect(source.calls).To(HaveLen(before))
		denyNamed = false

		// Discovery succeeds, but the runtime becomes unavailable during me.
		// This is not an authority denial: complete saves and replacements remain permissive.
		source.callUnavailable = true
		created, e := client.CreateConnector(ctx, "named", input)
		Expect(e).NotTo(HaveOccurred())
		input.Token = "outage-replacement"
		_, e = client.UpdateConnector(ctx, "named", original.ID, input)
		Expect(e).NotTo(HaveOccurred())
		outageStore, e := connectors.NewJSON(dir)
		Expect(e).NotTo(HaveOccurred())
		outageBindings, e := outageStore.ListBindings(ctx, "named")
		Expect(e).NotTo(HaveOccurred())
		Expect(outageBindings).To(HaveLen(2))
		for _, id := range []string{created.ID, original.ID} {
			b, e := outageStore.GetBinding(ctx, id)
			Expect(e).NotTo(HaveOccurred())
			Expect(b.Token).NotTo(BeEmpty())
			Expect(b.AgentID).To(Equal(agentID))
			source.callUnavailable = false
			_, e = tr.Call(ctx, studio.BindingConnectorScope(b), contract.ConnectorRequest{Op: contract.ConnectorOpMe, Kind: "slack"})
			Expect(e).NotTo(HaveOccurred())
			expected := "replacement"
			if id == original.ID {
				expected = "outage-replacement"
			}
			Expect(source.calls[len(source.calls)-1].Token).To(Equal(expected))
		}
		// Unknown named users are refused during both prospective and replacement
		// validation, even when an agent or stored binding exists for that identity.
		ag, e := svc.SaveAgent(ctx, "missing-user", agents.Agent{Name: "Orphan", Model: "fake"})
		Expect(e).NotTo(HaveOccurred())
		s := studio.ConnectorCallScope{Purpose: studio.ConnectorPurposeVerifyToken, Principal: "missing-user", AgentID: ag.ID, AddonID: "slack", Kind: "slack"}
		_, e = tr.Call(ctx, s, contract.ConnectorRequest{Op: contract.ConnectorOpMe, Kind: "slack", Token: "new"})
		Expect(e).To(MatchError(connectors.ErrForbidden))
		source.unavailable = true
		_, e = tr.Kinds(ctx)
		Expect(e).To(MatchError(gateway.ErrUnavailable))
		source.unavailable = false
		// A different certificate signed by another keyset is not a pinned peer.
		alienKeys := filepath.Join(GinkgoT().TempDir(), "keys")
		Expect(gateway.GenerateCarrierKeys(alienKeys, []string{"127.0.0.1"})).To(Succeed())
		alienCfg, e := gateway.LoadCarrierConfig(alienKeys, "frontend", server.URL)
		Expect(e).NotTo(HaveOccurred())
		alienCfg.Roots = fc.Roots
		alienCfg.Pins = fc.Pins
		alien, e := gateway.NewCarrier(*alienCfg)
		Expect(e).NotTo(HaveOccurred())
		before = len(source.calls)
		_, e = (&gateway.Gateway{Forward: alien}).ConnectorCall(ctx, s, contract.ConnectorRequest{Op: contract.ConnectorOpMe, Kind: "slack", Token: "new"})
		Expect(e).To(HaveOccurred())
		Expect(source.calls).To(HaveLen(before))
		// Exercise bounded decoding through the authenticated handler, not a mock.
		payload, _ := json.Marshal(map[string]any{"scope": s, "request": contract.ConnectorRequest{Op: contract.ConnectorOpMe, Kind: "slack", Token: "new"}})
		transport := &http.Transport{TLSClientConfig: &tls.Config{Certificates: []tls.Certificate{fc.Certificate}, RootCAs: fc.Roots, MinVersion: tls.VersionTLS12}}
		defer transport.CloseIdleConnections()
		httpClient := &http.Client{Transport: transport}
		resp, e := httpClient.Post(server.URL+"/addons/connectors/call", "application/json", strings.NewReader(string(payload)+strings.Repeat(" ", 2<<20)))
		Expect(e).NotTo(HaveOccurred())
		defer resp.Body.Close()
		Expect(resp.StatusCode).To(Equal(http.StatusForbidden))
	})
})
