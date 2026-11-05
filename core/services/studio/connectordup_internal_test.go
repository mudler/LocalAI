package studio

import (
	"context"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/mudler/LocalAI/core/services/studio/connectors"
	"github.com/mudler/LocalAI/pkg/addons/contract"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// dupAddon is a fake connector add-on: it answers the op me with an account
// by token, and keeps count of the calls.
type dupAddon struct {
	mu       sync.Mutex
	accounts map[string]string // by token; a token that is absent is refused
	silent   bool              // answers me without an account, like an older add-on
	meCalls  int
}

func (a *dupAddon) Kinds(context.Context) ([]ConnectorKind, error) {
	return []ConnectorKind{{AddonID: "one", ConnectorKind: contract.ConnectorKind{ID: "chat", Title: "Chat", Delivery: contract.DeliveryPush}}}, nil
}

func (a *dupAddon) Call(_ context.Context, _ ConnectorCallScope, req contract.ConnectorRequest) (contract.ConnectorResponse, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if req.Op != contract.ConnectorOpMe {
		return contract.ConnectorResponse{}, nil
	}
	a.meCalls++
	acct, ok := a.accounts[req.Token]
	if !ok {
		return contract.ConnectorResponse{Error: &contract.ConnectorError{Code: contract.ConnectorUnauthorized}}, nil
	}
	if a.silent {
		return contract.ConnectorResponse{BotID: "bot"}, nil
	}
	return contract.ConnectorResponse{BotID: "bot", AccountID: acct}, nil
}

func (a *dupAddon) calls() int { a.mu.Lock(); defer a.mu.Unlock(); return a.meCalls }

// dupClock is an injected clock.
type dupClock struct {
	mu sync.Mutex
	t  time.Time
}

func (c *dupClock) Now() time.Time { c.mu.Lock(); defer c.mu.Unlock(); return c.t }
func (c *dupClock) Advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.t = c.t.Add(d)
}

