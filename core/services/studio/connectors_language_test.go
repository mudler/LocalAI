package studio_test

import (
	"context"
	"strings"
	"time"

	"github.com/mudler/LocalAI/core/services/studio"
	"github.com/mudler/LocalAI/core/services/studio/agents"
	"github.com/mudler/LocalAI/core/services/studio/connectors"
	"github.com/mudler/LocalAI/core/services/studio/hostmsg"
	"github.com/mudler/LocalAI/core/services/studio/studiotest"
	"github.com/mudler/LocalAI/core/services/studio/workspace"
	"github.com/mudler/LocalAI/pkg/addons/contract"
	"github.com/mudler/LocalAI/tests/studiobot"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// The sentences that the host writes in a chat come in the language of the
// person: the reply language of the connection, or with auto the language of
// the sender's messages, which a short reply such as "ok" does not change.
var _ = Describe("the language of the host sentences in a chat", func() {
	ctx := context.Background()
	const (
		chatID = "C1"
		thread = "T1"
	)
	var (
		sh   *shared
		fake *postAddon
		gate *capSwitch
		r    *connRig
		b    connectors.Binding
		seq  int
	)
	script := func(_ workspace.Actor, text string) []studiotest.ToolStep {
		if strings.HasSuffix(text, "propose") {
			return []studiotest.ToolStep{{Tool: "propose_trigger", Args: `{"name":"Report","prompt":"RUN write it","when":"daily 08:00","time_zone":"UTC","delivery":{"target":"origin"}}`}, {Say: "proposed"}}
		}
		return []studiotest.ToolStep{{Say: "ok"}}
	}
	unpaired := false
	BeforeEach(func() { unpaired = false })
	setup := func(lang *string, opt func(*studio.ConnectorOptions)) {
		sh, fake, gate = newShared(), &postAddon{}, &capSwitch{}
		tr := studiobot.NewTransport(studiobot.Addon{ID: "chat-addon", Kind: contract.ConnectorKind{ID: "chatty", Title: "Chatty", Delivery: contract.DeliveryPull}, Runtime: fake})
		r = newConnRigOpts(sh, connStore(), tr, script, nil, func(o *studio.ConnectorOptions) {
			o.OutboxPoll = 10 * time.Millisecond
			o.RatePerMinute = 100
			if opt != nil {
				opt(o)
			}
		})
		ag, err := r.svc.SaveAgent(ctx, alice, agents.Agent{Name: "Worker", Model: "m", Builtins: []string{studio.CapTriggersPropose}})
		Expect(err).NotTo(HaveOccurred())
		r.agent = ag
		r.svc.SetCapabilityGate(gate)
		gate.set(studio.CapTriggersPropose, true)
		gate.set(studio.CapProposalsConfirm, true)
		r.untrusted = unpaired
		b = r.bind(studio.ConnectorInput{Kind: "chatty", Token: "tok", Allow: []string{"11"}, ReplyLanguage: lang,
			Approval: &connectors.Approval{Acts: []string{"act.files"}}})
		seq = 0
	}
	ptr := func(s string) *string { return &s }
	ingest := func(m contract.ConnectorMessage) {
		seq++
		m.MessageID, m.SenderID, m.SenderName = "m"+string(rune('a'+seq)), "11", "Cy"
		Expect(r.conns.Ingest(ctx, "chat-addon", contract.ConnectorInbound{BindingID: b.ID, ConnectorMessage: m})).To(Succeed())
		sh.clock.Advance(time.Second)
	}
	dm := func(text string) contract.ConnectorMessage {
		return contract.ConnectorMessage{Chat: "11", Direct: true, Text: text}
	}
	turnEnded := func() {
		waitFor("the turn to end", func() bool {
			chats, err := r.svc.List(ctx, alice, 50)
			Expect(err).NotTo(HaveOccurred())
			for _, c := range chats {
				if turn(r.svc, alice, c.ID).Running {
					return false
				}
			}
			// The chat is idle before the connector has let go of the reply;
			// a command in that gap is refused as busy.
			return !r.conns.RepliesActiveForTest()
		})
	}
	// ask sends a direct message and returns what the bot wrote back last,
	// after n messages have come.
	ask := func(text string, n int) string {
		ingest(dm(text))
		waitFor("the answer", func() bool { return len(fake.texts("11", "")) >= n })
		// The answer reaches the chat before the turn is marked finished, and a
		// command that comes in that gap is refused as busy. Wait for the end of
		// the turn, as a person does when they read the answer.
		turnEnded()
		t := fake.texts("11", "")
		return t[len(t)-1]
	}

	It("writes the sentences in the language the connection names", func() {
		unpaired = true
		setup(ptr("it"), nil)
		Expect(ask("/new", 1)).To(Equal("Nuova chat avviata. Non vedrò più i nostri messaggi precedenti."))
		ingest(contract.ConnectorMessage{Chat: chatID, Thread: thread, Mention: true, Text: "/new"})
		waitFor("the refusal", func() bool { return len(fake.texts(chatID, thread)) >= 1 })
		Expect(fake.texts(chatID, thread)[0]).To(Equal("Solo un account verificato può avviare una nuova chat qui, perché tutti in questa conversazione la condividono."))
		ingest(contract.ConnectorMessage{Chat: "11", Direct: true, Unsupported: true})
		waitFor("the refusal", func() bool { return len(fake.texts("11", "")) >= 2 })
		Expect(fake.texts("11", "")[1]).To(Equal("Non riesco a leggere questo tipo di messaggio. Invia un testo, una foto, un file o un messaggio vocale."))
		Expect(ask(strings.Repeat("a", 8001), 3)).To(Equal("Il messaggio è troppo lungo. Invia meno di 8.000 caratteri."))
		Expect(ask("LA-ABCDEFGH", 4)).To(Equal("Questo codice non è valido. Potrebbe essere scaduto o già usato. Chiedine uno nuovo."))
	})

	It("keeps a fixed language whatever the sender writes", func() {
		setup(ptr("de"), nil)
		Expect(ask("Hello, can you help me with this file please?", 1)).To(Equal("ok"), "the model answers in its own words")
		Expect(ask("/new", 2)).To(HavePrefix("Neuer Chat gestartet"))
	})

	It("follows the language of the sender with auto, and a short reply does not change it", func() {
		setup(nil, nil)
		Expect(ask("Hello, can you send me the report please?", 1)).To(Equal("ok"))
		Expect(ask("/new", 2)).To(Equal("Started a new chat. I will not see our earlier messages."))
		Expect(ask("Ciao, puoi mandarmi il file di ieri?", 3)).To(Equal("ok"))
		Expect(ask("/new", 4)).To(HavePrefix("Nuova chat avviata."))
		Expect(ask("ok", 5)).To(Equal("ok"))
		Expect(ask("/new", 6)).To(HavePrefix("Nuova chat avviata."), "ok does not move the language")
		Expect(ask("thanks", 7)).To(Equal("ok"))
		Expect(ask("/new", 8)).To(HavePrefix("Nuova chat avviata."), "one English word does not move it either")
		Expect(ask("Kannst du mir bitte die Datei von gestern schicken?", 9)).To(Equal("ok"))
		Expect(ask("/new", 10)).To(HavePrefix("Neuer Chat gestartet."), "a sentence in another language does")
	})

	It("forgets the language after a day and falls back to English", func() {
		setup(nil, nil)
		Expect(ask("Ciao, puoi mandarmi il file di ieri?", 1)).To(Equal("ok"))
		sh.clock.Advance(25 * time.Hour)
		Expect(ask("/new", 2)).To(HavePrefix("Started a new chat."))
	})

	It("uses the interface language of the owner before English, when the server knows it", func() {
		setup(nil, func(o *studio.ConnectorOptions) {
			o.OwnerLanguage = func(context.Context, string) string { return "fr-CA" }
		})
		Expect(ask("/new", 1)).To(HavePrefix("Nouvelle conversation démarrée."))
	})

	// refusalSentence sends a kind of message the host cannot read and returns
	// the sentence it wrote for it. The refusal does not depend on a running
	// turn, so it is never the "still answering" note.
	refusalSentence := func() string {
		before := len(fake.texts("11", ""))
		ingest(contract.ConnectorMessage{Chat: "11", Direct: true, Unsupported: true})
		waitFor("the refusal", func() bool { return len(fake.texts("11", "")) > before })
		t := fake.texts("11", "")
		return t[len(t)-1]
	}

	It("speaks the interface language the owner's page sent, before it reads the sender", func() {
		setup(nil, nil)
		saved, err := r.conns.UpdateConnector(ctx, alice, b.ID, studio.ConnectorInput{Kind: "chatty", Name: b.Name, AgentID: b.AgentID, Allow: []string{"11"}, Enabled: true, UILanguage: ptr("de-AT")})
		Expect(err).NotTo(HaveOccurred())
		Expect(saved.UILanguage).To(Equal("de-AT"))
		ask("Ciao, puoi mandarmi il file di ieri?", 1)
		Expect(refusalSentence()).To(Equal(hostmsg.Template("de", "unsupported")), "the owner's language comes before the detection")
	})

	It("keeps the stored interface language when a save sends none, and ignores a value that is no tag", func() {
		setup(nil, nil)
		update := func(lang *string) studio.ConnectorInfo {
			got, err := r.conns.UpdateConnector(ctx, alice, b.ID, studio.ConnectorInput{Kind: "chatty", Name: b.Name, AgentID: b.AgentID, Allow: []string{"11"}, Enabled: true, UILanguage: lang})
			Expect(err).NotTo(HaveOccurred())
			return got
		}
		Expect(update(ptr("it")).UILanguage).To(Equal("it"))
		Expect(update(nil).UILanguage).To(Equal("it"))
		Expect(update(ptr("")).UILanguage).To(Equal("it"))
		Expect(update(ptr("not a tag!")).UILanguage).To(Equal("it"))
		Expect(update(ptr("fr")).UILanguage).To(Equal("fr"))
	})

	It("falls back to the detection when the owner's language is not one of the catalog or is unknown", func() {
		setup(nil, nil)
		_, err := r.conns.UpdateConnector(ctx, alice, b.ID, studio.ConnectorInput{Kind: "chatty", Name: b.Name, AgentID: b.AgentID, Allow: []string{"11"}, Enabled: true, UILanguage: ptr("ko")})
		Expect(err).NotTo(HaveOccurred())
		ask("Ciao, puoi mandarmi il file di ieri?", 1)
		Expect(refusalSentence()).To(Equal(hostmsg.Template("it", "unsupported")))
	})

	It("lets a fixed reply language win over the interface language of the owner", func() {
		setup(ptr("fr"), nil)
		_, err := r.conns.UpdateConnector(ctx, alice, b.ID, studio.ConnectorInput{Kind: "chatty", Name: b.Name, AgentID: b.AgentID, Allow: []string{"11"}, Enabled: true, UILanguage: ptr("de")})
		Expect(err).NotTo(HaveOccurred())
		Expect(ask("/new", 1)).To(HavePrefix("Nouvelle conversation démarrée."))
	})

	It("asks and confirms a trigger in Italian, and the answers never reach the model", func() {
		setup(ptr("it"), nil)
		ingest(contract.ConnectorMessage{Chat: chatID, Thread: thread, Mention: true, Text: "propose"})
		waitFor("the proposal", func() bool { return len(fake.texts(chatID, thread)) >= 2 })
		text := fake.texts(chatID, thread)[0]
		Expect(text).To(ContainSubstring("Proposta "))
		Expect(text).To(ContainSubstring("Permessi: sola lettura."))
		Expect(text).To(ContainSubstring("Non parte nulla finché non approvi. Rispondi \"sì\" per avviarlo o \"annulla\" per scartarlo"))
		Expect(text).To(ContainSubstring("ogni giorno alle 08:00 (UTC)"))
		before := len(fake.texts(chatID, thread))
		ingest(contract.ConnectorMessage{Chat: chatID, Thread: thread, Mention: true, Text: "sì"})
		waitFor("the confirmation", func() bool { return len(fake.texts(chatID, thread)) > before })
		got := fake.texts(chatID, thread)
		Expect(got[len(got)-1]).To(HavePrefix("Confermato. \"Report\" è attivo: Ogni giorno alle 08:00 (UTC)."))
		chats, err := r.svc.List(ctx, alice, 10)
		Expect(err).NotTo(HaveOccurred())
		for _, c := range chats {
			Expect(r.h.Sent(c.ID)).NotTo(ContainElement("sì"), "a confirmation is read by the host, not by the model")
		}
	})

	It("cancels a proposal with the word of each language", func() {
		for _, word := range []string{"annulla", "abbrechen", "cancelar", "annuler", "cancelar", "no"} {
			setup(nil, nil)
			ingest(contract.ConnectorMessage{Chat: chatID, Thread: thread, Mention: true, Text: "propose"})
			waitFor("the proposal", func() bool { return len(fake.texts(chatID, thread)) >= 2 })
			ingest(contract.ConnectorMessage{Chat: chatID, Thread: thread, Mention: true, Text: word})
			waitFor("the answer to "+word, func() bool { return len(fake.texts(chatID, thread)) >= 3 })
			Expect(fake.texts(chatID, thread)[2]).To(Equal("Cancelled."), word)
			ps, err := r.host.Proposals(ctx, alice)
			Expect(err).NotTo(HaveOccurred())
			Expect(ps).To(BeEmpty(), word)
		}
	})
})
