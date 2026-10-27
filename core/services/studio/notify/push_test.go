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
	"testing"

	"github.com/mudler/LocalAI/core/services/studio/notify"
	"github.com/mudler/LocalAI/core/services/studio/notify/notifytest"
)

func TestJSONPushInMemory(t *testing.T) {
	notifytest.RunPush(t, func(t *testing.T) notify.PushStore {
		s, err := notify.NewJSONPush("")
		if err != nil {
			t.Fatal(err)
		}
		return s
	})
}

func TestJSONPushOnDisk(t *testing.T) {
	notifytest.RunPush(t, func(t *testing.T) notify.PushStore {
		s, err := notify.NewJSONPush(t.TempDir())
		if err != nil {
			t.Fatal(err)
		}
		return s
	})
}

func TestJSONPushSurvivesReopenAndKeepsItsFilePrivate(t *testing.T) {
	dir := t.TempDir()
	first, _ := notify.NewJSONPush(dir)
	notifytest.SeedPush(t, first)
	second, err := notify.NewJSONPush(dir)
	if err != nil {
		t.Fatal(err)
	}
	notifytest.CheckPushSeed(t, second)
	st, err := os.Stat(filepath.Join(dir, "push.json"))
	if err != nil || st.Mode().Perm() != 0o600 {
		t.Fatalf("push.json: %v %v", st, err)
	}
}

// browserKeys makes the keys a browser makes for a push subscription.
func browserKeys(t *testing.T) (p256dh, auth string) {
	t.Helper()
	k, err := ecdh.P256().GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
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

func newFakePush(t *testing.T) *fakePush {
	t.Helper()
	f := &fakePush{}
	f.status.Store(201)
	f.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		f.last.Store(body)
		f.hits.Add(1)
		w.WriteHeader(int(f.status.Load()))
	}))
	t.Cleanup(f.srv.Close)
	return f
}

func newPusher(t *testing.T, st notify.PushStore) *notify.Pusher {
	t.Helper()
	return notify.NewPusher(notify.PusherOptions{Store: st, Client: http.DefaultClient, CheckEndpoint: func(string) error { return nil }})
}

func TestANotificationReachesTheBrowsersOfItsOwnerAndOnlyThem(t *testing.T) {
	st, _ := notify.NewJSONPush("")
	p := newPusher(t, st)
	mine, other := newFakePush(t), newFakePush(t)
	k, a := browserKeys(t)
	ctx := context.Background()
	if err := p.Subscribe(ctx, "u1", mine.srv.URL+"/m", k, a); err != nil {
		t.Fatal(err)
	}
	if err := p.Subscribe(ctx, "u2", other.srv.URL+"/o", k, a); err != nil {
		t.Fatal(err)
	}
	p.Send(ctx, notify.Notification{Principal: "u1", Title: "Nightly failed", Body: "secret body text", Link: "/app/chats/c1"})
	if mine.hits.Load() != 1 || other.hits.Load() != 0 {
		t.Fatalf("hits: mine %d, other %d", mine.hits.Load(), other.hits.Load())
	}
	// The payload is encrypted for the browser, so the push service sees none
	// of the text: nothing in the body shows the title or the body text.
	raw, _ := mine.last.Load().([]byte)
	for _, s := range []string{"Nightly failed", "secret body text"} {
		if len(raw) == 0 || json.Valid(raw) || bytes.Contains(raw, []byte(s)) {
			t.Fatalf("the push body is not encrypted: %q", raw)
		}
	}
}

func TestASubscriptionTheServiceReportsGoneIsDropped(t *testing.T) {
	st, _ := notify.NewJSONPush("")
	p := newPusher(t, st)
	gone := newFakePush(t)
	gone.status.Store(410)
	k, a := browserKeys(t)
	ctx := context.Background()
	_ = p.Subscribe(ctx, "u1", gone.srv.URL+"/g", k, a)
	p.Send(ctx, notify.Notification{Principal: "u1", Title: "x"})
	if subs, _ := st.ForPrincipal(ctx, "u1"); len(subs) != 0 {
		t.Fatalf("a dead subscription stayed: %+v", subs)
	}
	// A server error is not a reason to forget the browser.
	flaky := newFakePush(t)
	flaky.status.Store(500)
	_ = p.Subscribe(ctx, "u1", flaky.srv.URL+"/f", k, a)
	p.Send(ctx, notify.Notification{Principal: "u1", Title: "x"})
	if subs, _ := st.ForPrincipal(ctx, "u1"); len(subs) != 1 {
		t.Fatalf("a 500 dropped the subscription: %+v", subs)
	}
}

func TestSubscribeRefusesAnEndpointTheServerMustNotCall(t *testing.T) {
	st, _ := notify.NewJSONPush("")
	p := notify.NewPusher(notify.PusherOptions{Store: st}) // the default check
	k, a := browserKeys(t)
	for _, ep := range []string{"http://93.184.216.34/x", "http://push.example/x", "https://127.0.0.1/x", "https://localhost/x", "https://169.254.169.254/latest", "https://10.0.0.5/x", "file:///etc/passwd", ""} {
		if err := p.Subscribe(context.Background(), "u1", ep, k, a); err == nil {
			t.Errorf("endpoint %q was accepted", ep)
		}
	}
	if err := p.Subscribe(context.Background(), "u1", "https://example.org/x", "", a); err == nil {
		t.Error("a subscription without keys was accepted")
	}
}

func TestThePublicKeyIsStableAcrossPushers(t *testing.T) {
	st, _ := notify.NewJSONPush("")
	one, err := newPusher(t, st).PublicKey(context.Background())
	if err != nil || one == "" {
		t.Fatalf("key = %q, %v", one, err)
	}
	two, _ := newPusher(t, st).PublicKey(context.Background())
	if one != two {
		t.Fatalf("keys differ: %q %q", one, two)
	}
}

// The tests above inject http.DefaultClient. The client a server really uses is
// the default one, which must not dial a loopback or private address at send
// time, even for an endpoint that passed the check when it was stored (a name
// can change its address later).
func TestTheDefaultClientDoesNotDialALoopbackAddress(t *testing.T) {
	st, _ := notify.NewJSONPush("")
	p := notify.NewPusher(notify.PusherOptions{Store: st, CheckEndpoint: func(string) error { return nil }})
	fake := newFakePush(t)
	k, a := browserKeys(t)
	ctx := context.Background()
	if err := p.Subscribe(ctx, "u1", fake.srv.URL+"/m", k, a); err != nil {
		t.Fatal(err)
	}
	p.Send(ctx, notify.Notification{Principal: "u1", Title: "x"})
	if fake.hits.Load() != 0 {
		t.Fatalf("the default client reached a loopback server %d times", fake.hits.Load())
	}
}
