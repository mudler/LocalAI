package studio_test

import (
	"bytes"
	"context"
	"net/http"
	"strings"
	"sync/atomic"
	"time"

	"github.com/mudler/LocalAI/addons/telegram/transport"
	"github.com/mudler/LocalAI/core/http/auth"
	"github.com/mudler/LocalAI/core/services/studio"
	"github.com/mudler/LocalAI/core/services/studio/media"
	"github.com/mudler/LocalAI/core/services/studio/provider"
	"github.com/mudler/LocalAI/core/services/studio/session"
	"github.com/mudler/LocalAI/pkg/addons/contract"
	"github.com/mudler/LocalAI/tests/studiobot"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// The files of a Telegram message through the connector manager: the add-on
// is the real one over a scripted Bot API, the chats are the real ones.

var oggVoice = append([]byte("OggS\x00\x02"), bytes.Repeat([]byte{7}, 64)...)

// transcriber is a media service whose transcription model answers text,
// or none when model is "". It counts the requests.
func transcriber(model, text string, status int) (*media.Service, *atomic.Int32) {
	var calls atomic.Int32
	p := provider.New()
	p.Transport().SetHandler(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_, _ = w.Write([]byte(`{"text":"` + text + `"}`))
	}))
	users := func(_ context.Context, id string) (*auth.User, error) {
		return &auth.User{ID: id, Status: auth.StatusActive}, nil
	}
	return media.New(p, users, func(media.Capability) string { return model }), &calls
}

// mediaRig is a Telegram rig whose chats see images and transcribe with m.
func mediaRig(m *media.Service, answer string) (*connRig, *scriptedBot) {
	sh, bot := newShared(), newScriptedBot()
	sh.deps = func(d *studio.Deps) {
		d.Media = m
		d.Modalities = func(string) studio.Modalities { return studio.Modalities{Vision: true} }
	}
	r := newConnRig(sh, connStore(), bot, connSay(answer), nil)
	r.bind(studio.ConnectorInput{Allow: []string{"11"}})
	return r, bot
}

func photoOf(id string) map[string]any {
	return map[string]any{"photo": []any{map[string]any{"file_id": id, "width": 1, "height": 1}}}
}

func onlyChat(r *connRig) session.ChatRecord {
	chats, err := r.svc.List(context.Background(), alice, 10)
	Expect(err).NotTo(HaveOccurred())
	Expect(chats).To(HaveLen(1), "chats = %+v", chats)
	return chats[0]
}

func noChats(r *connRig) {
	chats, err := r.svc.List(context.Background(), alice, 10)
	Expect(err).NotTo(HaveOccurred())
	Expect(chats).To(BeEmpty(), "a refused message made %d chats", len(chats))
}

// hostileMedia is the Telegram add-on, except that the op media answers a
// file whose name climbs out of any folder.
type hostileMedia struct{ inner contract.ConnectorRuntime }

func (h hostileMedia) Connector(ctx context.Context, req contract.ConnectorRequest) (contract.ConnectorResponse, error) {
	if req.Op == contract.ConnectorOpMedia {
		return contract.ConnectorResponse{File: &contract.ConnectorFile{Name: "../../../escape/evil.png", MIME: "image/png", Data: pngBytes}}, nil
	}
	return h.inner.Connector(ctx, req)
}

