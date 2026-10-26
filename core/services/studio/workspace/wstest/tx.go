package wstest

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/mudler/LocalAI/core/services/studio/workspace"
)

func testTx(t *testing.T, newStore NewStore) {
	t.Run("commit groups calls into one change set", func(t *testing.T) {
		s := newStore(t)
		tx, err := s.Begin(Ctx(), AliceAdmin(), "Sort upload")
		if err != nil {
			t.Fatal(err)
		}
		if _, err := tx.Put(Ctx(), "/in/a.txt", strings.NewReader("a"), workspace.PutOptions{}); err != nil {
			t.Fatal(err)
		}
		if _, err := tx.Mkdir(Ctx(), "/out"); err != nil {
			t.Fatal(err)
		}
		cs, err := tx.Commit()
		if err != nil {
			t.Fatal(err)
		}
		if cs.Rev != 1 || cs.Title != "Sort upload" || cs.Kind != workspace.ChangeAgent || cs.ChatID != "7" || cs.Actor != Admin {
			t.Fatalf("change set = %+v", cs)
		}
		if len(cs.Ops) != 3 { // create /in, create /in/a.txt, create /out
			t.Fatalf("ops = %+v", cs.Ops)
		}
		for _, op := range cs.Ops {
			if op.Kind != workspace.OpCreate {
				t.Fatalf("op = %+v", op)
			}
		}
	})
	t.Run("rollback changes nothing", func(t *testing.T) {
		s := newStore(t)
		tx, err := s.Begin(Ctx(), AdminActor, "x")
		if err != nil {
			t.Fatal(err)
		}
		if _, err := tx.Mkdir(Ctx(), "/gone"); err != nil {
			t.Fatal(err)
		}
		if err := tx.Rollback(); err != nil {
			t.Fatal(err)
		}
		if got := Tree(t, s); len(got) != 0 {
			t.Fatalf("tree = %v", got)
		}
		if _, err := tx.Mkdir(Ctx(), "/late"); !errors.Is(err, workspace.ErrInvalid) {
			t.Fatalf("call after rollback: %v", err)
		}
	})
	t.Run("a failed call keeps the earlier calls", func(t *testing.T) {
		s := newStore(t)
		MustPut(t, s, AdminActor, "/f", "x")
		tx, err := s.Begin(Ctx(), AdminActor, "x")
		if err != nil {
			t.Fatal(err)
		}
		if _, err := tx.Mkdir(Ctx(), "/ok"); err != nil {
			t.Fatal(err)
		}
		if _, err := tx.Put(Ctx(), "/new/f/g", strings.NewReader("y"), workspace.PutOptions{}); err != nil {
			t.Fatal(err) // creates /new and /new/f as folders
		}
		if _, err := tx.Put(Ctx(), "/f/g", strings.NewReader("y"), workspace.PutOptions{}); !errors.Is(err, workspace.ErrExists) {
			t.Fatalf("put under a file: %v", err)
		}
		cs, err := tx.Commit()
		if err != nil {
			t.Fatal(err)
		}
		if len(cs.Ops) != 4 {
			t.Fatalf("ops = %+v", cs.Ops)
		}
	})
	t.Run("empty commit returns a zero change set", func(t *testing.T) {
		s := newStore(t)
		tx, err := s.Begin(Ctx(), AdminActor, "nothing")
		if err != nil {
			t.Fatal(err)
		}
		cs, err := tx.Commit()
		if err != nil || cs.Rev != 0 {
			t.Fatalf("cs = %+v, %v", cs, err)
		}
	})
	t.Run("a read during an open Tx does not hang or fail", func(t *testing.T) {
		s := newStore(t)
		tx, err := s.Begin(Ctx(), AdminActor, "x")
		if err != nil {
			t.Fatal(err)
		}
		if _, err := tx.Mkdir(Ctx(), "/d"); err != nil {
			t.Fatal(err)
		}
		done := make(chan error, 1)
		go func() {
			_, err := s.Stat(Ctx(), AdminActor, "/d")
			done <- err
		}()
		time.Sleep(50 * time.Millisecond)
		if _, err := tx.Commit(); err != nil {
			t.Fatal(err)
		}
		select {
		case err := <-done:
			// A store with snapshot reads may answer before the commit and not
			// see the folder yet. Both outcomes are fine.
			if err != nil && !errors.Is(err, workspace.ErrNotFound) {
				t.Fatalf("stat during a Tx: %v", err)
			}
		case <-time.After(5 * time.Second):
			t.Fatal("Stat did not finish after the Tx committed")
		}
	})
}
