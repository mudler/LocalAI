package collections

import (
	"os"
	"path/filepath"
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
