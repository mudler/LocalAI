// SPDX-License-Identifier: MIT
package application

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/core/http/auth"
	"github.com/mudler/LocalAI/core/services/addons"
	"github.com/mudler/LocalAI/core/services/addons/gateway"
	"github.com/mudler/LocalAI/core/services/studio"
	"github.com/mudler/LocalAI/core/services/studio/agents"
	"github.com/mudler/LocalAI/core/services/studio/connectors"
	"github.com/mudler/LocalAI/core/services/studio/notify"
	"github.com/mudler/LocalAI/core/services/studio/provider"
	"github.com/mudler/LocalAI/core/services/studio/store"
	"github.com/mudler/LocalAI/core/services/studio/studiotest"
	"github.com/mudler/LocalAI/pkg/addons/contract"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

type ownerConnectorSource struct {
	mu              sync.Mutex
	calls           []contract.ConnectorRequest
	empty           bool
	delivery        string
	response        contract.ConnectorResponse
	callError       error
	block           bool
	deadlines       []time.Duration
	unavailable     bool
	callUnavailable bool
	entered         chan struct{}
	exited          chan struct{}
}

func (s *ownerConnectorSource) ConnectorKinds(context.Context) ([]addons.ConnectorKind, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.unavailable {
		return nil, gateway.ErrUnavailable
	}
	if s.empty {
		return nil, nil
	}
	delivery := s.delivery
	if delivery == "" {
		delivery = contract.DeliveryPush
	}
	return []addons.ConnectorKind{{AddonID: "slack", ConnectorKind: contract.ConnectorKind{ID: "slack", Title: "Slack", Delivery: delivery}}}, nil
}
func (s *ownerConnectorSource) Connector(ctx context.Context, addon string, req contract.ConnectorRequest) (contract.ConnectorResponse, error) {
	Expect(addon).To(Equal("slack"))
	s.mu.Lock()
	s.calls = append(s.calls, req)
	if d, ok := ctx.Deadline(); ok {
		s.deadlines = append(s.deadlines, time.Until(d))
	}
	block, callError, response, unavailable := s.block, s.callError, s.response, s.callUnavailable
	entered, exited := s.entered, s.exited
	s.mu.Unlock()
	// The identity read of a binding (op me) is quick and answers at once. It
	// is not the held call that a spec counts with entered and exited.
	if req.Op == contract.ConnectorOpMe {
		block, entered, exited = false, nil, nil
	}
	if exited != nil {
		defer func() { exited <- struct{}{} }()
	}
	if entered != nil {
		entered <- struct{}{}
	}
	if block {
		<-ctx.Done()
		return contract.ConnectorResponse{}, ctx.Err()
	}
	if callError != nil {
		return contract.ConnectorResponse{}, callError
	}
	if response.Error != nil {
		return response, nil
	}
	if unavailable {
		return contract.ConnectorResponse{}, gateway.ErrUnavailable
	}
	if req.Token == "refused" {
		return contract.ConnectorResponse{Error: &contract.ConnectorError{Code: contract.ConnectorUnauthorized}}, nil
	}
	return contract.ConnectorResponse{BotID: "bot", BotUsername: "owner-bot"}, nil
}

func (s *ownerConnectorSource) configure(fn func()) {
	s.mu.Lock()
	defer s.mu.Unlock()
	fn()
}

func (s *ownerConnectorSource) requests() []contract.ConnectorRequest {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]contract.ConnectorRequest(nil), s.calls...)
}

