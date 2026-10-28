package notify

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	webpush "github.com/SherClockHolmes/webpush-go"

	"github.com/mudler/LocalAI/pkg/httpclient"
)

// MaxPushSubscriptions is how many browsers one user can have subscribed. The
// oldest go first.
const MaxPushSubscriptions = 10

// PushSubscription is one browser that asked to be told about notifications.
// Endpoint is the address of the browser's push service; P256dh and Auth are
// the keys the browser made for it.
type PushSubscription struct {
	Principal string
	Endpoint  string
	P256dh    string
	Auth      string
	Created   time.Time
}

// PushStore keeps push subscriptions and the VAPID key pair of the server.
type PushStore interface {
	// Subscribe stores the subscription. An endpoint is one browser: when it is
	// stored already, whoever owned it before loses it. The principal's
	// subscriptions are trimmed to MaxPushSubscriptions, oldest first.
	Subscribe(ctx context.Context, s PushSubscription) error
	// Unsubscribe removes the principal's subscription with this endpoint.
	Unsubscribe(ctx context.Context, principal, endpoint string) error
	ForPrincipal(ctx context.Context, principal string) ([]PushSubscription, error)
	// Drop removes the subscription with this endpoint whoever owns it. The
	// sender calls it when the push service says the browser is gone.
	Drop(ctx context.Context, endpoint string) error
	// EnsureVAPID returns the server's key pair. The first call stores the pair
	// that generate makes; every call after it, on any frontend, returns that
	// pair, even when two calls race.
	EnsureVAPID(ctx context.Context, generate func() (private, public string, err error)) (private, public string, err error)
}

// ErrPushEndpoint means a subscription names an address the server must not
// send to.
var ErrPushEndpoint = errors.New("notify: the push endpoint is not acceptable")

// PusherOptions configures a Pusher.
type PusherOptions struct {
	Store PushStore
	// Subscriber is the contact in the VAPID token: a mailto: or https URL. Nil
	// means the project's home page.
	Subscriber string
	// Client sends the requests. Nil means a client that refuses private and
	// internal addresses and does not follow redirects. A test gives its own.
	Client webpush.HTTPClient
	// CheckEndpoint validates an endpoint when a browser subscribes. Nil means
	// an https URL on a public address.
	CheckEndpoint func(endpoint string) error
	Logger        *slog.Logger
}

// Pusher sends a notification to the browsers of its owner. The payload has
// the title and the link, never the body.
type Pusher struct {
	o PusherOptions

	mu     sync.Mutex // guards shut and orders wg.Add before Close's Wait
	shut   bool
	ctx    context.Context // ends when Close is called
	cancel context.CancelFunc
	wg     sync.WaitGroup
	slots  chan struct{} // bounds the pushes in flight
}

// maxPushesInFlight bounds the SendAsync calls that run at once. A push is
// best effort, so one past the bound is dropped and logged.
const maxPushesInFlight = 32

// pushTimeout bounds one SendAsync.
const pushTimeout = time.Minute

// NewPusher returns a Pusher. The store is required.
func NewPusher(o PusherOptions) *Pusher {
	if o.Subscriber == "" {
		o.Subscriber = "https://localai.io"
	}
	if o.Client == nil {
		o.Client = httpclient.New(httpclient.WithExternalOnly(), httpclient.WithTimeout(10*time.Second))
	}
	if o.CheckEndpoint == nil {
		o.CheckEndpoint = checkPushEndpoint
	}
	if o.Logger == nil {
		o.Logger = slog.Default()
	}
	ctx, cancel := context.WithCancel(context.Background())
	return &Pusher{o: o, ctx: ctx, cancel: cancel, slots: make(chan struct{}, maxPushesInFlight)}
}

// SendAsync sends n in the background and returns at once. At most
// maxPushesInFlight run together, and Close ends them. It does nothing once
// Close has been called.
func (p *Pusher) SendAsync(n Notification) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.shut {
		return
	}
	select {
	case p.slots <- struct{}{}:
	default:
		p.o.Logger.Warn("notify: too many pushes in flight; one is dropped")
		return
	}
	p.wg.Add(1)
	go func() {
		defer p.wg.Done()
		defer func() { <-p.slots }()
		ctx, cancel := context.WithTimeout(p.ctx, pushTimeout)
		defer cancel()
		p.Send(ctx, n)
	}()
}

