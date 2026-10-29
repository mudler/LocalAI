package collections

import (
	"context"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/mudler/LocalAI/core/config"
)

const (
	userA = "11111111-1111-4111-8111-111111111111"
	userB = "22222222-2222-4222-8222-222222222222"
)

func newService(t *testing.T) (*Service, string) {
	t.Helper()
	dir := t.TempDir()
	cfg := config.NewApplicationConfig()
	cfg.DataPath = dir
	cfg.APIAddress = "127.0.0.1:8080"
	s, err := New(cfg, nil)
	if err != nil {
		t.Fatal(err)
	}
	return s, dir
}

func TestEachUserHasCollectionsOfTheirOwn(t *testing.T) {
	s, _ := newService(t)
	if err := s.CreateForUser(userA, "notes"); err != nil {
		t.Fatal(err)
	}
	if err := s.CreateForUser(userB, "other"); err != nil {
		t.Fatal(err)
	}
	a, _ := s.ListForUser(userA)
	b, _ := s.ListForUser(userB)
	if len(a) != 1 || a[0] != "notes" || len(b) != 1 || b[0] != "other" {
		t.Fatalf("user A has %v, user B has %v", a, b)
	}
	if root, err := s.ListForUser(""); err != nil || len(root) != 0 {
		t.Fatalf("the root has %v, %v", root, err)
	}
}

func TestTheRootAndTheUsersLiveInTheirOwnFolders(t *testing.T) {
	s, dir := newService(t)
	_ = s.CreateForUser(userA, "notes")
	_ = s.CreateForUser("", "shared")
	for _, p := range []string{
		filepath.Join(dir, "users", userA, "collections"),
		filepath.Join(dir, "users", userA, "assets"),
		filepath.Join(dir, "collections"),
	} {
		if st, err := os.Stat(p); err != nil || !st.IsDir() {
			t.Errorf("%s: %v", p, err)
		}
	}
	ids, err := s.UserIDs()
	if err != nil || len(ids) != 1 || ids[0] != userA {
		t.Fatalf("user dirs = %v, %v", ids, err)
	}
}

func TestAUserIDThatIsNotAUUIDIsRefused(t *testing.T) {
	s, _ := newService(t)
	for _, id := range []string{"../x", "a/b", "not-a-uuid", "..", `a\b`} {
		if _, err := s.Backend(id); err == nil {
			t.Errorf("user ID %q was accepted", id)
		}
	}
	if err := ValidateUserID(""); err != nil {
		t.Fatalf("the root user ID: %v", err)
	}
}

func TestTheBackendOfAUserIsMadeOnce(t *testing.T) {
	s, _ := newService(t)
	one, _ := s.Backend(userA)
	two, _ := s.Backend(userA)
	if one != two {
		t.Fatal("a second call made another backend")
	}
}

// roundTripFunc is an http.RoundTripper that answers in memory.
type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestEmbeddingsGoThroughTheClientTheHostGives(t *testing.T) {
	dir := t.TempDir()
	cfg := config.NewApplicationConfig()
	cfg.DataPath = dir
	// Nothing listens here: a call that leaves the process cannot succeed.
	cfg.APIAddress = "127.0.0.1:1"
	cfg.Collections.VectorEngine = "chromem"
	cfg.Collections.EmbeddingModel = "embed"

	var calls atomic.Int32
	client := &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		calls.Add(1)
		if got := r.Header.Get("Authorization"); got != "" {
			t.Errorf("the in-process call carries a credential: %q", got)
		}
		body := `{"data":[{"embedding":[1,0,0],"index":0}],"model":"embed","usage":{}}`
		return &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": {"application/json"}},
			Body: io.NopCloser(strings.NewReader(body)), Request: r}, nil
	})}
	s, err := New(cfg, nil, WithHTTPClient(client))
	if err != nil {
		t.Fatal(err)
	}
	if err := s.CreateForUser(userA, "notes"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.UploadForUser(context.Background(), userA, "notes", "a.txt", strings.NewReader("hello there")); err != nil {
		t.Fatalf("upload: %v", err)
	}
	if calls.Load() == 0 {
		t.Fatal("the embedding call did not use the client of the host")
	}
}

