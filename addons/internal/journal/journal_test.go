// SPDX-License-Identifier: MIT
package journal_test

import (
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/mudler/LocalAI/addons/internal/journal"
	"github.com/mudler/LocalAI/pkg/addons/contract"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func TestJournal(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Journal suite")
}

type job struct{ Name string }

func committed(op string) contract.Receipt {
	return contract.Receipt{OperationID: op, State: contract.ReceiptCommitted, Revision: contract.Revision{Schema: 1, Data: 1}}
}

var _ = Describe("journal", func() {
	at := time.Unix(1700000000, 0).UTC()

	It("answers a receipt after a restart and clears the attempt note", func() {
		dir := GinkgoT().TempDir()
		j := journal.Open(dir)
		Expect(j.Begin("op1", at)).To(Succeed())
		started, ok := j.Started("op1")
		Expect(ok).To(BeTrue())
		Expect(started).To(BeTemporally("==", at))

		Expect(j.Commit(committed("op1"))).To(Succeed())
		_, ok = j.Started("op1")
		Expect(ok).To(BeFalse())

		again := journal.Open(dir)
		rc, ok := again.Receipt("op1")
		Expect(ok).To(BeTrue())
		Expect(rc.State).To(Equal(contract.ReceiptCommitted))
	})

	It("forgets an attempt", func() {
		j := journal.Open("")
		Expect(j.Begin("op1", at)).To(Succeed())
		j.Forget("op1")
		_, ok := j.Started("op1")
		Expect(ok).To(BeFalse())
	})

	It("keeps memory only without a directory", func() {
		j := journal.Open("")
		Expect(j.Commit(contract.Receipt{OperationID: "a"})).To(Succeed())
		_, ok := j.Receipt("a")
		Expect(ok).To(BeTrue())
	})

	It("drops the oldest receipt over the bound", func() {
		j := journal.Open("", journal.WithMaxReceipts(2))
		for i := range 3 {
			Expect(j.Commit(contract.Receipt{OperationID: strconv.Itoa(i)})).To(Succeed())
		}
		_, ok := j.Receipt("0")
		Expect(ok).To(BeFalse())
		_, ok = j.Receipt("2")
		Expect(ok).To(BeTrue())
	})

	It("stores a job with its receipt in one write", func() {
		dir := GinkgoT().TempDir()
		j := journal.OpenJobs[job](dir)
		Expect(j.CommitJob(committed("op"), "res", &job{Name: "n"})).To(Succeed())
		got, ok := journal.OpenJobs[job](dir).Job("res")
		Expect(ok).To(BeTrue())
		Expect(got.Name).To(Equal("n"))
	})

	It("leaves the memory as it was when the write fails", func() {
		dir := GinkgoT().TempDir()
		j := journal.Open(dir)
		Expect(os.Chmod(dir, 0o500)).To(Succeed())
		DeferCleanup(func() { _ = os.Chmod(dir, 0o700) })
		if os.Geteuid() == 0 {
			Skip("root can write to a read-only directory")
		}
		Expect(j.Commit(contract.Receipt{OperationID: "x"})).NotTo(Succeed())
		_, ok := j.Receipt("x")
		Expect(ok).To(BeFalse())
	})

	It("ignores an unreadable file", func() {
		dir := GinkgoT().TempDir()
		Expect(os.WriteFile(filepath.Join(dir, "journal.json"), []byte("{"), 0o600)).To(Succeed())
		_, ok := journal.Open(dir).Receipt("x")
		Expect(ok).To(BeFalse())
	})
})
