// Package connectorstest is the contract suite every connectors.Store passes.
//
//nolint:forbidigo // reusable contract suite driven by the caller's *testing.T
package connectorstest

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/mudler/LocalAI/core/services/studio/connectors"
)

// NewStore returns an empty store.
type NewStore func(t *testing.T) connectors.Store

var t0 = time.Date(2026, 10, 27, 9, 0, 0, 0, time.UTC)

func binding(id, owner, name string) connectors.Binding {
	return connectors.Binding{ID: id, Owner: owner, Kind: connectors.KindTelegram, Name: name, AgentID: "ag1", Enabled: true,
		Allow: []string{"123", "@alice"}, NotifyChat: "456", Token: []byte("sealed-bytes-" + id), Created: t0, Updated: t0}
}

// Run checks the store.
func Run(t *testing.T, newStore NewStore) {
	ctx := context.Background()
	t.Run("RoundTripAndOwnerScope", func(t *testing.T) {
		s := newStore(t)
		if err := s.PutBinding(ctx, binding("b1", "u1", "Bot")); err != nil {
			t.Fatal(err)
		}
		_ = s.PutBinding(ctx, binding("b2", "u2", "Other"))
		got, err := s.GetBinding(ctx, "b1")
		if err != nil || got.Name != "Bot" || got.AgentID != "ag1" || !got.Enabled || len(got.Allow) != 2 || got.NotifyChat != "456" || !bytes.Equal(got.Token, []byte("sealed-bytes-b1")) {
			t.Fatalf("got %+v, %v", got, err)
		}
		if l, _ := s.ListBindings(ctx, "u1"); len(l) != 1 || l[0].ID != "b1" {
			t.Fatalf("u1 sees %+v", l)
		}
		if _, err := s.GetBinding(ctx, "nope"); !errors.Is(err, connectors.ErrNotFound) {
			t.Fatalf("missing: %v", err)
		}
		b := binding("b1", "u1", "Bot 2")
		b.Enabled, b.Allow = false, nil
		_ = s.PutBinding(ctx, b)
		got, _ = s.GetBinding(ctx, "b1")
		if got.Name != "Bot 2" || got.Enabled || len(got.Allow) != 0 {
			t.Fatalf("after replace: %+v", got)
		}
	})
	t.Run("EnabledBindings", func(t *testing.T) {
		s := newStore(t)
		a, b := binding("a", "u1", "A"), binding("b", "u2", "B")
		b.Enabled = false
		_ = s.PutBinding(ctx, a)
		_ = s.PutBinding(ctx, b)
		l, err := s.EnabledBindings(ctx)
		if err != nil || len(l) != 1 || l[0].ID != "a" {
			t.Fatalf("enabled = %+v, %v", l, err)
		}
	})
	t.Run("ReturnedValuesAreCopies", func(t *testing.T) {
		s := newStore(t)
		_ = s.PutBinding(ctx, binding("b1", "u1", "Bot"))
		got, _ := s.GetBinding(ctx, "b1")
		got.Allow[0], got.Token[0] = "999", 'X'
		again, _ := s.GetBinding(ctx, "b1")
		if again.Allow[0] != "123" || again.Token[0] != 's' {
			t.Fatalf("a caller changed the stored binding: %+v", again)
		}
	})
	t.Run("OffsetAndDelete", func(t *testing.T) {
		s := newStore(t)
		_ = s.PutBinding(ctx, binding("b1", "u1", "Bot"))
		if _, ok, _ := s.Offset(ctx, "b1"); ok {
			t.Fatal("an offset before any was saved")
		}
		_ = s.SetOffset(ctx, "b1", 42)
		if off, ok, err := s.Offset(ctx, "b1"); err != nil || !ok || off != 42 {
			t.Fatalf("offset = %d %v %v", off, ok, err)
		}
		_ = s.RecordSender(ctx, "b1", "123", "Alice", true, t0)
		_ = s.SetChat(ctx, "b1", "123", "chat9")
		if err := s.DeleteBinding(ctx, "b1"); err != nil {
			t.Fatal(err)
		}
		if err := s.DeleteBinding(ctx, "b1"); err != nil {
			t.Fatalf("second delete: %v", err)
		}
		if _, ok, _ := s.Offset(ctx, "b1"); ok {
			t.Fatal("the offset outlived its binding")
		}
		if l, _ := s.Senders(ctx, "b1", 0); len(l) != 0 {
			t.Fatalf("senders outlived: %+v", l)
		}
		if _, ok, _ := s.ChatOf(ctx, "b1", "123"); ok {
			t.Fatal("the chat map outlived its binding")
		}
	})
	t.Run("SenderLog", func(t *testing.T) {
		s := newStore(t)
		_ = s.RecordSender(ctx, "b1", "1", "One", false, t0)
		_ = s.RecordSender(ctx, "b1", "1", "One renamed", false, t0.Add(time.Minute))
		_ = s.RecordSender(ctx, "b1", "2", "Two", true, t0.Add(2*time.Minute))
		_ = s.RecordSender(ctx, "b2", "9", "Other bot", false, t0)
		l, err := s.Senders(ctx, "b1", 0)
		if err != nil || len(l) != 2 || l[0].ID != "2" || !l[0].Allowed || l[1].ID != "1" || l[1].Count != 2 || l[1].Name != "One renamed" || !l[1].First.Equal(t0) || !l[1].Last.Equal(t0.Add(time.Minute)) {
			t.Fatalf("senders = %+v, %v", l, err)
		}
		if one, _ := s.Senders(ctx, "b1", 1); len(one) != 1 || one[0].ID != "2" {
			t.Fatalf("limit 1 = %+v", one)
		}
		for i := 0; i < connectors.MaxSendersKept+5; i++ {
			_ = s.RecordSender(ctx, "b3", fmt.Sprint(i), "x", false, t0.Add(time.Duration(i)*time.Second))
		}
		all, _ := s.Senders(ctx, "b3", 0)
		if len(all) != connectors.MaxSendersKept || all[0].ID != fmt.Sprint(connectors.MaxSendersKept+4) {
			t.Fatalf("kept %d, newest %q", len(all), all[0].ID)
		}
	})
	t.Run("ChatMap", func(t *testing.T) {
		s := newStore(t)
		if _, ok, _ := s.ChatOf(ctx, "b1", "1"); ok {
			t.Fatal("a chat before any was set")
		}
		_ = s.SetChat(ctx, "b1", "1", "c1")
		_ = s.SetChat(ctx, "b1", "2", "c2")
		_ = s.SetChat(ctx, "b1", "1", "c3")
		if id, ok, _ := s.ChatOf(ctx, "b1", "1"); !ok || id != "c3" {
			t.Fatalf("chat of 1 = %q %v", id, ok)
		}
		if id, _, _ := s.ChatOf(ctx, "b1", "2"); id != "c2" {
			t.Fatalf("chat of 2 = %q", id)
		}
	})
	t.Run("KeyIsMadeOnceEvenWhenCallsRace", func(t *testing.T) {
		s := newStore(t)
		var mu sync.Mutex
		n := 0
		gen := func() ([]byte, error) {
			mu.Lock()
			n++
			v := byte(n)
			mu.Unlock()
			time.Sleep(5 * time.Millisecond)
			return bytes.Repeat([]byte{v}, connectors.KeySize), nil
		}
		res := make(chan []byte, 8)
		var wg sync.WaitGroup
		for i := 0; i < 8; i++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				k, err := s.EnsureKey(ctx, gen)
				if err != nil {
					t.Error(err)
				}
				res <- k
			}()
		}
		wg.Wait()
		close(res)
		var first []byte
		for k := range res {
			if first == nil {
				first = k
			}
			if !bytes.Equal(k, first) {
				t.Fatal("two callers got different keys")
			}
		}
		again, _ := s.EnsureKey(ctx, func() ([]byte, error) { return nil, errors.New("must not generate again") })
		if !bytes.Equal(again, first) {
			t.Fatal("a later call got another key")
		}
	})
}

