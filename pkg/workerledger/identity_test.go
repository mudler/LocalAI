// SPDX-License-Identifier: MIT
package workerledger_test

import (
	"os"
	"path/filepath"
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/mudler/LocalAI/pkg/workerledger"
)

func TestWorkerLedger(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Worker ledger")
}

var _ = Describe("Process ledger file", func() {
	It("distinguishes a missing ledger from a corrupt one", func() {
		dir := GinkgoT().TempDir()
		entries, err := workerledger.ReadFile(filepath.Join(dir, "none.json"))
		Expect(err).ToNot(HaveOccurred())
		Expect(entries).To(BeNil())
		bad := filepath.Join(dir, "bad.json")
		Expect(os.WriteFile(bad, []byte("{"), 0o600)).To(Succeed())
		_, err = workerledger.ReadFile(bad)
		Expect(err).To(HaveOccurred(), "ownership of earlier backends is unknown")
	})

	It("writes owner-only, atomically, and round-trips", func() {
		path := filepath.Join(GinkgoT().TempDir(), "sub", "ledger.json")
		want := []workerledger.Entry{{Key: "m#0", PID: 42, StartTime: "7", Generation: "g"}}
		Expect(workerledger.WriteFile(path, want)).To(Succeed())
		info, err := os.Stat(path)
		Expect(err).ToNot(HaveOccurred())
		Expect(info.Mode().Perm()).To(Equal(os.FileMode(0o600)))
		got, err := workerledger.ReadFile(path)
		Expect(err).ToNot(HaveOccurred())
		Expect(got).To(Equal(want))
		_, err = os.Stat(path + ".tmp")
		Expect(os.IsNotExist(err)).To(BeTrue(), "no temporary file is left behind")
	})

	It("classifies a missing group as gone", func() {
		Expect(workerledger.Classify(workerledger.Entry{PID: 0})).To(Equal(workerledger.Gone))
		Expect(workerledger.Classify(workerledger.Entry{PID: 1 << 30, StartTime: "1"})).To(Equal(workerledger.Gone))
	})
})