var _ = Describe("connectors media", func() {
	It("a photo with a caption is an upload of the chat that a model which sees gets, and the caption is the text", func() {
		r, bot := mediaRig(nil, "A cat.")
		bot.media["p1"] = botMedia{Path: "photos/file_1.png", Data: pngBytes}
		extra := photoOf("p1")
		extra["caption"] = "what is this?"
		bot.message(1, 11, extra)
		waitFor("the answer", func() bool { return len(bot.answers()) == 1 })
		Expect(bot.answers()[0]).To(Equal("A cat."))
		c := onlyChat(r)
		sent := r.h.Sent(c.ID)
		Expect(sent).To(HaveLen(1), "the chat got %q", sent)
		Expect(sent[0]).To(HavePrefix("what is this?"), "the chat got %q", sent)
		Expect(sent[0]).To(ContainSubstring("photo-up1.png"), "the chat got %q", sent)
		f := chatFiles(r.svc, alice, c.ID)["photo-up1.png"]
		Expect(f.Origin).To(Equal(session.FileFromUser), "Files lists %+v", f)
		Expect(f.Media).To(BeTrue(), "the image went to the model as an image: %+v", f)
		Expect(r.logs.String()).NotTo(ContainSubstring(botToken))
	})

	It("an album is one turn with all its photos", func() {
		r, bot := mediaRig(nil, "Two photos.")
		bot.media["p1"] = botMedia{Path: "photos/file_1.png", Data: pngBytes}
		bot.media["p2"] = botMedia{Path: "photos/file_2.png", Data: pngBytes}
		first, second := photoOf("p1"), photoOf("p2")
		first["media_group_id"], second["media_group_id"] = "G1", "G1"
		first["caption"] = "our trip"
		bot.message(1, 11, first)
		bot.message(2, 11, second)
		waitFor("the answer", func() bool { return len(bot.answers()) == 1 })
		c := onlyChat(r)
		sent := r.h.Sent(c.ID)
		Expect(sent).To(HaveLen(1), "the album took more than one turn: %q", sent)
		Expect(sent[0]).To(And(HavePrefix("our trip"), ContainSubstring("photo-up1.png"), ContainSubstring("photo-up2.png")), "the chat got %q", sent)
		Expect(chatFiles(r.svc, alice, c.ID)).To(HaveLen(2))
		time.Sleep(100 * time.Millisecond)
		Expect(bot.answers()).To(HaveLen(1), "one answer for the album")
	})

	It("a photo that is too large gets a sentence, and its caption still goes", func() {
		r, bot := mediaRig(nil, "ok")
		bot.media["big"] = botMedia{Path: "photos/big.png", Size: 5 << 20, Data: pngBytes}
		bot.message(1, 11, photoOf("big"))
		waitFor("the sentence", func() bool { return len(bot.answers()) == 1 })
		Expect(bot.answers()[0]).To(ContainSubstring("too large"))
		time.Sleep(100 * time.Millisecond)
		noChats(r)
		extra := photoOf("big")
		extra["caption"] = "see the picture"
		bot.message(2, 11, extra)
		waitFor("the sentence and the answer", func() bool { return len(bot.answers()) == 3 })
		Expect(bot.answers()[1]).To(ContainSubstring("too large"))
		Expect(r.h.Sent(onlyChat(r).ID)).To(Equal([]string{"see the picture"}))
	})

	It("a voice message is transcribed with the instance's model and its transcript is the text of the turn", func() {
		m, calls := transcriber("whisper", "buy milk and call the dentist", http.StatusOK)
		r, bot := mediaRig(m, "Noted.")
		bot.media["v1"] = botMedia{Path: "voice/file_1.oga", Data: oggVoice}
		bot.message(1, 11, map[string]any{"voice": map[string]any{"file_id": "v1", "duration": 2}})
		waitFor("the answer", func() bool { return len(bot.answers()) == 1 })
		Expect(bot.answers()[0]).To(Equal("Noted."))
		c := onlyChat(r)
		Expect(r.h.Sent(c.ID)).To(Equal([]string{"[Voice message, transcribed]\nbuy milk and call the dentist"}))
		Expect(calls.Load()).To(Equal(int32(1)), "transcribed once, not again as an upload")
		Expect(chatFiles(r.svc, alice, c.ID)).To(BeEmpty(), "the recording is not kept")
	})

	It("a voice message with no transcription model, or one that fails, gets one sentence and no turn", func() {
		for _, tc := range []struct {
			m    *media.Service
			want string
		}{
			{nil, "no speech-to-text model"},
			{func() *media.Service { m, _ := transcriber("", "", http.StatusOK); return m }(), "no speech-to-text model"},
			{func() *media.Service { m, _ := transcriber("whisper", "", http.StatusInternalServerError); return m }(), "could not understand"},
			{func() *media.Service { m, _ := transcriber("whisper", "", http.StatusOK); return m }(), "could not understand"},
		} {
			r, bot := mediaRig(tc.m, "never")
			bot.media["v1"] = botMedia{Path: "voice/file_1.oga", Data: oggVoice}
			bot.message(1, 11, map[string]any{"voice": map[string]any{"file_id": "v1"}, "caption": "listen"})
			waitFor("the sentence", func() bool { return len(bot.answers()) == 1 })
			Expect(bot.answers()[0]).To(ContainSubstring(tc.want))
			time.Sleep(100 * time.Millisecond)
			Expect(bot.answers()).To(HaveLen(1))
			noChats(r)
		}
	})

	It("a name that the add-on sends cannot leave the chat's folder", func() {
		sh, bot := newShared(), newScriptedBot()
		api := &transport.API{BaseURL: bot.srv.URL, Client: bot.srv.Client()}
		tg := studiobot.Telegram(api)
		tg.Runtime = hostileMedia{inner: tg.Runtime}
		r := newConnRigWith(sh, connStore(), studiobot.NewTransport(tg), connSay("ok"), nil)
		r.bot = bot
		r.bind(studio.ConnectorInput{Allow: []string{"11"}})
		bot.message(1, 11, photoOf("p1"))
		waitFor("the answer", func() bool { return len(bot.answers()) == 1 })
		c := onlyChat(r)
		files := chatFiles(r.svc, alice, c.ID)
		Expect(files).To(HaveLen(1))
		Expect(files).To(HaveKey("evil.png"))
		sent := r.h.Sent(c.ID)
		Expect(sent[0]).NotTo(ContainSubstring(".."), "the chat got %q", sent)
		Expect(strings.Count(sent[0], "escape")).To(BeZero(), "the chat got %q", sent)
	})
})
