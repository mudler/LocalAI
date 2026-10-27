package studio_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/mudler/LocalAI/core/services/addons/hostops"
	"github.com/mudler/LocalAI/core/services/studio"
	"github.com/mudler/LocalAI/core/services/studio/agents"
	"github.com/mudler/LocalAI/core/services/studio/connectors"
	"github.com/mudler/LocalAI/pkg/addons/contract"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// specT is the testing.T of the suite. The rigs of this package take one, and
// their cleanup runs when the suite ends.
var specT *testing.T

func TestConnectorSpecs(t *testing.T) {
	specT = t
	RegisterFailHandler(Fail)
	RunSpecs(t, "Chats connector add-on path")
}

const incarnation = "credential-of-this-incarnation"

var _ = Describe("a connector add-on pushing a message", func() {
	var (
		r      *connRig
		b      connectors.Binding
		broker http.Handler
		st     connectors.Store
	)
	ctx := context.Background()
	msg := func(sender, text string) contract.ConnectorMessage {
		return contract.ConnectorMessage{Chat: sender, Direct: true, SenderID: sender, SenderName: "S" + sender, Text: text}
	}
	// push is the add-on side: one POST to the host operation channel.
	push := func(credential string, args map[string]any) (int, string) {
		raw, _ := json.Marshal(contract.HostRequest{OperationID: "u1", Op: contract.ConnectorInboundOp, Arguments: rawArgs(args)})
		req := httptest.NewRequest(http.MethodPost, "/host", bytes.NewReader(raw))
		req.Header.Set(contract.HostCredentialHeader, credential)
		rec := httptest.NewRecorder()
		broker.ServeHTTP(rec, req)
		return rec.Code, rec.Body.String()
	}
	inbound := func(m contract.ConnectorMessage) map[string]any {
		raw, _ := json.Marshal(contract.ConnectorInbound{BindingID: b.ID, ConnectorMessage: m})
		var out map[string]any
		Expect(json.Unmarshal(raw, &out)).To(Succeed())
		return out
	}
	chatsOf := func(user string) int {
		l, err := r.svc.List(ctx, user, 20)
		Expect(err).NotTo(HaveOccurred())
		return len(l)
	}

	BeforeEach(func() {
		sh, bot := newShared(specT), newScriptedBot(specT)
		st = connStore(specT)
		r = newConnRig(specT, sh, st, bot, connSay("pushed answer"), nil)
		// A push-only add-on: stop the pull loop so the host operation is the only door.
		r.tr.Running = true
		b = r.bind(specT, studio.ConnectorInput{Allow: []string{"11"}})
		inbox := hostops.New(hostops.NewInFlight(), func(string, string) bool { return false }, contract.DefaultLimits())
		Expect(hostops.RegisterConnector(inbox, r.conns)).To(Succeed())
		broker = inbox.Handler("telegram", incarnation, []string{"connector."})
	})

	It("runs a turn as the owner of the binding for an allowed sender", func() {
		code, body := push(incarnation, inbound(msg("11", "hello from outside")))
		Expect(code).To(Equal(http.StatusOK), body)
		Eventually(func() int { return len(r.bot.answers()) }, "10s").Should(Equal(1))
		Expect(r.bot.answers()[0]).To(Equal("pushed answer"))
		chats, _ := r.svc.List(ctx, alice, 20)
		Expect(chats).To(HaveLen(1))
		Expect(chats[0].OriginID).To(Equal(b.ID))
	})

	It("refuses an add-on that is not the one of the binding, with no turn and no log", func() {
		err := r.conns.Ingest(ctx, "another-addon", contract.ConnectorInbound{BindingID: b.ID, ConnectorMessage: msg("11", "let me in")})
		Expect(err).To(MatchError(connectors.ErrForbidden))
		Expect(r.conns.Ingest(ctx, "", contract.ConnectorInbound{BindingID: b.ID, ConnectorMessage: msg("11", "x")})).To(MatchError(connectors.ErrForbidden))
		Expect(chatsOf(alice)).To(BeZero())
		senders, _ := st.Senders(ctx, b.ID, 0)
		Expect(senders).To(BeEmpty())
		Expect(r.bot.answers()).To(BeEmpty())
	})

	It("cannot choose the actor: a user in the arguments is refused, and the turn runs as the owner", func() {
		for _, key := range []string{"userId", "user", "actor", "owner", "mode", "addonId"} {
			args := inbound(msg("11", "as someone else"))
			args[key] = "bob"
			code, _ := push(incarnation, args)
			Expect(code).To(Equal(http.StatusBadRequest), key)
		}
		Expect(chatsOf(alice)).To(BeZero(), "a refused call must not run a turn")
		Expect(chatsOf("bob")).To(BeZero())
		// A name the broker does not know is refused too, never read.
		args := inbound(msg("11", "x"))
		args["runAs"] = "bob"
		code, _ := push(incarnation, args)
		Expect(code).To(Equal(http.StatusBadRequest))
		// The accepted call runs as the owner of the stored binding.
		code, _ = push(incarnation, inbound(msg("11", "hi")))
		Expect(code).To(Equal(http.StatusOK))
		Eventually(func() int { return chatsOf(alice) }, "10s").Should(Equal(1))
		Expect(chatsOf("bob")).To(BeZero())
	})

	It("is refused without the live credential of the incarnation", func() {
		for _, c := range []string{"", "credential-of-an-old-incarnation", "credential-of-another-addon"} {
			code, _ := push(c, inbound(msg("11", "hi")))
			Expect(code).To(Equal(http.StatusUnauthorized), "credential %q", c)
		}
		Expect(chatsOf(alice)).To(BeZero())
		senders, _ := st.Senders(ctx, b.ID, 0)
		Expect(senders).To(BeEmpty())
	})

	It("refuses a binding that is switched off", func() {
		_, err := r.conns.SetConnectorEnabled(ctx, alice, b.ID, false)
		Expect(err).NotTo(HaveOccurred())
		code, _ := push(incarnation, inbound(msg("11", "hi")))
		Expect(code).To(Equal(http.StatusForbidden))
		Expect(chatsOf(alice)).To(BeZero())
	})

	It("refuses a binding that does not exist the same way as one of another add-on", func() {
		args := inbound(msg("11", "hi"))
		args["bindingId"] = "missing"
		code, _ := push(incarnation, args)
		Expect(code).To(Equal(http.StatusForbidden))
	})

	It("keeps the allowlist and writes the sender log", func() {
		code, _ := push(incarnation, inbound(msg("99", "a stranger")))
		Expect(code).To(Equal(http.StatusOK))
		group := msg("11", "an allowed person in a group")
		group.Direct = false
		code, _ = push(incarnation, inbound(group))
		Expect(code).To(Equal(http.StatusOK))
		senders, err := st.Senders(ctx, b.ID, 0)
		Expect(err).NotTo(HaveOccurred())
		byID := map[string]connectors.Sender{}
		for _, s := range senders {
			byID[s.ID] = s
		}
		Expect(byID).To(HaveKey("99"))
		Expect(byID["99"].Allowed).To(BeFalse())
		Expect(byID["99"].Name).To(Equal("S99"))
		Expect(byID["11"].Allowed).To(BeFalse(), "a group message is never accepted")
		Expect(chatsOf(alice)).To(BeZero())
		Expect(r.bot.answers()).To(BeEmpty())
	})

	It("refuses a message with no sender or no chat", func() {
		bad := msg("11", "x")
		bad.SenderID = ""
		Expect(r.conns.Ingest(ctx, "telegram", contract.ConnectorInbound{BindingID: b.ID, ConnectorMessage: bad})).To(MatchError(connectors.ErrInvalid))
	})
})

