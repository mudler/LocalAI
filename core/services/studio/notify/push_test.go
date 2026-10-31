package notify_test

import (
	"bytes"
	"context"
	"crypto/ecdh"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync/atomic"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/mudler/LocalAI/core/services/studio/notify"
	"github.com/mudler/LocalAI/core/services/studio/notify/notifytest"
)

// browserKeys makes the keys a browser makes for a push subscription.
func browserKeys() (p256dh, auth string) {
	k, err := ecdh.P256().GenerateKey(rand.Reader)
	Expect(err).NotTo(HaveOccurred())
	a := make([]byte, 16)
	_, _ = rand.Read(a)
	return base64.RawURLEncoding.EncodeToString(k.PublicKey().Bytes()), base64.RawURLEncoding.EncodeToString(a)
}

// fakePush stands in for a browser's push service.
type fakePush struct {
	srv    *httptest.Server
	status atomic.Int32
	hits   atomic.Int32
	last   atomic.Value // the request body
}

func newFakePush() *fakePush {
	f := &fakePush{}
	f.status.Store(201)
	f.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		f.last.Store(body)
		f.hits.Add(1)
		w.WriteHeader(int(f.status.Load()))
	}))
	DeferCleanup(f.srv.Close)
	return f
}

func newPusher(st notify.PushStore) *notify.Pusher {
	return notify.NewPusher(notify.PusherOptions{Store: st, Client: http.DefaultClient, CheckEndpoint: func(string) error { return nil }})
}

// blockingPushStore holds every lookup until its context ends, as a database
// that does not answer would.
type blockingPushStore struct {
	notify.PushStore
	started chan struct{}
}

func (b blockingPushStore) ForPrincipal(ctx context.Context, _ string) ([]notify.PushSubscription, error) {
	b.started <- struct{}{}
	<-ctx.Done()
	return nil, ctx.Err()
}