func TestAConfiguredCollectionsKeyKeepsTheNetworkPath(t *testing.T) {
	cfg := config.NewApplicationConfig()
	cfg.DataPath = t.TempDir()
	cfg.Collections.APIKey = "operator-key"
	s, err := New(cfg, nil, WithHTTPClient(&http.Client{}))
	if err != nil {
		t.Fatal(err)
	}
	if s.httpClient != nil {
		t.Fatal("an operator key was set, but the service still uses the in-process client")
	}
}

func TestResetEmptiesACollectionAndKeepsIt(t *testing.T) {
	cfg := config.NewApplicationConfig()
	cfg.DataPath = t.TempDir()
	cfg.Collections.VectorEngine = "chromem"
	cfg.Collections.EmbeddingModel = "embed"
	client := &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		body := `{"data":[{"embedding":[1,0,0],"index":0}],"model":"embed","usage":{}}`
		return &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": {"application/json"}},
			Body: io.NopCloser(strings.NewReader(body)), Request: r}, nil
	})}
	s, err := New(cfg, nil, WithHTTPClient(client))
	if err != nil {
		t.Fatal(err)
	}
	if err := s.CreateForUser(userA, "notes"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.UploadForUser(context.Background(), userA, "notes", "a.txt", strings.NewReader("hello there")); err != nil {
		t.Fatal(err)
	}
	if err := s.ResetForUser(userA, "notes"); err != nil {
		t.Fatalf("reset: %v", err)
	}
	names, err := s.ListForUser(userA)
	if err != nil || len(names) != 1 || names[0] != "notes" {
		t.Fatalf("collections after the reset = %v, %v; want [notes]", names, err)
	}
	entries, err := s.ListEntriesForUser(userA, "notes")
	if err != nil || len(entries) != 0 {
		t.Fatalf("entries after the reset = %v, %v; want none", entries, err)
	}
	// It takes uploads again.
	if _, err := s.UploadForUser(context.Background(), userA, "notes", "b.txt", strings.NewReader("again")); err != nil {
		t.Fatalf("upload after the reset: %v", err)
	}
}

func TestAnEmptyListIsNotNil(t *testing.T) {
	s, _ := newService(t)
	names, err := s.ListForUser(userA)
	if err != nil || names == nil || len(names) != 0 {
		t.Fatalf("collections of a user with none = %#v, %v; want an empty list", names, err)
	}
}

func TestResetKeepsACollectionThatHadASource(t *testing.T) {
	cfg := config.NewApplicationConfig()
	cfg.DataPath = t.TempDir()
	cfg.Collections.VectorEngine = "chromem"
	cfg.Collections.EmbeddingModel = "embed"
	client := &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		body := `{"data":[{"embedding":[1,0,0],"index":0}],"model":"embed","usage":{}}`
		return &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": {"application/json"}},
			Body: io.NopCloser(strings.NewReader(body)), Request: r}, nil
	})}
	s, err := New(cfg, nil, WithHTTPClient(client))
	if err != nil {
		t.Fatal(err)
	}
	if err := s.CreateForUser(userA, "notes"); err != nil {
		t.Fatal(err)
	}
	// The fetch fails, but the collection has a source history from now on.
	_ = s.AddSourceForUser(userA, "notes", "https://example.invalid/feed", 60)
	_ = s.RemoveSourceForUser(userA, "notes", "https://example.invalid/feed")
	if _, err := s.UploadForUser(context.Background(), userA, "notes", "a.txt", strings.NewReader("hello there")); err != nil {
		t.Fatal(err)
	}
	if err := s.ResetForUser(userA, "notes"); err != nil {
		t.Fatalf("reset: %v", err)
	}
	if entries, err := s.ListEntriesForUser(userA, "notes"); err != nil || len(entries) != 0 {
		t.Fatalf("entries after the reset = %v, %v; want none", entries, err)
	}
}
