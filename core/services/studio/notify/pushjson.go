package notify

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"sync"

	"github.com/mudler/LocalAI/pkg/atomicfile"
)

// jsonPush keeps subscriptions and the VAPID pair in memory and, with a
// directory, in the file push.json (mode 0600, rewritten atomically). One
// process owns the directory.
type jsonPush struct {
	mu   sync.Mutex
	path string
	d    pushData
}

type pushData struct {
	Subs    []PushSubscription `json:"subs"`
	Private string             `json:"vapid_private,omitempty"`
	Public  string             `json:"vapid_public,omitempty"`
}

// NewJSONPush returns a PushStore. With an empty dir it keeps everything in
// memory.
func NewJSONPush(dir string) (PushStore, error) {
	s := &jsonPush{}
	if dir == "" {
		return s, nil
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}
	s.path = filepath.Join(dir, "push.json")
	raw, err := os.ReadFile(s.path)
	if os.IsNotExist(err) {
		return s, nil
	}
	if err != nil {
		return nil, err
	}
	if err := json.Unmarshal(raw, &s.d); err != nil {
		return nil, fmt.Errorf("notify: read %s: %w", s.path, err)
	}
	return s, nil
}

func (s *jsonPush) change(f func(*pushData)) error {
	next := pushData{Subs: slices.Clone(s.d.Subs), Private: s.d.Private, Public: s.d.Public}
	f(&next)
	if s.path != "" {
		raw, err := json.Marshal(next)
		if err != nil {
			return err
		}
		if err := atomicfile.Write(s.path, raw, 0o600); err != nil {
			return err
		}
	}
	s.d = next
	return nil
}

func (s *jsonPush) Subscribe(_ context.Context, sub PushSubscription) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.change(func(d *pushData) {
		d.Subs = slices.DeleteFunc(d.Subs, func(x PushSubscription) bool { return x.Endpoint == sub.Endpoint })
		d.Subs = append(d.Subs, sub)
		// Keep the newest MaxPushSubscriptions of this principal.
		n := 0
		for _, x := range d.Subs {
			if x.Principal == sub.Principal {
				n++
			}
		}
		for i := 0; n > MaxPushSubscriptions && i < len(d.Subs); {
			if d.Subs[i].Principal == sub.Principal {
				d.Subs = slices.Delete(d.Subs, i, i+1)
				n--
				continue
			}
			i++
		}
	})
}

func (s *jsonPush) Unsubscribe(_ context.Context, principal, endpoint string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.change(func(d *pushData) {
		d.Subs = slices.DeleteFunc(d.Subs, func(x PushSubscription) bool { return x.Principal == principal && x.Endpoint == endpoint })
	})
}

func (s *jsonPush) ForPrincipal(_ context.Context, principal string) ([]PushSubscription, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []PushSubscription
	for _, x := range s.d.Subs {
		if x.Principal == principal {
			out = append(out, x)
		}
	}
	return out, nil
}

func (s *jsonPush) Drop(_ context.Context, endpoint string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.change(func(d *pushData) {
		d.Subs = slices.DeleteFunc(d.Subs, func(x PushSubscription) bool { return x.Endpoint == endpoint })
	})
}

func (s *jsonPush) EnsureVAPID(_ context.Context, generate func() (string, string, error)) (string, string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.d.Private != "" {
		return s.d.Private, s.d.Public, nil
	}
	priv, pub, err := generate()
	if err != nil {
		return "", "", err
	}
	if err := s.change(func(d *pushData) { d.Private, d.Public = priv, pub }); err != nil {
		return "", "", err
	}
	return priv, pub, nil
}