var _ = Describe("web push", func() {
	Describe("the JSON push store", func() {
		Describe("in memory", func() {
			notifytest.RunPush(func() notify.PushStore {
				s, err := notify.NewJSONPush("")
				Expect(err).NotTo(HaveOccurred())
				return s
			})
		})

		Describe("on disk", func() {
			notifytest.RunPush(func() notify.PushStore {
				s, err := notify.NewJSONPush(GinkgoT().TempDir())
				Expect(err).NotTo(HaveOccurred())
				return s
			})
		})

		It("survives a reopen and keeps its file private", func() {
			dir := GinkgoT().TempDir()
			first, _ := notify.NewJSONPush(dir)
			notifytest.SeedPush(first)
			second, err := notify.NewJSONPush(dir)
			Expect(err).NotTo(HaveOccurred())
			notifytest.CheckPushSeed(second)
			st, err := os.Stat(filepath.Join(dir, "push.json"))
			Expect(err).NotTo(HaveOccurred())
			Expect(st.Mode().Perm()).To(Equal(os.FileMode(0o600)))
		})
	})

	It("sends a notification to the browsers of its owner and only them", func() {
		st, _ := notify.NewJSONPush("")
		p := newPusher(st)
		mine, other := newFakePush(), newFakePush()
		k, a := browserKeys()
		ctx := context.Background()
		Expect(p.Subscribe(ctx, "u1", mine.srv.URL+"/m", k, a)).To(Succeed())
		Expect(p.Subscribe(ctx, "u2", other.srv.URL+"/o", k, a)).To(Succeed())
		p.Send(ctx, notify.Notification{Principal: "u1", Title: "Nightly failed", Body: "secret body text", Link: "/app/chats/c1"})
		Expect(mine.hits.Load()).To(BeEquivalentTo(1), "hits of mine")
		Expect(other.hits.Load()).To(BeEquivalentTo(0), "hits of other")
		// The payload is encrypted for the browser, so the push service sees none
		// of the text: nothing in the body shows the title or the body text.
		raw, _ := mine.last.Load().([]byte)
		for _, s := range []string{"Nightly failed", "secret body text"} {
			Expect(raw).NotTo(BeEmpty(), "the push body is not encrypted")
			Expect(json.Valid(raw)).To(BeFalse(), "the push body is not encrypted: %q", raw)
			Expect(bytes.Contains(raw, []byte(s))).To(BeFalse(), "the push body is not encrypted: %q", raw)
		}
	})

	It("drops a subscription the service reports gone", func() {
		st, _ := notify.NewJSONPush("")
		p := newPusher(st)
		gone := newFakePush()
		gone.status.Store(410)
		k, a := browserKeys()
		ctx := context.Background()
		_ = p.Subscribe(ctx, "u1", gone.srv.URL+"/g", k, a)
		p.Send(ctx, notify.Notification{Principal: "u1", Title: "x"})
		subs, _ := st.ForPrincipal(ctx, "u1")
		Expect(subs).To(BeEmpty(), "a dead subscription stayed")
		// A server error is not a reason to forget the browser.
		flaky := newFakePush()
		flaky.status.Store(500)
		_ = p.Subscribe(ctx, "u1", flaky.srv.URL+"/f", k, a)
		p.Send(ctx, notify.Notification{Principal: "u1", Title: "x"})
		subs, _ = st.ForPrincipal(ctx, "u1")
		Expect(subs).To(HaveLen(1), "a 500 dropped the subscription")
	})

	It("refuses an endpoint the server must not call", func() {
		st, _ := notify.NewJSONPush("")
		p := notify.NewPusher(notify.PusherOptions{Store: st}) // the default check
		k, a := browserKeys()
		for _, ep := range []string{"http://93.184.216.34/x", "http://push.example/x", "https://127.0.0.1/x", "https://localhost/x", "https://169.254.169.254/latest", "https://10.0.0.5/x", "file:///etc/passwd", ""} {
			Expect(p.Subscribe(context.Background(), "u1", ep, k, a)).NotTo(Succeed(), "endpoint %q was accepted", ep)
		}
		Expect(p.Subscribe(context.Background(), "u1", "https://example.org/x", "", a)).NotTo(Succeed(), "a subscription without keys was accepted")
	})

	// A public address over plain http must be refused for its scheme, not for a
	// failed name lookup: an address literal needs no lookup.
	It("requires https even on a public address", func() {
		st, _ := notify.NewJSONPush("")
		p := notify.NewPusher(notify.PusherOptions{Store: st})
		k, a := browserKeys()
		err := p.Subscribe(context.Background(), "u1", "http://93.184.216.34/x", k, a)
		Expect(err).To(MatchError(ContainSubstring("https")), "an http endpoint on a public address must be refused with a message that names https")
		Expect(p.Subscribe(context.Background(), "u1", "https://93.184.216.34/x", k, a)).To(Succeed(), "an https endpoint on a public address was refused")
	})

	It("keeps the public key stable across pushers", func() {
		st, _ := notify.NewJSONPush("")
		one, err := newPusher(st).PublicKey(context.Background())
		Expect(err).NotTo(HaveOccurred())
		Expect(one).NotTo(BeEmpty())
		two, _ := newPusher(st).PublicKey(context.Background())
		Expect(two).To(Equal(one))
	})

	// The tests above inject http.DefaultClient. The client a server really uses is
	// the default one, which must not dial a loopback or private address at send
	// time, even for an endpoint that passed the check when it was stored (a name
	// can change its address later).
	It("does not dial a loopback address with the default client", func() {
		st, _ := notify.NewJSONPush("")
		p := notify.NewPusher(notify.PusherOptions{Store: st, CheckEndpoint: func(string) error { return nil }})
		fake := newFakePush()
		k, a := browserKeys()
		ctx := context.Background()
		Expect(p.Subscribe(ctx, "u1", fake.srv.URL+"/m", k, a)).To(Succeed())
		p.Send(ctx, notify.Notification{Principal: "u1", Title: "x"})
		Expect(fake.hits.Load()).To(BeEquivalentTo(0), "the default client reached a loopback server")
	})

	It("ends the pushes in flight on Close and does nothing for later ones", func() {
		st := blockingPushStore{started: make(chan struct{}, 64)}
		p := newPusher(st)
		p.SendAsync(notify.Notification{Principal: "u1", Title: "T"})
		Eventually(st.started).Should(Receive())
		closed := make(chan struct{})
		go func() { defer GinkgoRecover(); p.Close(); close(closed) }()
		Eventually(closed, 5*time.Second).Should(BeClosed(), "Close did not end the push in flight")
		p.SendAsync(notify.Notification{Principal: "u1", Title: "after"})
		Consistently(st.started, 50*time.Millisecond).ShouldNot(Receive(), "a push started after Close")
	})

	It("drops more pushes than the bound instead of queuing them forever", func() {
		st := blockingPushStore{started: make(chan struct{}, 256)}
		p := newPusher(st)
		defer p.Close()
		for range 200 {
			p.SendAsync(notify.Notification{Principal: "u1", Title: "T"})
		}
		Consistently(func() int { return len(st.started) }, 100*time.Millisecond).Should(BeNumerically("<=", 32), "pushes ran at once")
	})
})
