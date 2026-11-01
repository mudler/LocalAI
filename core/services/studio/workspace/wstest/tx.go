package wstest

import (
	"strings"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/mudler/LocalAI/core/services/studio/workspace"
)

func describeTx(newStore NewStore) {
	Describe("Tx", func() {
		It("commit groups calls into one change set", func() {
			s := newStore()
			tx, err := s.Begin(Ctx(), AliceAdmin(), "Sort upload")
			Expect(err).ToNot(HaveOccurred())
			_, err = tx.Put(Ctx(), "/in/a.txt", strings.NewReader("a"), workspace.PutOptions{})
			Expect(err).ToNot(HaveOccurred())
			_, err = tx.Mkdir(Ctx(), "/out")
			Expect(err).ToNot(HaveOccurred())
			cs, err := tx.Commit()
			Expect(err).ToNot(HaveOccurred())
			Expect(cs.Rev).To(BeEquivalentTo(1))
			Expect(cs.Title).To(Equal("Sort upload"))
			Expect(cs.Kind).To(Equal(workspace.ChangeAgent))
			Expect(cs.ChatID).To(Equal("7"))
			Expect(cs.Actor).To(Equal(Admin))
			Expect(cs.Ops).To(HaveLen(3)) // create /in, create /in/a.txt, create /out
			for _, op := range cs.Ops {
				Expect(op.Kind).To(Equal(workspace.OpCreate), "op = %+v", op)
			}
		})
		It("rollback changes nothing", func() {
			s := newStore()
			tx, err := s.Begin(Ctx(), AdminActor, "x")
			Expect(err).ToNot(HaveOccurred())
			_, err = tx.Mkdir(Ctx(), "/gone")
			Expect(err).ToNot(HaveOccurred())
			Expect(tx.Rollback()).To(Succeed())
			Expect(Tree(s)).To(BeEmpty())
			_, err = tx.Mkdir(Ctx(), "/late")
			Expect(err).To(MatchError(workspace.ErrInvalid), "call after rollback")
		})
		It("a failed call keeps the earlier calls", func() {
			s := newStore()
			MustPut(s, AdminActor, "/f", "x")
			tx, err := s.Begin(Ctx(), AdminActor, "x")
			Expect(err).ToNot(HaveOccurred())
			_, err = tx.Mkdir(Ctx(), "/ok")
			Expect(err).ToNot(HaveOccurred())
			_, err = tx.Put(Ctx(), "/new/f/g", strings.NewReader("y"), workspace.PutOptions{})
			Expect(err).ToNot(HaveOccurred()) // creates /new and /new/f as folders
			_, err = tx.Put(Ctx(), "/f/g", strings.NewReader("y"), workspace.PutOptions{})
			Expect(err).To(MatchError(workspace.ErrExists), "put under a file")
			cs, err := tx.Commit()
			Expect(err).ToNot(HaveOccurred())
			Expect(cs.Ops).To(HaveLen(4))
		})
		It("empty commit returns a zero change set", func() {
			s := newStore()
			tx, err := s.Begin(Ctx(), AdminActor, "nothing")
			Expect(err).ToNot(HaveOccurred())
			cs, err := tx.Commit()
			Expect(err).ToNot(HaveOccurred())
			Expect(cs.Rev).To(BeZero())
		})
		It("a read during an open Tx does not hang or fail", func() {
			s := newStore()
			tx, err := s.Begin(Ctx(), AdminActor, "x")
			Expect(err).ToNot(HaveOccurred())
			_, err = tx.Mkdir(Ctx(), "/d")
			Expect(err).ToNot(HaveOccurred())
			done := make(chan error, 1)
			go func() {
				_, err := s.Stat(Ctx(), AdminActor, "/d")
				done <- err
			}()
			time.Sleep(50 * time.Millisecond)
			_, err = tx.Commit()
			Expect(err).ToNot(HaveOccurred())
			select {
			case statErr := <-done:
				// A store with snapshot reads may answer before the commit and not
				// see the folder yet. Both outcomes are fine.
				if statErr != nil {
					Expect(statErr).To(MatchError(workspace.ErrNotFound), "stat during a Tx")
				}
			case <-time.After(5 * time.Second):
				Fail("Stat did not finish after the Tx committed")
			}
		})
	})
}