var _ = Describe("two connections of one bot", func() {
	ctx := context.Background()
	t0 := time.Date(2026, 11, 5, 9, 0, 0, 0, time.UTC)
	mk := func(id, owner, account string, age time.Duration, on bool) connectors.Binding {
		return connectors.Binding{ID: id, Owner: owner, Name: "Bot " + id, Kind: "chat", AddonID: "one", AgentID: "a1", Enabled: on, AccountID: account, Created: t0.Add(age)}
	}

	DescribeTable("finds the connections that are the same bot",
		func(a, b connectors.Binding, same bool) {
			Expect(sameBot(a, b)).To(Equal(same))
			Expect(sameBot(b, a)).To(Equal(same), "the relation is symmetric")
			if same {
				Expect(duplicatesOf([]connectors.Binding{a, b}, b)).To(HaveLen(1))
			} else {
				Expect(duplicatesOf([]connectors.Binding{a, b}, b)).To(BeEmpty())
			}
		},
		Entry("same identity, both on", mk("a", "u1", "T1/U1", 0, true), mk("b", "u1", "T1/U1", time.Hour, true), true),
		Entry("different identity", mk("a", "u1", "T1/U1", 0, true), mk("b", "u1", "T1/U2", time.Hour, true), false),
		Entry("same bot in another workspace", mk("a", "u1", "T1/U1", 0, true), mk("b", "u1", "T2/U1", time.Hour, true), false),
		Entry("identity unknown on one side", mk("a", "u1", "T1/U1", 0, true), mk("b", "u1", "", time.Hour, true), false),
		Entry("identity unknown on both sides", mk("a", "u1", "", 0, true), mk("b", "u1", "", time.Hour, true), false),
		Entry("one of them is off", mk("a", "u1", "T1/U1", 0, true), mk("b", "u1", "T1/U1", time.Hour, false), false),
		Entry("both are off", mk("a", "u1", "T1/U1", 0, false), mk("b", "u1", "T1/U1", time.Hour, false), false),
		Entry("different owner", mk("a", "u1", "T1/U1", 0, true), mk("b", "u2", "T1/U1", time.Hour, true), false),
		Entry("same account id on another kind", mk("a", "u1", "7", 0, true), func() connectors.Binding {
			b := mk("b", "u1", "7", time.Hour, true)
			b.Kind = "other"
			return b
		}(), false),
		Entry("same account id on another add-on", mk("a", "u1", "7", 0, true), func() connectors.Binding {
			b := mk("b", "u1", "7", time.Hour, true)
			b.AddonID = "two"
			return b
		}(), false),
		Entry("a credentials-only binding never answers", mk("a", "u1", "T1/U1", 0, true), func() connectors.Binding {
			b := mk("b", "u1", "T1/U1", time.Hour, true)
			b.AgentID = ""
			return b
		}(), false),
	)

	It("lists the other connections oldest first, and never the connection itself", func() {
		all := []connectors.Binding{mk("c", "u1", "X", 2*time.Hour, true), mk("a", "u1", "X", 0, true), mk("b", "u1", "X", time.Hour, true)}
		Expect(refsOf(duplicatesOf(all, all[0]))).To(Equal([]DuplicateRef{{ID: "a", Name: "Bot a"}, {ID: "b", Name: "Bot b"}}))
		Expect(duplicatesOf(all[:1], all[0])).To(BeEmpty())
	})

	Describe("the answer to one message", func() {
		var (
			addon *dupAddon
			clock *dupClock
			st    connectors.Store
			fe    []*Connectors // two frontends over one store
			logs  *strings.Builder
		)
		// put stores a binding as a frontend would have saved it.
		put := func(b connectors.Binding, token string) {
			s, err := fe[0].sealerFor(ctx)
			Expect(err).NotTo(HaveOccurred())
			b.Token, err = s.Seal(connectors.AAD(b.ID, b.Owner), []byte(token))
			Expect(err).NotTo(HaveOccurred())
			Expect(st.PutBinding(ctx, b)).To(Succeed())
		}
		// message is what both add-ons receive for one post of the platform.
		message := func(id string) contract.ConnectorMessage {
			return contract.ConnectorMessage{Chat: "C1", Thread: "t1", Mention: true, MessageID: id, SenderID: "U9", SenderName: "Sam", Text: "hello"}
		}
		// seen says whether the binding took the message up to the sender log.
		seen := func(id string) bool {
			s, err := st.Senders(ctx, id, 0)
			Expect(err).NotTo(HaveOccurred())
			return len(s) > 0
		}
		BeforeEach(func() {
			addon = &dupAddon{accounts: map[string]string{"old": "T1/U1", "new": "T1/U1", "other": "T1/U2"}}
			clock = &dupClock{t: t0.Add(24 * time.Hour)}
			st, _ = connectors.NewJSON("")
			logs = &strings.Builder{}
			fe = nil
			for range 2 {
				svc, _ := newSkillService(&skillBroker{})
				c, err := NewConnectors(svc, ConnectorOptions{Store: st, Transport: addon, Clock: clock.Now, Logger: slog.New(slog.NewTextHandler(logs, nil))})
				Expect(err).NotTo(HaveOccurred())
				DeferCleanup(c.Close)
				fe = append(fe, c)
			}
		})

		It("is taken by the oldest enabled connection only, on every frontend", func() {
			put(mk("old", "u1", "T1/U1", 0, true), "old")
			put(mk("new", "u1", "T1/U1", time.Hour, true), "new")
			for i, c := range fe {
				id := "m" + string(rune('1'+i))
				Expect(c.Ingest(ctx, "one", contract.ConnectorInbound{BindingID: "new", ConnectorMessage: message(id)})).To(Succeed())
				Expect(c.Ingest(ctx, "one", contract.ConnectorInbound{BindingID: "old", ConnectorMessage: message(id)})).To(Succeed())
			}
			Expect(seen("old")).To(BeTrue(), "the older connection takes the message")
			Expect(seen("new")).To(BeFalse(), "the younger one stays silent, whichever frontend got the message")
			Expect(strings.Count(logs.String(), "stays silent")).To(Equal(2), "one line for each message")
		})

		It("lets the younger connection answer when the older one is switched off", func() {
			old := mk("old", "u1", "T1/U1", 0, false)
			put(old, "old")
			put(mk("new", "u1", "T1/U1", time.Hour, true), "new")
			Expect(fe[1].Ingest(ctx, "one", contract.ConnectorInbound{BindingID: "new", ConnectorMessage: message("m1")})).To(Succeed())
			Expect(seen("new")).To(BeTrue())
		})

		It("leaves a connection of another identity, of another owner and of an unknown identity alone", func() {
			put(mk("old", "u1", "T1/U1", 0, true), "old")
			put(mk("diff", "u1", "T1/U2", time.Hour, true), "other")
			put(mk("alien", "u2", "T1/U1", time.Hour, true), "new")
			put(mk("blank", "u1", "", time.Hour, true), "") // no token to ask with: stays unknown
			for _, id := range []string{"old", "diff", "alien", "blank"} {
				Expect(fe[0].Ingest(ctx, "one", contract.ConnectorInbound{BindingID: id, ConnectorMessage: message("m1")})).To(Succeed())
			}
			for _, id := range []string{"old", "diff", "alien"} {
				Expect(seen(id)).To(BeTrue(), id)
			}
		})

		It("tells the oldest connection by the clock of the store, whatever the order of creation in the list", func() {
			// The youngest ID sorts first: the age decides, not the name.
			put(mk("a-young", "u1", "T1/U1", time.Hour, true), "new")
			put(mk("z-old", "u1", "T1/U1", 0, true), "old")
			Expect(fe[0].Ingest(ctx, "one", contract.ConnectorInbound{BindingID: "a-young", ConnectorMessage: message("m1")})).To(Succeed())
			Expect(fe[1].Ingest(ctx, "one", contract.ConnectorInbound{BindingID: "z-old", ConnectorMessage: message("m1")})).To(Succeed())
			Expect(seen("z-old")).To(BeTrue())
			Expect(seen("a-young")).To(BeFalse())
		})

		It("takes a message that has no platform ID as it always did", func() {
			put(mk("old", "u1", "T1/U1", 0, true), "old")
			put(mk("new", "u1", "T1/U1", time.Hour, true), "new")
			m := message("")
			Expect(fe[0].Ingest(ctx, "one", contract.ConnectorInbound{BindingID: "new", ConnectorMessage: m})).To(Succeed())
			Expect(seen("new")).To(BeTrue(), "without the ID the two connections cannot be told to hold the same message")
		})

		It("learns the account of a stored connection once, and asks an add-on that does not tell only now and then", func() {
			put(mk("old", "u1", "", 0, true), "old")
			Expect(fe[0].Ingest(ctx, "one", contract.ConnectorInbound{BindingID: "old", ConnectorMessage: message("m1")})).To(Succeed())
			b, _ := st.GetBinding(ctx, "old")
			Expect(b.AccountID).To(Equal("T1/U1"))
			Expect(addon.calls()).To(Equal(1))
			Expect(fe[0].Ingest(ctx, "one", contract.ConnectorInbound{BindingID: "old", ConnectorMessage: message("m2")})).To(Succeed())
			Expect(addon.calls()).To(Equal(1), "a known account is not asked again")

			addon.silent = true
			put(mk("older", "u1", "", 0, true), "old")
			for i := range 3 {
				Expect(fe[0].Ingest(ctx, "one", contract.ConnectorInbound{BindingID: "older", ConnectorMessage: message("s" + string(rune('0'+i)))})).To(Succeed())
			}
			Expect(addon.calls()).To(Equal(2), "an add-on that tells nothing is asked once inside the retry time")
			clock.Advance(accountRetry + time.Second)
			Expect(fe[0].Ingest(ctx, "one", contract.ConnectorInbound{BindingID: "older", ConnectorMessage: message("s9")})).To(Succeed())
			Expect(addon.calls()).To(Equal(3), "and again after it")
			b, _ = st.GetBinding(ctx, "older")
			Expect(b.AccountID).To(BeEmpty(), "no identity is made up for an add-on that does not tell")
		})
	})

	DescribeTable("asks for a confirmation when a save makes a connection a duplicate",
		func(old *connectors.Binding, b connectors.Binding, mine []connectors.Binding, in ConnectorInput, wantOther string) {
			err := checkDuplicate(old, b, mine, in)
			if wantOther == "" {
				Expect(err).NotTo(HaveOccurred())
				return
			}
			var d *DuplicateError
			Expect(err).To(MatchError(ErrDuplicateConnection))
			Expect(err).To(BeAssignableToTypeOf(d))
			Expect(err.(*DuplicateError).Other.Name).To(Equal(wantOther))
			Expect(err.Error()).To(Equal("This bot is also connected as " + wantOther + ". Both connections will answer the same message."))
		},
		Entry("a new twin", nil, mk("n", "u1", "X", time.Hour, true), []connectors.Binding{mk("a", "u1", "X", 0, true)}, ConnectorInput{}, "Bot a"),
		Entry("a new twin, confirmed", nil, mk("n", "u1", "X", time.Hour, true), []connectors.Binding{mk("a", "u1", "X", 0, true)}, ConnectorInput{ConfirmDuplicate: DuplicateConfirmation}, ""),
		Entry("a new twin that is off", nil, mk("n", "u1", "X", time.Hour, false), []connectors.Binding{mk("a", "u1", "X", 0, true)}, ConnectorInput{}, ""),
		Entry("another bot", nil, mk("n", "u1", "Y", time.Hour, true), []connectors.Binding{mk("a", "u1", "X", 0, true)}, ConnectorInput{}, ""),
		Entry("an unknown identity", nil, mk("n", "u1", "", time.Hour, true), []connectors.Binding{mk("a", "u1", "", 0, true)}, ConnectorInput{}, ""),
		Entry("a connection that was a twin already is not asked again", func() *connectors.Binding { b := mk("n", "u1", "X", time.Hour, true); return &b }(),
			mk("n", "u1", "X", time.Hour, true), []connectors.Binding{mk("a", "u1", "X", 0, true), mk("n", "u1", "X", time.Hour, true)}, ConnectorInput{}, ""),
		Entry("a switched-off connection that is saved on", func() *connectors.Binding { b := mk("n", "u1", "X", time.Hour, false); return &b }(),
			mk("n", "u1", "X", time.Hour, true), []connectors.Binding{mk("a", "u1", "X", 0, true), mk("n", "u1", "X", time.Hour, false)}, ConnectorInput{}, "Bot a"),
	)
})
