// SPDX-License-Identifier: MIT

// Package settings keeps the administrator's choices of the Web add-on: the
// order in which the search providers are tried, their keys, the Google
// search engine ID and the SearXNG address.
//
// The file is settings.json in the add-on's data folder, mode 0600, written
// atomically. A key is a secret: it is written there and handed to its
// provider, and nothing else. View, which is what the Settings page reads,
// says only whether a key is set.
package settings

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"

	"github.com/mudler/LocalAI/addons/internal/atomicfile"
	"github.com/mudler/LocalAI/addons/web/internal/providers"
)

// Settings are the saved choices.
type Settings struct {
	// Order lists the providers to try, first to last. A provider that is not
	// ready (no key, no address) is skipped.
	Order []string `json:"order"`
	// Keys maps a keyed provider to its key.
	Keys map[string]string `json:"keys,omitempty"`
	// GoogleEngine is the Programmable Search Engine ID (cx).
	GoogleEngine string `json:"googleEngine,omitempty"`
	// SearXNGURL is the address of a SearXNG instance.
	SearXNGURL string `json:"searxngUrl,omitempty"`
}

// Defaults are the settings before any choice: every provider in the default
// order, so only DuckDuckGo is ready.
func Defaults() Settings { return Settings{Order: slices.Clone(providers.Names)} }

func (s Settings) clone() Settings {
	out := Settings{Order: slices.Clone(s.Order), GoogleEngine: s.GoogleEngine, SearXNGURL: s.SearXNGURL}
	if len(s.Keys) > 0 {
		out.Keys = make(map[string]string, len(s.Keys))
		for k, v := range s.Keys {
			out.Keys[k] = v
		}
	}
	return out
}

// Ready reports whether a provider has what it needs, and if not, what is
// missing.
func (s Settings) Ready(name string) (bool, string) {
	switch name {
	case providers.Brave, providers.Tavily:
		if s.Keys[name] == "" {
			return false, "no key"
		}
	case providers.Google:
		switch {
		case s.Keys[name] == "" && s.GoogleEngine == "":
			return false, "no key and no search engine ID"
		case s.Keys[name] == "":
			return false, "no key"
		case s.GoogleEngine == "":
			return false, "no search engine ID"
		}
	case providers.SearXNG:
		if s.SearXNGURL == "" {
			return false, "no address"
		}
	case providers.DuckDuckGo:
	default:
		return false, "unknown provider"
	}
	return true, ""
}

// Change is one save of the Settings page. A nil field keeps its value.
type Change struct {
	Order []string `json:"order,omitempty"`
	// Keys sets the key of each named provider. An empty value keeps the key.
	Keys map[string]string `json:"keys,omitempty"`
	// Clear removes the keys of the named providers.
	Clear        []string `json:"clear,omitempty"`
	GoogleEngine *string  `json:"googleEngine,omitempty"`
	SearXNGURL   *string  `json:"searxngUrl,omitempty"`
}

// ErrInvalid wraps every refusal of a Change. The message never holds a key.
var ErrInvalid = errors.New("the settings are not valid")

func known(name string) bool { return slices.Contains(providers.Names, name) }

// apply returns s with c applied, or the reason c is refused.
func apply(s Settings, c Change) (Settings, error) {
	out := s.clone()
	if c.Order != nil {
		if len(c.Order) == 0 {
			return s, fmt.Errorf("%w: choose at least one provider", ErrInvalid)
		}
		seen := map[string]bool{}
		for _, n := range c.Order {
			if !known(n) || seen[n] {
				return s, fmt.Errorf("%w: the order names an unknown or repeated provider", ErrInvalid)
			}
			seen[n] = true
		}
		out.Order = slices.Clone(c.Order)
	}
	for _, n := range c.Clear {
		if !providers.Keyed(n) {
			return s, fmt.Errorf("%w: %q has no key", ErrInvalid, n)
		}
		delete(out.Keys, n)
	}
	for n, k := range c.Keys {
		if !providers.Keyed(n) {
			return s, fmt.Errorf("%w: %q has no key", ErrInvalid, n)
		}
		k = strings.TrimSpace(k)
		if k == "" {
			continue
		}
		if !providers.ValidKey(k) {
			// The key itself is never repeated in the answer.
			return s, fmt.Errorf("%w: the key of %s must be 8 to 256 printable characters without spaces", ErrInvalid, n)
		}
		if out.Keys == nil {
			out.Keys = map[string]string{}
		}
		out.Keys[n] = k
	}
	if len(out.Keys) == 0 {
		out.Keys = nil
	}
	if c.GoogleEngine != nil {
		v := strings.TrimSpace(*c.GoogleEngine)
		if v != "" && !providers.ValidEngineID(v) {
			return s, fmt.Errorf("%w: the search engine ID has letters, digits, colons, dashes and underscores only", ErrInvalid)
		}
		out.GoogleEngine = v
	}
	if c.SearXNGURL != nil {
		v := strings.TrimSpace(*c.SearXNGURL)
		if v != "" {
			u, err := url.Parse(v)
			if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" || u.User != nil {
				return s, fmt.Errorf("%w: the SearXNG address must be an http or https URL such as https://searx.example.org", ErrInvalid)
			}
		}
		out.SearXNGURL = v
	}
	return out, nil
}