// Seed stores a binding and a key; CheckSeed finds them in another store on the same data.
func Seed(t *testing.T, s connectors.Store) {
	t.Helper()
	ctx := context.Background()
	_ = s.PutBinding(ctx, binding("seed", "u1", "Seeded"))
	_ = s.SetOffset(ctx, "seed", 7)
	_ = s.RecordSender(ctx, "seed", "1", "One", true, t0)
	_ = s.SetChat(ctx, "seed", "1", "chat1")
	if _, err := s.EnsureKey(ctx, func() ([]byte, error) { return bytes.Repeat([]byte{9}, connectors.KeySize), nil }); err != nil {
		t.Fatal(err)
	}
}

// CheckSeed checks what Seed wrote.
func CheckSeed(t *testing.T, s connectors.Store) {
	t.Helper()
	ctx := context.Background()
	b, err := s.GetBinding(ctx, "seed")
	if err != nil || b.Name != "Seeded" {
		t.Fatalf("binding: %+v %v", b, err)
	}
	if off, ok, _ := s.Offset(ctx, "seed"); !ok || off != 7 {
		t.Fatalf("offset = %d %v", off, ok)
	}
	if l, _ := s.Senders(ctx, "seed", 0); len(l) != 1 {
		t.Fatalf("senders = %+v", l)
	}
	if id, ok, _ := s.ChatOf(ctx, "seed", "1"); !ok || id != "chat1" {
		t.Fatalf("chat = %q %v", id, ok)
	}
	k, err := s.EnsureKey(ctx, func() ([]byte, error) { return bytes.Repeat([]byte{1}, connectors.KeySize), nil })
	if err != nil || !bytes.Equal(k, bytes.Repeat([]byte{9}, connectors.KeySize)) {
		t.Fatalf("key: %v %v", k, err)
	}
}