var _ = Describe("connector owner routing", func() {
	It("reports healthy empty connector kinds when add-ons are explicitly disabled", func() {
		app := &Application{applicationConfig: &config.ApplicationConfig{}}
		Expect(app.startAddons()).To(Succeed())
		Expect(app.addonGateway).To(BeNil())
		kinds, err := (connectorTransport{app}).Kinds(context.Background())
		Expect(err).NotTo(HaveOccurred())
		Expect(kinds).To(BeEmpty())
	})

	It("refuses a connector with the real transport when add-ons are disabled, and keeps nothing", func() {
		ctx := context.Background()
		app := &Application{applicationConfig: &config.ApplicationConfig{}}
		Expect(app.startAddons()).To(Succeed())
		tr := connectorTransport{app}
		chatStore, err := store.NewJSON("")
		Expect(err).NotTo(HaveOccurred())
		agentStore, err := agents.NewJSON("")
		Expect(err).NotTo(HaveOccurred())
		svc, err := studio.New(studio.Deps{Store: chatStore, Agents: agentStore, Provider: provider.New(), Harness: studiotest.NewGateHarness(), StateDir: GinkgoT().TempDir()})
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(func() { Expect(svc.Close(ctx)).To(Succeed()) })
		dir := GinkgoT().TempDir()
		bindings, err := connectors.NewJSON(dir)
		Expect(err).NotTo(HaveOccurred())
		client, err := studio.NewConnectors(svc, studio.ConnectorOptions{Store: bindings, Transport: tr, VerifyToken: studio.VerifyTokenWithMe(tr)})
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(client.Close)
		ag, err := svc.SaveAgent(ctx, studio.AnonymousPrincipal, agents.Agent{Name: "Offline", Model: "fake"})
		Expect(err).NotTo(HaveOccurred())
		_, err = client.CreateConnector(ctx, studio.AnonymousPrincipal, studio.ConnectorInput{Kind: "telegram", Name: "Telegram", AgentID: ag.ID, Token: "offline-secret", AcceptAny: true})
		Expect(err).To(MatchError(connectors.ErrInvalid))
		reopened, err := connectors.NewJSON(dir)
		Expect(err).NotTo(HaveOccurred())
		left, err := reopened.ListBindings(ctx, studio.AnonymousPrincipal)
		Expect(err).NotTo(HaveOccurred())
		Expect(left).To(BeEmpty())
	})

	It("keeps configured missing gateways and unreachable nonowners unavailable", func() {
		ctx := context.Background()
		for _, app := range []*Application{
			{}, // A fixture without config is not an explicitly disabled install.
			{applicationConfig: &config.ApplicationConfig{Addons: config.AddonConfig{Enabled: true, InstanceID: "owner", OwnerID: "owner"}}},
			{applicationConfig: &config.ApplicationConfig{Addons: config.AddonConfig{Enabled: true, Frontend: true, InstanceID: "frontend", OwnerID: "owner"}}},
		} {
			_, err := (connectorTransport{app}).Kinds(ctx)
			Expect(err).To(MatchError(gateway.ErrUnavailable))
		}
		keys := filepath.Join(GinkgoT().TempDir(), "keys")
		Expect(gateway.GenerateCarrierKeys(keys, []string{"127.0.0.1"})).To(Succeed())
		server := httptest.NewServer(http.NotFoundHandler())
		server.Close()
		cfg, err := gateway.LoadCarrierConfig(keys, "frontend", strings.Replace(server.URL, "http://", "https://", 1))
		Expect(err).NotTo(HaveOccurred())
		carrier, err := gateway.NewCarrier(*cfg)
		Expect(err).NotTo(HaveOccurred())
		app := &Application{applicationConfig: &config.ApplicationConfig{Addons: config.AddonConfig{Enabled: true, Frontend: true}}, addonGateway: &gateway.Gateway{Forward: carrier}}
		_, err = (connectorTransport{app}).Kinds(ctx)
		Expect(err).To(MatchError(gateway.ErrUnavailable))
	})

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
		var wireMu sync.Mutex
		wireText := func() string { wireMu.Lock(); defer wireMu.Unlock(); return string(wire) }
		server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Method == http.MethodPost {
				body, readErr := io.ReadAll(r.Body)
				Expect(readErr).NotTo(HaveOccurred())
				wireMu.Lock()
				wire = body
				wireMu.Unlock()
				r.Body = io.NopCloser(bytes.NewReader(body))
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
		source.configure(func() { source.empty = true })
		kinds, err := tr.Kinds(ctx)
		Expect(err).NotTo(HaveOccurred())
		Expect(kinds).To(BeEmpty())
		source.configure(func() { source.empty = false })
		kinds, err = tr.Kinds(ctx)
		Expect(err).NotTo(HaveOccurred())
		Expect(kinds).To(HaveLen(1))
		Expect(kinds[0].ID).To(Equal("slack"))
		chatStore, err := store.NewJSON("")
		Expect(err).NotTo(HaveOccurred())
		agentStore, err := agents.NewJSON("")
		Expect(err).NotTo(HaveOccurred())
		var denyNamed atomic.Bool
		svc, err := studio.New(studio.Deps{Store: chatStore, Agents: agentStore, Provider: provider.New(), Harness: studiotest.NewGateHarness(), StateDir: GinkgoT().TempDir(), Users: func(_ context.Context, id string) (*auth.User, error) {
			if id == "named" && denyNamed.Load() {
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
			Expect(wireText()).NotTo(ContainSubstring("substituted-secret"))
			Expect(wireText()).NotTo(ContainSubstring("stored-secret"))
			Expect(source.requests()[len(source.requests())-1].Token).To(Equal("stored-secret"))
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
				before := len(source.requests())
				_, e = tr.Call(ctx, bad, contract.ConnectorRequest{Op: contract.ConnectorOpMe, Kind: bad.Kind})
				Expect(e).To(MatchError(connectors.ErrForbidden))
				Expect(source.requests()).To(HaveLen(before))
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
			Expect(source.requests()[len(source.requests())-1].Token).To(Equal("replacement"))
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
		before := len(source.requests())
		original := all[0]
		denyNamed.Store(true)
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
		Expect(source.requests()).To(HaveLen(before))
		denyNamed.Store(false)

		// Discovery succeeds, but the runtime becomes unavailable during me.
		// This is not an authority denial: complete saves and replacements remain permissive.
		source.configure(func() { source.callUnavailable = true })
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
			source.configure(func() { source.callUnavailable = false })
			_, e = tr.Call(ctx, studio.BindingConnectorScope(b), contract.ConnectorRequest{Op: contract.ConnectorOpMe, Kind: "slack"})
			Expect(e).NotTo(HaveOccurred())
			expected := "replacement"
			if id == original.ID {
				expected = "outage-replacement"
			}
			Expect(source.requests()[len(source.requests())-1].Token).To(Equal(expected))
		}
		// Discovery can succeed before the owner's connector service is initialized.
		// Unlike the explicit owner denial above, this outage must allow complete saves.
		authority := ownerApp.studioConnectors
		ownerApp.studioConnectors = nil
		before = len(source.requests())
		input.Token = "authority-outage-create"
		created, createErr = client.CreateConnector(ctx, "named", input)
		input.Token = "authority-outage-replacement"
		_, updateErr = client.UpdateConnector(ctx, "named", original.ID, input)
		ownerApp.studioConnectors = authority
		Expect(createErr).NotTo(HaveOccurred(), "authority service outage must preserve permissive create")
		Expect(updateErr).NotTo(HaveOccurred(), "authority service outage must preserve permissive token replacement")
		Expect(source.requests()).To(HaveLen(before))
		authorityOutageStore, e := connectors.NewJSON(dir)
		Expect(e).NotTo(HaveOccurred())
		authorityOutageBindings, e := authorityOutageStore.ListBindings(ctx, "named")
		Expect(e).NotTo(HaveOccurred())
		Expect(authorityOutageBindings).To(HaveLen(3))
		for _, id := range []string{created.ID, original.ID} {
			b, e := authorityOutageStore.GetBinding(ctx, id)
			Expect(e).NotTo(HaveOccurred())
			Expect(b.Token).NotTo(BeEmpty())
			Expect(b.AgentID).To(Equal(agentID))
			_, e = tr.Call(ctx, studio.BindingConnectorScope(b), contract.ConnectorRequest{Op: contract.ConnectorOpMe, Kind: "slack"})
			Expect(e).NotTo(HaveOccurred())
			expected := "authority-outage-create"
			if id == original.ID {
				expected = "authority-outage-replacement"
			}
			Expect(source.requests()[len(source.requests())-1].Token).To(Equal(expected))
		}
		// Every operation shares the same owner authority and keeps the stored token.
		b, e := bindings.GetBinding(ctx, original.ID)
		Expect(e).NotTo(HaveOccurred())
		scope := studio.BindingConnectorScope(b)
		for _, op := range []string{contract.ConnectorOpSend, contract.ConnectorOpEdit, contract.ConnectorOpTyping, contract.ConnectorOpDraft, contract.ConnectorOpHistory, contract.ConnectorOpDelete} {
			req := contract.ConnectorRequest{Op: op, Kind: "slack", Chat: "chat", Thread: "thread", Text: "private text", MessageID: "working", DraftID: 7, Media: "ref"}
			before = len(source.requests())
			_, e = tr.Call(ctx, scope, req)
			Expect(e).NotTo(HaveOccurred())
			req.Token = "authority-outage-replacement"
			Expect(source.requests()[before:]).To(Equal([]contract.ConnectorRequest{req}))
			Expect(wireText()).NotTo(ContainSubstring(req.Token))
		}
		// File bytes only travel by handle: a media or send_file call without one is refused.
		before = len(source.requests())
		for _, op := range []string{contract.ConnectorOpMedia, contract.ConnectorOpSendFile} {
			_, e = tr.Call(ctx, scope, contract.ConnectorRequest{Op: op, Kind: "slack", Media: "ref", Chat: "chat"})
			Expect(e).To(MatchError(connectors.ErrForbidden), op)
		}
		Expect(source.requests()).To(HaveLen(before))
		source.configure(func() { source.response = contract.ConnectorResponse{} })
		for _, code := range []string{contract.ConnectorUnauthorized, contract.ConnectorConflict, contract.ConnectorRateLimited} {
			source.configure(func() { source.response.Error = &contract.ConnectorError{Code: code, RetryAfter: 17} })
			out, e := tr.Call(ctx, scope, contract.ConnectorRequest{Op: contract.ConnectorOpSend, Kind: "slack", Chat: "chat", Text: "private text"})
			Expect(e).NotTo(HaveOccurred())
			Expect(out.Error).To(Equal(source.response.Error))
		}
		source.configure(func() { source.response = contract.ConnectorResponse{} })
		source.configure(func() { source.callError = errors.New("secret-token private text raw carrier envelope") })
		before = len(source.requests())
		_, e = tr.Call(ctx, scope, contract.ConnectorRequest{Op: contract.ConnectorOpSend, Kind: "slack", Chat: "chat", Text: "private text"})
		Expect(e).To(MatchError(gateway.ErrUnavailable))
		Expect(e.Error()).NotTo(ContainSubstring("private"))
		Expect(source.requests()).To(HaveLen(before+1), "an uncertain send must not be replayed")
		source.configure(func() { source.callError = nil })
		// Pull authorization rejects disabled bindings but explicit tests still work.
		source.configure(func() { source.delivery = contract.DeliveryPull })
		_, e = client.SetConnectorEnabled(ctx, "named", b.ID, false)
		Expect(e).NotTo(HaveOccurred())
		before = len(source.requests())
		_, e = tr.Call(ctx, scope, contract.ConnectorRequest{Op: contract.ConnectorOpPoll, Kind: "slack"})
		Expect(e).To(MatchError(connectors.ErrForbidden))
		Expect(source.requests()).To(HaveLen(before))
		_, e = client.TestConnector(ctx, "named", b.ID)
		Expect(e).NotTo(HaveOccurred())
		// A runtime-free poll leader reaches the owner; a nonleader still forwards notices.
		_, e = client.SetConnectorEnabled(ctx, "named", b.ID, true)
		Expect(e).NotTo(HaveOccurred())
		enabled, e := bindings.EnabledBindings(ctx)
		Expect(e).NotTo(HaveOccurred())
		entered, exited := make(chan struct{}, len(enabled)), make(chan struct{}, len(enabled))
		source.configure(func() { source.block = true; source.entered = entered; source.exited = exited })
		leader, e := studio.NewConnectors(svc, studio.ConnectorOptions{Store: bindings, Transport: tr, Lead: func(context.Context) bool { return true }})
		Expect(e).NotTo(HaveOccurred())
		before = len(source.requests())
		leader.Sync(ctx)
		for range enabled {
			Eventually(entered, 5*time.Second).Should(Receive())
		}
		leader.Close()
		for range enabled {
			Eventually(exited, 5*time.Second).Should(Receive())
		}
		source.configure(func() { source.block = false; source.entered = nil; source.exited = nil })
		// A binding without a stored account is first asked who the bot is (op
		// me), then polled. Both reach the owner, and nothing else does.
		var ops []string
		for _, r := range source.requests()[before:] {
			ops = append(ops, r.Op)
		}
		Expect(ops).To(ContainElement(contract.ConnectorOpPoll))
		Expect(ops).To(HaveEach(Or(Equal(contract.ConnectorOpPoll), Equal(contract.ConnectorOpMe))))
		input.Token = ""
		input.NotifyChat = "C0123456"
		_, e = client.UpdateConnector(ctx, "named", b.ID, input)
		Expect(e).NotTo(HaveOccurred())
		nonleader, e := studio.NewConnectors(svc, studio.ConnectorOptions{Store: bindings, Transport: tr, Lead: func(context.Context) bool { return false }})
		Expect(e).NotTo(HaveOccurred())
		before = len(source.requests())
		nonleader.Sync(ctx)
		Expect(source.requests()).To(HaveLen(before))
		nonleader.Forward(ctx, notify.Notification{Principal: "named", Title: "Notice", Body: "Body"})
		nonleader.Close()
		Expect(source.requests()).To(HaveLen(before + 1))
		Expect(source.requests()[before].Chat).To(Equal("C0123456"))
		entered, exited = make(chan struct{}, 1), make(chan struct{}, 1)
		source.configure(func() { source.block = true; source.entered = entered; source.exited = exited })
		short, cancel := context.WithCancel(ctx)
		defer cancel()
		before = len(source.requests())
		result := make(chan error, 1)
		go func() {
			_, callErr := tr.Call(short, scope, contract.ConnectorRequest{Op: contract.ConnectorOpSend, Kind: "slack", Chat: "chat"})
			result <- callErr
		}()
		Eventually(entered, 5*time.Second).Should(Receive())
		cancel()
		Eventually(result, 5*time.Second).Should(Receive(HaveOccurred()))
		Eventually(exited, 5*time.Second).Should(Receive())
		Expect(source.requests()).To(HaveLen(before + 1))
		source.mu.Lock()
		Expect(source.deadlines).NotTo(BeEmpty())
		for _, d := range source.deadlines {
			Expect(d).To(BeNumerically("<=", 45*time.Second))
		}
		source.mu.Unlock()
		source.configure(func() { source.block = false; source.entered = nil; source.exited = nil })
		// Oversized outgoing envelopes are refused before dispatch.
		before = len(source.requests())
		_, e = tr.Call(ctx, scope, contract.ConnectorRequest{Op: contract.ConnectorOpSend, Kind: "slack", Chat: "chat", Text: strings.Repeat("x", 2<<20)})
		Expect(e).To(MatchError(connectors.ErrForbidden))
		Expect(source.requests()).To(HaveLen(before))
		// Unknown named users are refused during both prospective and replacement
		// validation, even when an agent or stored binding exists for that identity.
		ag, e := svc.SaveAgent(ctx, "missing-user", agents.Agent{Name: "Orphan", Model: "fake"})
		Expect(e).NotTo(HaveOccurred())
		s := studio.ConnectorCallScope{Purpose: studio.ConnectorPurposeVerifyToken, Principal: "missing-user", AgentID: ag.ID, AddonID: "slack", Kind: "slack"}
		_, e = tr.Call(ctx, s, contract.ConnectorRequest{Op: contract.ConnectorOpMe, Kind: "slack", Token: "new"})
		Expect(e).To(MatchError(connectors.ErrForbidden))
		beforeDiscoveryStore, e := connectors.NewJSON(dir)
		Expect(e).NotTo(HaveOccurred())
		beforeDiscoveryBindings, e := beforeDiscoveryStore.AllBindings(ctx)
		Expect(e).NotTo(HaveOccurred())
		before = len(source.requests())
		source.configure(func() { source.unavailable = true })
		_, e = tr.Kinds(ctx)
		Expect(e).To(MatchError(gateway.ErrUnavailable))
		_, e = client.CreateConnector(ctx, "named", studio.ConnectorInput{Kind: "slack", Name: "Discovery outage", AgentID: agentID, Token: "must-not-be-persisted", AcceptAny: true})
		Expect(e).To(HaveOccurred(), "failed discovery must stop create before token verification or persistence")
		Expect(source.requests()).To(HaveLen(before))
		afterDiscoveryStore, e := connectors.NewJSON(dir)
		Expect(e).NotTo(HaveOccurred())
		afterDiscoveryBindings, e := afterDiscoveryStore.AllBindings(ctx)
		Expect(e).NotTo(HaveOccurred())
		Expect(afterDiscoveryBindings).To(Equal(beforeDiscoveryBindings), "reopening the JSON store must find no partial binding or token change")
		source.configure(func() { source.unavailable = false })
		// A different certificate signed by another keyset is not a pinned peer.
		alienKeys := filepath.Join(GinkgoT().TempDir(), "keys")
		Expect(gateway.GenerateCarrierKeys(alienKeys, []string{"127.0.0.1"})).To(Succeed())
		alienCfg, e := gateway.LoadCarrierConfig(alienKeys, "frontend", server.URL)
		Expect(e).NotTo(HaveOccurred())
		alienCfg.Roots = fc.Roots
		alienCfg.Pins = fc.Pins
		alien, e := gateway.NewCarrier(*alienCfg)
		Expect(e).NotTo(HaveOccurred())
		before = len(source.requests())
		_, e = (&gateway.Gateway{Forward: alien}).ConnectorCall(ctx, s, contract.ConnectorRequest{Op: contract.ConnectorOpMe, Kind: "slack", Token: "new"})
		Expect(e).To(HaveOccurred())
		Expect(source.requests()).To(HaveLen(before))
		// Exercise bounded decoding through the authenticated handler, not a mock.
		payload, _ := json.Marshal(map[string]any{"scope": s, "request": contract.ConnectorRequest{Op: contract.ConnectorOpMe, Kind: "slack", Token: "new"}})
		transport := &http.Transport{TLSClientConfig: &tls.Config{Certificates: []tls.Certificate{fc.Certificate}, RootCAs: fc.Roots, MinVersion: tls.VersionTLS12}}
		defer transport.CloseIdleConnections()
		httpClient := &http.Client{Transport: transport}
		resp, e := httpClient.Post(server.URL+"/addons/connectors/call", "application/json", strings.NewReader(string(payload)+strings.Repeat(" ", 2<<20)))
		Expect(e).NotTo(HaveOccurred())
		defer func() { _ = resp.Body.Close() }()
		Expect(resp.StatusCode).To(Equal(http.StatusForbidden))
	})
})