func rawArgs(v any) json.RawMessage {
	raw, err := json.Marshal(v)
	Expect(err).NotTo(HaveOccurred())
	return raw
}

var _ = Describe("the pull loop of a connector add-on", func() {
	ctx := context.Background()

	It("moves the stored offset to what the poll answer names, also past updates it skips", func() {
		sh, bot := newShared(specT), newScriptedBot(specT)
		st := connStore(specT)
		r := newConnRig(specT, sh, st, bot, connSay("ok"), nil)
		b := r.bind(specT, studio.ConnectorInput{AcceptAny: true})
		bot.mu.Lock()
		bot.updates = append(bot.updates, map[string]any{"update_id": 7, "edited_message": map[string]any{"text": "an edit"}})
		bot.mu.Unlock()
		Eventually(func() int64 { off, _, _ := st.Offset(ctx, b.ID); return off }, "10s").Should(Equal(int64(8)))
		Expect(bot.answers()).To(BeEmpty(), "an edit is no message")
		// The next poll asks from the stored offset.
		Eventually(func() []int64 { bot.mu.Lock(); defer bot.mu.Unlock(); return append([]int64(nil), bot.offsets...) }, "10s").Should(ContainElement(int64(8)))
	})
})

var _ = Describe("a binding made before connectors became add-ons", func() {
	ctx := context.Background()

	It("shows as inactive with a plain reason while the add-on is missing, and works once it runs", func() {
		sh, bot := newShared(specT), newScriptedBot(specT)
		st := connStore(specT)
		r := newConnRig(specT, sh, st, bot, connSay("answered after the install"), nil)
		r.tr.Running = false
		b := r.bind(specT, studio.ConnectorInput{AcceptAny: true})
		// What the old core stored: a telegram binding that names no add-on.
		old, err := st.GetBinding(ctx, b.ID)
		Expect(err).NotTo(HaveOccurred())
		old.AddonID = ""
		Expect(st.PutBinding(ctx, old)).To(Succeed())

		list, err := r.conns.ListConnectors(ctx, alice)
		Expect(err).NotTo(HaveOccurred())
		Expect(list).To(HaveLen(1))
		Expect(list[0].AddonID).To(Equal("telegram"))
		Expect(list[0].Active).To(BeFalse())
		Expect(list[0].Reason).To(Equal("install the Telegram add-on"))
		Consistently(func() int { return bot.pollCount() }, "300ms").Should(BeZero(), "no add-on, no poll")

		r.tr.Running = true
		r.conns.Notify()
		bot.say(1, 5, "A", "", "hi")
		Eventually(func() int { return len(bot.answers()) }, "10s").Should(Equal(1))
		again, _ := r.conns.GetConnector(ctx, alice, b.ID)
		Expect(again.Active).To(BeTrue())
		Expect(again.Reason).To(BeEmpty())
	})

	It("lets a client older than the add-ons create a telegram binding, and refuses an unknown kind", func() {
		sh, bot := newShared(specT), newScriptedBot(specT)
		r := newConnRig(specT, sh, connStore(specT), bot, connSay("ok"), nil)
		r.tr.Running = false
		ag, err := r.svc.SaveAgent(ctx, alice, agents.Agent{Name: "Support", Model: "m"})
		Expect(err).NotTo(HaveOccurred())
		made, err := r.conns.CreateConnector(ctx, alice, studio.ConnectorInput{Name: "Old client", Token: "1:t", AgentID: ag.ID})
		Expect(err).NotTo(HaveOccurred())
		Expect(made.Kind).To(Equal("telegram"))
		Expect(made.AddonID).To(Equal("telegram"))
		Expect(made.Active).To(BeFalse())
		_, err = r.conns.CreateConnector(ctx, alice, studio.ConnectorInput{Kind: "carrier-pigeon", Name: "x", Token: "1:t", AgentID: ag.ID})
		Expect(err).To(MatchError(connectors.ErrInvalid))
	})
})