// Close cancels the pushes in flight and waits for them. Later SendAsync calls
// do nothing.
func (p *Pusher) Close() {
	p.mu.Lock()
	p.shut = true
	p.mu.Unlock()
	p.cancel()
	p.wg.Wait()
}

// checkPushEndpoint accepts an https URL on a public address. The push service
// of a browser is always https, and a plain http endpoint would send the
// payload in the clear.
func checkPushEndpoint(endpoint string) error {
	u, err := url.Parse(endpoint)
	if err != nil || !strings.EqualFold(u.Scheme, "https") {
		return errors.New("the endpoint must be an https address")
	}
	return httpclient.ValidateExternalURL(endpoint)
}

func generateVAPID() (string, string, error) {
	priv, pub, err := webpush.GenerateVAPIDKeys()
	return priv, pub, err
}

// PublicKey returns the server's VAPID public key, which a browser needs to
// subscribe. It makes the pair on first use.
func (p *Pusher) PublicKey(ctx context.Context) (string, error) {
	_, pub, err := p.o.Store.EnsureVAPID(ctx, generateVAPID)
	return pub, err
}

// Subscribe stores a browser's subscription after checking its endpoint.
func (p *Pusher) Subscribe(ctx context.Context, principal, endpoint, p256dh, auth string) error {
	if endpoint == "" || p256dh == "" || auth == "" || len(endpoint) > 1024 {
		return fmt.Errorf("%w: endpoint, p256dh and auth are required", ErrPushEndpoint)
	}
	if err := p.o.CheckEndpoint(endpoint); err != nil {
		return fmt.Errorf("%w: %v", ErrPushEndpoint, err)
	}
	return p.o.Store.Subscribe(ctx, PushSubscription{Principal: principal, Endpoint: endpoint, P256dh: p256dh, Auth: auth, Created: time.Now().UTC()})
}

// Unsubscribe removes the principal's subscription.
func (p *Pusher) Unsubscribe(ctx context.Context, principal, endpoint string) error {
	return p.o.Store.Unsubscribe(ctx, principal, endpoint)
}

type pushPayload struct {
	Title string `json:"title"`
	Link  string `json:"link"`
}

// Send pushes n to every browser of its owner. A subscription that the push
// service reports gone (404 or 410) is dropped. A failure is logged and does
// not stop the others.
func (p *Pusher) Send(ctx context.Context, n Notification) {
	subs, err := p.o.Store.ForPrincipal(ctx, n.Principal)
	if err != nil || len(subs) == 0 {
		if err != nil {
			p.o.Logger.Warn("notify: could not list push subscriptions", "error", err)
		}
		return
	}
	priv, pub, err := p.o.Store.EnsureVAPID(ctx, generateVAPID)
	if err != nil {
		p.o.Logger.Warn("notify: no VAPID keys", "error", err)
		return
	}
	body, _ := json.Marshal(pushPayload{Title: n.Title, Link: n.Link})
	for _, s := range subs {
		resp, err := webpush.SendNotificationWithContext(ctx, body,
			&webpush.Subscription{Endpoint: s.Endpoint, Keys: webpush.Keys{P256dh: s.P256dh, Auth: s.Auth}},
			&webpush.Options{HTTPClient: p.o.Client, Subscriber: p.o.Subscriber, VAPIDPublicKey: pub, VAPIDPrivateKey: priv, TTL: 3600, Urgency: webpush.UrgencyNormal})
		if err != nil {
			p.o.Logger.Warn("notify: push failed", "error", err)
			continue
		}
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 4096))
		_ = resp.Body.Close()
		switch {
		case resp.StatusCode == http.StatusNotFound || resp.StatusCode == http.StatusGone:
			if err := p.o.Store.Drop(ctx, s.Endpoint); err != nil {
				p.o.Logger.Warn("notify: could not drop a dead push subscription", "error", err)
			}
		case resp.StatusCode >= 300:
			p.o.Logger.Warn("notify: the push service refused a message", "status", resp.StatusCode)
		}
	}
}
