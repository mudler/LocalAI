package plugins_test

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/mudler/nib/plugin/claudeplugin"

	"github.com/mudler/LocalAI/core/services/plugins/fetch"
)

func TestPlugins(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Plugins service suite")
}

// mkt is what the fake fetcher answers for one marketplace location.
type mkt struct {
	body string
	sha  string
	err  error
}

// fakeFetcher serves marketplaces, plugin folders and refs from maps. It
// counts the marketplace fetches.
type fakeFetcher struct {
	mu      sync.Mutex
	mkts    map[string]mkt
	refs    map[string]string // gitURL + "#" + ref -> sha
	plugins map[string]func(sha string) (dir, got string, err error)
	calls   map[string]int
	delay   time.Duration
}

func newFakeFetcher() *fakeFetcher {
	return &fakeFetcher{
		mkts: map[string]mkt{}, refs: map[string]string{}, calls: map[string]int{},
		plugins: map[string]func(string) (string, string, error){},
	}
}

func (f *fakeFetcher) setMarketplace(location, body, sha string, err error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.mkts[location] = mkt{body, sha, err}
}

func (f *fakeFetcher) count(location string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.calls[location]
}

func (f *fakeFetcher) FetchMarketplace(_ context.Context, _, location, _ string) ([]byte, string, error) {
	f.mu.Lock()
	f.calls[location]++
	m, ok := f.mkts[location]
	d := f.delay
	f.mu.Unlock()
	if d > 0 {
		time.Sleep(d)
	}
	if !ok {
		return nil, "", fmt.Errorf("no marketplace at %s", location)
	}
	return []byte(m.body), m.sha, m.err
}

func (f *fakeFetcher) ResolveRef(_ context.Context, gitURL, ref string) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if sha, ok := f.refs[gitURL+"#"+ref]; ok {
		return sha, nil
	}
	return "", fmt.Errorf("no ref %q at %s", ref, gitURL)
}

func (f *fakeFetcher) FetchPlugin(_ context.Context, _ fetch.MarketplaceSource, _ *claudeplugin.Marketplace, e *claudeplugin.Entry, sha string) (*fetch.Fetched, error) {
	f.mu.Lock()
	fn, ok := f.plugins[e.Name]
	f.mu.Unlock()
	if !ok {
		return nil, fmt.Errorf("no plugin %s", e.Name)
	}
	dir, got, err := fn(sha)
	if err != nil {
		return nil, err
	}
	return &fetch.Fetched{Dir: dir, SHA: got, Source: e.Source, Done: func() {}}, nil
}

const (
	shaA = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	shaB = "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
	shaC = "cccccccccccccccccccccccccccccccccccccccc"
)
