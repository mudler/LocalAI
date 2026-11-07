package studio

import (
	"context"
	"sync"
	"time"

	"github.com/mudler/LocalAI/core/services/studio/connectors"
	"github.com/mudler/LocalAI/core/services/studio/hostmsg"
	"github.com/mudler/LocalAI/core/services/studio/triggers"
	"github.com/mudler/LocalAI/pkg/addons/contract"
)

// The sentences that the host writes to people in a chat (see hostmsg) come
// in the language of the person. A binding has a reply language: a fixed
// language, or auto, which uses the interface language of the owner when the
// server knows it and otherwise reads the language of the sender's messages.
// The last clear language of a sender is kept for langTTL, so a short answer
// such as "ok" or "yes" keeps the language of the conversation.
//
// Every host sentence goes through msg (or a Voice made by voice). A guard
// test fails when a fixed English text reaches the send path any other way.

const (
	// langTTL is how long the language of a sender is kept after the last
	// message that showed or kept it.
	langTTL = 24 * time.Hour
	// langMax bounds the memory; the oldest entries go first when it is full.
	langMax = 4096
)

type langKey struct{ binding, who string }

type langEntry struct {
	lang string
	at   time.Time
}

// langMemory is the last clear language of each sender and each chat. A trigger
// writes to a chat without a sender, and finds the language by the chat.
type langMemory struct {
	mu sync.Mutex
	m  map[langKey]langEntry
}

func (l *langMemory) get(k langKey, now time.Time) (string, bool) {
	l.mu.Lock()
	defer l.mu.Unlock()
	e, ok := l.m[k]
	if !ok || now.Sub(e.at) >= langTTL {
		return "", false
	}
	return e.lang, true
}

func (l *langMemory) set(k langKey, lang string, now time.Time) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.m == nil {
		l.m = map[langKey]langEntry{}
	}
	if len(l.m) >= langMax {
		for key, e := range l.m {
			if now.Sub(e.at) >= langTTL {
				delete(l.m, key)
			}
		}
		if len(l.m) >= langMax {
			clear(l.m)
		}
	}
	l.m[k] = langEntry{lang: lang, at: now}
}

// learn reads one message of a sender. A message in a clear language sets it,
// but a message that only barely shows another language than the one the
// sender had does not move it (hostmsg.SwitchScore): "thanks" in an Italian
// conversation stays Italian. A message that says nothing keeps the language
// and renews its time.
func (l *langMemory) learn(binding, sender, chat, text string, now time.Time) {
	d := hostmsg.Detect(text)
	for _, k := range []langKey{{binding, "s:" + sender}, {binding, "c:" + chat}} {
		if k.who == "s:" || k.who == "c:" {
			continue
		}
		cur, ok := l.get(k, now)
		switch {
		case d.Lang == "" && ok:
			l.set(k, cur, now)
		case d.Lang == "":
		case !ok || cur == d.Lang || d.Score >= hostmsg.SwitchScore:
			l.set(k, d.Lang, now)
		default:
			l.set(k, cur, now)
		}
	}
}

// noteLanguage lets the sender's message teach the host their language. It
// reads the text and keeps nothing of it.
func (c *Connectors) noteLanguage(b connectors.Binding, m contract.ConnectorMessage) {
	if b.ReplyLang() != hostmsg.Auto {
		return
	}
	c.lang.learn(b.ID, m.SenderID, m.Chat, m.Text, c.o.Clock())
}

// languageOf is the language of the sentences for a sender (or a chat, when
// the sender is not known): the setting of the binding when it names one. With
// auto it is the interface language of the owner when the server knows it and
// the catalog has it, else the language the sender last wrote clearly (the
// stop-word detection), else English.
func (c *Connectors) languageOf(ctx context.Context, b connectors.Binding, sender, chat string) string {
	if set := b.ReplyLang(); set != hostmsg.Auto {
		if l, ok := hostmsg.Normalize(set); ok {
			return l
		}
	}
	if l, ok := c.ownerLanguage(ctx, b); ok {
		return l
	}
	now := c.o.Clock()
	if sender != "" {
		if l, ok := c.lang.get(langKey{b.ID, "s:" + sender}, now); ok {
			return l
		}
	}
	if chat != "" {
		if l, ok := c.lang.get(langKey{b.ID, "c:" + chat}, now); ok {
			return l
		}
	}
	return hostmsg.English
}

// ownerLanguage is the interface language of the owner of a binding as a
// language of the catalog. The page that saved the binding sent it and the
// binding keeps it, so every frontend knows it. A server hook can supply it
// for a binding that has none. A tag the catalog lacks (Korean, for example)
// counts as unknown, and the detection decides.
func (c *Connectors) ownerLanguage(ctx context.Context, b connectors.Binding) (string, bool) {
	if l, ok := hostmsg.Normalize(b.UILanguage); ok {
		return l, true
	}
	if c.o.OwnerLanguage != nil {
		return hostmsg.Normalize(c.o.OwnerLanguage(ctx, b.Owner))
	}
	return "", false
}

// voice speaks to a sender (and a chat) of a binding in their language.
func (c *Connectors) voice(ctx context.Context, b connectors.Binding, sender, chat string) hostmsg.Voice {
	return hostmsg.Voice{Lang: c.languageOf(ctx, b, sender, chat)}
}

// voiceOf is voice for a binding known by its ID. A binding that cannot be read
// gets the English voice.
func (c *Connectors) voiceOf(ctx context.Context, bindingID, sender, chat string) hostmsg.Voice {
	b, err := c.o.Store.GetBinding(ctx, bindingID)
	if err != nil {
		return hostmsg.Voice{Lang: hostmsg.English}
	}
	return c.voice(ctx, b, sender, chat)
}

// voiceOf is the voice of the chat where a trigger was proposed: it has no
// sender, so the language of the chat decides.
func (h *Triggers) voiceOf(ctx context.Context, t triggers.Trigger) hostmsg.Voice {
	if c := h.svc.connectors; c != nil {
		return c.voiceOf(ctx, t.Route.Binding, "", t.Route.Chat)
	}
	return hostmsg.Voice{}
}

// msg is the one function that turns a key of the host message catalog into the
// sentence for a sender of a binding. args are name and value pairs for the
// places of the template. The guard test (hostmsg_guard_test.go) fails when a
// sentence for a chat is written any other way.
func (c *Connectors) msg(ctx context.Context, b connectors.Binding, sender, key string, args ...any) string {
	return c.voice(ctx, b, sender, "").T(key, args...)
}

// byTextFrom is the detail of an audit line for an answer that came by text.
// It is for the log and never reaches a chat.
func byTextFrom(sender string) string { return "by text from sender " + sender }
