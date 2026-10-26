package wstest

import (
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/mudler/LocalAI/core/services/studio/workspace"
)

func testFiles(t *testing.T, newStore NewStore) {
	t.Run("root exists and is empty", func(t *testing.T) {
		s := newStore(t)
		n, err := s.Stat(Ctx(), AdminActor, "/")
		if err != nil || n.Kind != workspace.KindDir || n.Path != "/" {
			t.Fatalf("root = %+v, %v", n, err)
		}
		if kids, err := s.List(Ctx(), AdminActor, "/"); err != nil || len(kids) != 0 {
			t.Fatalf("root children = %v, %v", kids, err)
		}
	})
	t.Run("put creates parents and reads back", func(t *testing.T) {
		s := newStore(t)
		n := MustPut(t, s, AdminActor, "/clients/rossi/a.pdf", "pdf")
		if n.Path != "/clients/rossi/a.pdf" || n.Kind != workspace.KindFile || n.Size != 3 || n.Version != 1 || n.Blob == "" {
			t.Fatalf("node = %+v", n)
		}
		want := map[string]string{"/clients": "/", "/clients/rossi": "/", "/clients/rossi/a.pdf": "pdf"}
		if got := Tree(t, s); !reflect.DeepEqual(got, want) {
			t.Fatalf("tree = %v", got)
		}
	})
	t.Run("list is sorted by name", func(t *testing.T) {
		s := newStore(t)
		for _, p := range []string{"/d/c.txt", "/d/a.txt", "/d/b"} {
			if strings.HasSuffix(p, ".txt") {
				MustPut(t, s, AdminActor, p, "x")
			} else if _, err := s.Mkdir(Ctx(), AdminActor, p); err != nil {
				t.Fatal(err)
			}
		}
		kids, err := s.List(Ctx(), AdminActor, "/d")
		if err != nil {
			t.Fatal(err)
		}
		var names []string
		for _, k := range kids {
			names = append(names, k.Name)
		}
		if !reflect.DeepEqual(names, []string{"a.txt", "b", "c.txt"}) {
			t.Fatalf("names = %v", names)
		}
	})
	t.Run("NFD and NFC are one name", func(t *testing.T) {
		s := newStore(t)
		MustPut(t, s, AdminActor, "/café.txt", "one")
		n, err := s.Put(Ctx(), AdminActor, "/café.txt", strings.NewReader("two"), workspace.PutOptions{BaseVersion: 1})
		if err != nil || n.Version != 2 || n.ConflictOf != "" {
			t.Fatalf("put NFD = %+v, %v", n, err)
		}
		if got := Tree(t, s); len(got) != 1 || got["/café.txt"] != "two" {
			t.Fatalf("tree = %v", got)
		}
	})
	t.Run("same content is not a change", func(t *testing.T) {
		s := newStore(t)
		n1 := MustPut(t, s, AdminActor, "/a.txt", "x")
		n2 := MustPut(t, s, AdminActor, "/a.txt", "x")
		if n2.Version != n1.Version || n2.Rev != n1.Rev {
			t.Fatalf("a second put of the same bytes changed the node: %+v -> %+v", n1, n2)
		}
	})
	t.Run("matching base version writes", func(t *testing.T) {
		s := newStore(t)
		n1 := MustPut(t, s, AdminActor, "/r.xlsx", "v1")
		n2, err := s.Put(Ctx(), AliceAdmin(), "/r.xlsx", strings.NewReader("v2"), workspace.PutOptions{BaseVersion: n1.Version})
		if err != nil || n2.Version != 2 || n2.ConflictOf != "" {
			t.Fatalf("put = %+v, %v", n2, err)
		}
		if MustRead(t, s, AdminActor, "/r.xlsx") != "v2" {
			t.Fatal("content not written")
		}
	})
	t.Run("stale base version keeps both", func(t *testing.T) {
		s := newStore(t)
		MustPut(t, s, AdminActor, "/r.xlsx", "v1")
		MustPut(t, s, AdminActor, "/r.xlsx", "v2") // now version 2
		a := workspace.Actor{Principal: Admin, ChatID: "12"}
		n, err := s.Put(Ctx(), a, "/r.xlsx", strings.NewReader("mine"), workspace.PutOptions{BaseVersion: 1})
		if err != nil {
			t.Fatal(err)
		}
		if n.Path != "/r (from chat 12).xlsx" || n.ConflictOf != "/r.xlsx" {
			t.Fatalf("conflict copy = %+v", n)
		}
		if MustRead(t, s, AdminActor, "/r.xlsx") != "v2" || MustRead(t, s, AdminActor, n.Path) != "mine" {
			t.Fatal("both versions must be kept")
		}
		n2, err := s.Put(Ctx(), a, "/r.xlsx", strings.NewReader("again"), workspace.PutOptions{BaseVersion: 1})
		if err != nil || n2.Path != "/r (from chat 12) 2.xlsx" {
			t.Fatalf("second conflict copy = %+v, %v", n2, err)
		}
	})
	t.Run("zero base version on an existing file keeps both", func(t *testing.T) {
		s := newStore(t)
		MustPut(t, s, AdminActor, "/a.txt", "old")
		n, err := s.Put(Ctx(), AdminActor, "/a.txt", strings.NewReader("new"), workspace.PutOptions{})
		if err != nil || n.ConflictOf != "/a.txt" {
			t.Fatalf("put = %+v, %v", n, err)
		}
	})
	t.Run("overwrite skips the version check", func(t *testing.T) {
		s := newStore(t)
		MustPut(t, s, AdminActor, "/a.txt", "old")
		n := MustPut(t, s, AdminActor, "/a.txt", "new")
		if n.ConflictOf != "" || MustRead(t, s, AdminActor, "/a.txt") != "new" {
			t.Fatalf("overwrite = %+v", n)
		}
	})
	t.Run("case-only sibling is refused", func(t *testing.T) {
		s := newStore(t)
		MustPut(t, s, AdminActor, "/Report.pdf", "x")
		if _, err := s.Put(Ctx(), AdminActor, "/report.pdf", strings.NewReader("y"), workspace.PutOptions{}); !errors.Is(err, workspace.ErrExists) {
			t.Fatalf("err = %v, want ErrExists", err)
		}
		if _, err := s.Mkdir(Ctx(), AdminActor, "/REPORT.PDF"); !errors.Is(err, workspace.ErrExists) {
			t.Fatalf("mkdir err = %v, want ErrExists", err)
		}
	})
	t.Run("file and folder names do not mix", func(t *testing.T) {
		s := newStore(t)
		MustPut(t, s, AdminActor, "/f", "x")
		if _, err := s.Put(Ctx(), AdminActor, "/f/g", strings.NewReader("y"), workspace.PutOptions{}); !errors.Is(err, workspace.ErrExists) {
			t.Fatalf("put under a file: %v", err)
		}
		if _, err := s.Mkdir(Ctx(), AdminActor, "/d"); err != nil {
			t.Fatal(err)
		}
		if _, err := s.Put(Ctx(), AdminActor, "/d", strings.NewReader("y"), workspace.PutOptions{Overwrite: true}); !errors.Is(err, workspace.ErrExists) {
			t.Fatalf("put on a folder: %v", err)
		}
		if _, err := s.Mkdir(Ctx(), AdminActor, "/d"); !errors.Is(err, workspace.ErrExists) {
			t.Fatalf("mkdir twice: %v", err)
		}
		if _, _, err := s.Open(Ctx(), AdminActor, "/d"); !errors.Is(err, workspace.ErrInvalid) {
			t.Fatalf("open a folder: %v", err)
		}
		if _, err := s.List(Ctx(), AdminActor, "/f"); !errors.Is(err, workspace.ErrInvalid) {
			t.Fatalf("list a file: %v", err)
		}
	})
	t.Run("bad names are refused", func(t *testing.T) {
		s := newStore(t)
		for _, p := range []string{"/a/../b", "/a\x00", "//a", "/a\nb"} {
			if _, err := s.Put(Ctx(), AdminActor, p, strings.NewReader("x"), workspace.PutOptions{}); !errors.Is(err, workspace.ErrBadName) {
				t.Errorf("put %q: %v, want ErrBadName", p, err)
			}
		}
		if _, err := s.Put(Ctx(), AdminActor, "/", strings.NewReader("x"), workspace.PutOptions{}); !errors.Is(err, workspace.ErrInvalid) {
			t.Errorf("put root: %v", err)
		}
	})
	t.Run("missing path is not found", func(t *testing.T) {
		s := newStore(t)
		if _, err := s.Stat(Ctx(), AdminActor, "/nope"); !errors.Is(err, workspace.ErrNotFound) {
			t.Fatalf("stat: %v", err)
		}
	})
}

// AliceAdmin returns the admin principal acting in Alice's chat, for tests
// that need a chat ID with full access.
func AliceAdmin() workspace.Actor {
	return workspace.Actor{Principal: Admin, ChatID: AliceChat.ChatID}
}
