// SPDX-License-Identifier: MIT
package store

import (
	"context"
	"errors"
	"fmt"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"os"
	"path/filepath"
	"strings"
)

const capID = "r0123456789abcdef0123456789abcde"

// fill writes blobs until the database refuses and returns that error.
func fill(s *Store, id, tag string) error {
	GinkgoHelper()
	db, err := s.openDatabase(id)
	if err != nil {
		Expect(err).NotTo(HaveOccurred())
	}
	defer func() { _ = db.Close() }()
	blob := strings.Repeat("x", 3000)
	for i := 0; i < 500; i++ {
		if _, err = db.Exec("INSERT INTO tables_operations(operation_id,kind,schema_revision,data_revision,applied_at) VALUES(?,?,?,?,?)", fmt.Sprintf("fill-%s-%d", tag, i), blob, 1, 0, "now"); err != nil {
			return capacityError(err)
		}
	}
	return nil
}

var _ = Describe("database size cap and path handling", func() {
	It("enforces the size cap on every connection and fails with a typed capacity error", func() {
		dir := filepath.Join(GinkgoT().TempDir(), "tables")
		s, err := Open(dir, Options{MaxPages: 64})
		Expect(err).NotTo(HaveOccurred())
		_, err = s.Create(context.Background(), capID, "Capped", "op-create-capped")
		Expect(err).NotTo(HaveOccurred())
		Expect(errors.Is(fill(s, capID, "one"), ErrCapacity)).To(BeTrue())
		Expect(s.Close()).To(Succeed())
		// A re-opened store and a new connection are held to the same cap.
		s, err = Open(dir, Options{MaxPages: 64})
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(func() { _ = s.Close() })
		Expect(errors.Is(fill(s, capID, "two"), ErrCapacity)).To(BeTrue())
		fi, err := os.Stat(filepath.Join(dir, "databases", capID+".db"))
		Expect(err).NotTo(HaveOccurred())
		Expect(fi.Size()).To(BeNumerically("<=", 64*4096))
	})

	It("builds a database under a data directory containing percent, question and hash characters", func() {
		dir := filepath.Join(GinkgoT().TempDir(), "a%41b?c#d")
		s, err := Open(dir, Options{})
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(func() { _ = s.Close() })
		_, err = s.Create(context.Background(), capID, "Odd", "op-create-odd")
		Expect(err).NotTo(HaveOccurred())
		Expect(filepath.Join(dir, "databases", capID+".db")).To(BeARegularFile())
		Expect(filepath.Join(filepath.Dir(dir), "aAb")).NotTo(BeAnExistingFile())
		ok, err := s.Exists(context.Background(), capID)
		Expect(err).NotTo(HaveOccurred())
		Expect(ok).To(BeTrue())
	})
})