// Store holds the settings in memory and on disk. It is safe for concurrent
// use.
type Store struct {
	path string
	mu   sync.Mutex
	set  Settings
	rev  int
	note string
}

// Open reads the saved settings. A missing file gives the defaults; a broken
// one too, and the view says so.
func Open(dir string) (*Store, error) {
	if dir == "" {
		return nil, errors.New("settings: no data folder")
	}
	s := &Store{path: filepath.Join(dir, "settings.json"), set: Defaults()}
	raw, err := os.ReadFile(s.path)
	switch {
	case errors.Is(err, os.ErrNotExist):
	case err != nil:
		return nil, err
	default:
		var f Settings
		if json.Unmarshal(raw, &f) != nil {
			s.note = "The saved settings could not be read, so the defaults apply."
			break
		}
		// Check the file with the rules of a change, so a hand-edited file
		// cannot hold what the page could not save.
		got, err := apply(Defaults(), Change{Order: orNil(f.Order), Keys: f.Keys, GoogleEngine: &f.GoogleEngine, SearXNGURL: &f.SearXNGURL})
		if err != nil {
			s.note = "The saved settings are not valid, so the defaults apply."
			break
		}
		s.set = got
	}
	return s, nil
}

func orNil(l []string) []string {
	if len(l) == 0 {
		return nil
	}
	return l
}

// Get returns a copy of the settings and their revision, which changes with
// every save.
func (s *Store) Get() (Settings, int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.set.clone(), s.rev
}

// Apply saves a change. Nothing changes when it is refused or cannot be
// written.
func (s *Store) Apply(c Change) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	next, err := apply(s.set, c)
	if err != nil {
		return err
	}
	raw, err := json.Marshal(next)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(s.path), 0o700); err != nil {
		return errors.New("the settings could not be saved")
	}
	if err := atomicfile.Write(s.path, raw, 0o600); err != nil {
		return errors.New("the settings could not be saved")
	}
	s.set, s.note = next, ""
	s.rev++
	return nil
}

// ProviderView is one provider as the Settings page shows it.
type ProviderView struct {
	Name string `json:"name"`
	// Keyed says that the provider needs a key; KeySet that one is saved.
	Keyed  bool `json:"keyed"`
	KeySet bool `json:"keySet"`
	Ready  bool `json:"ready"`
	// Missing says what the provider still needs, when it is not ready.
	Missing string `json:"missing,omitempty"`
}

// View is what the Settings page reads. It holds no key.
type View struct {
	Order        []string       `json:"order"`
	Providers    []ProviderView `json:"providers"`
	GoogleEngine string         `json:"googleEngine"`
	SearXNGURL   string         `json:"searxngUrl"`
	Note         string         `json:"note,omitempty"`
}

// View returns the settings without the keys.
func (s *Store) View() View {
	s.mu.Lock()
	defer s.mu.Unlock()
	v := View{Order: slices.Clone(s.set.Order), GoogleEngine: s.set.GoogleEngine, SearXNGURL: s.set.SearXNGURL, Note: s.note}
	for _, n := range providers.Names {
		ready, missing := s.set.Ready(n)
		v.Providers = append(v.Providers, ProviderView{Name: n, Keyed: providers.Keyed(n), KeySet: s.set.Keys[n] != "", Ready: ready, Missing: missing})
	}
	return v
}
