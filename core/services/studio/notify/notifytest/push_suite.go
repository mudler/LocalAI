package notifytest

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/mudler/LocalAI/core/services/studio/notify"
)

// NewPushStore returns an empty push store.
type NewPushStore func(t *testing.T) notify.PushStore

func sub(p, endpoint string, at time.Time) notify.PushSubscription {
	return notify.PushSubscription{Principal: p, Endpoint: endpoint, P256dh: "key-" + endpoint, Auth: "auth-" + endpoint, Created: at}
}

func endpoints(ss []notify.PushSubscription) []string {
	out := make([]string, len(ss))
	for i, s := range ss {
		out[i] = s.Endpoint
	}
	return out
}

// RunPush checks a PushStore.
func RunPush(t *testing.T, newStore NewPushStore) {
	ctx := context.Background()
	t.Run("SubscribeListUnsubscribe", func(t *testing.T) {
		s := newStore(t)
		if err := s.Subscribe(ctx, sub("u1", "https://push/a", t0)); err != nil {
			t.Fatal(err)
		}
		_ = s.Subscribe(ctx, sub("u1", "https://push/b", t0.Add(time.Second)))
		_ = s.Subscribe(ctx, sub("u2", "https://push/c", t0))
		got, err := s.ForPrincipal(ctx, "u1")
		if err != nil || !same(endpoints(got), []string{"https://push/a", "https://push/b"}) || got[0].P256dh != "key-https://push/a" || got[0].Auth != "auth-https://push/a" {
			t.Fatalf("got %+v, %v", got, err)
		}
		_ = s.Unsubscribe(ctx, "u2", "https://push/a") // not u2's
		if got, _ := s.ForPrincipal(ctx, "u1"); len(got) != 2 {
			t.Fatalf("another user removed a subscription: %+v", got)
		}
		_ = s.Unsubscribe(ctx, "u1", "https://push/a")
		if got, _ := s.ForPrincipal(ctx, "u1"); !same(endpoints(got), []string{"https://push/b"}) {
			t.Fatalf("after unsubscribe: %+v", got)
		}
	})
	t.Run("AnEndpointBelongsToTheLastUserToSubscribeIt", func(t *testing.T) {
		s := newStore(t)
		_ = s.Subscribe(ctx, sub("u1", "https://push/a", t0))
		_ = s.Subscribe(ctx, sub("u2", "https://push/a", t0.Add(time.Second)))
		if got, _ := s.ForPrincipal(ctx, "u1"); len(got) != 0 {
			t.Fatalf("u1 still has %+v", got)
		}
		if got, _ := s.ForPrincipal(ctx, "u2"); len(got) != 1 {
			t.Fatalf("u2 has %+v", got)
		}
	})
	t.Run("DropRemovesWhoeverOwnsIt", func(t *testing.T) {
		s := newStore(t)
		_ = s.Subscribe(ctx, sub("u1", "https://push/a", t0))
		if err := s.Drop(ctx, "https://push/a"); err != nil {
			t.Fatal(err)
		}
		if got, _ := s.ForPrincipal(ctx, "u1"); len(got) != 0 {
			t.Fatalf("after drop: %+v", got)
		}
	})
	t.Run("TrimmedToTheNewest", func(t *testing.T) {
		s := newStore(t)
		for i := 0; i < notify.MaxPushSubscriptions+3; i++ {
			_ = s.Subscribe(ctx, sub("u1", fmt.Sprintf("https://push/%02d", i), t0.Add(time.Duration(i)*time.Second)))
		}
		_ = s.Subscribe(ctx, sub("u2", "https://push/other", t0))
		got, _ := s.ForPrincipal(ctx, "u1")
		if len(got) != notify.MaxPushSubscriptions || got[0].Endpoint != "https://push/03" {
			t.Fatalf("kept %v", endpoints(got))
		}
		if other, _ := s.ForPrincipal(ctx, "u2"); len(other) != 1 {
			t.Fatalf("trimming u1 touched u2: %+v", other)
		}
	})
	t.Run("VAPIDKeysAreMadeOnceEvenWhenCallsRace", func(t *testing.T) {
		s := newStore(t)
		var mu sync.Mutex
		made := 0
		gen := func() (string, string, error) {
			mu.Lock()
			made++
			n := made
			mu.Unlock()
			time.Sleep(5 * time.Millisecond)
			return fmt.Sprintf("priv-%d", n), fmt.Sprintf("pub-%d", n), nil
		}
		results := make(chan [2]string, 8)
		var wg sync.WaitGroup
		for i := 0; i < 8; i++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				p, q, err := s.EnsureVAPID(ctx, gen)
				if err != nil {
					t.Error(err)
				}
				results <- [2]string{p, q}
			}()
		}
		wg.Wait()
		close(results)
		var first [2]string
		for r := range results {
			if first == ([2]string{}) {
				first = r
			}
			if r != first {
				t.Fatalf("two callers got different keys: %v and %v", first, r)
			}
		}
		again, _, _ := s.EnsureVAPID(ctx, func() (string, string, error) { return "", "", errors.New("must not generate again") })
		if again != first[0] {
			t.Fatalf("a later call got %q, want %q", again, first[0])
		}
	})
}

// SeedPush stores a subscription and the keys; CheckPushSeed finds them in
// another store on the same data.
func SeedPush(t *testing.T, s notify.PushStore) {
	t.Helper()
	_ = s.Subscribe(context.Background(), sub("u1", "https://push/seed", t0))
	if _, _, err := s.EnsureVAPID(context.Background(), func() (string, string, error) { return "kept-private", "kept-public", nil }); err != nil {
		t.Fatal(err)
	}
}

// CheckPushSeed checks what SeedPush wrote.
func CheckPushSeed(t *testing.T, s notify.PushStore) {
	t.Helper()
	got, err := s.ForPrincipal(context.Background(), "u1")
	if err != nil || len(got) != 1 || got[0].Endpoint != "https://push/seed" {
		t.Fatalf("subscriptions: %+v %v", got, err)
	}
	priv, pub, err := s.EnsureVAPID(context.Background(), func() (string, string, error) { return "new", "new", nil })
	if err != nil || priv != "kept-private" || pub != "kept-public" {
		t.Fatalf("keys: %q %q %v", priv, pub, err)
	}
}
