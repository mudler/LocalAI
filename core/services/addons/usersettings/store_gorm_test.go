//go:build auth

// SPDX-License-Identifier: MIT
package usersettings

import (
	"context"

	"github.com/mudler/LocalAI/core/http/auth"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("database store", func() {
	registerStoreContract(func() Store {
		db, err := auth.InitDB(":memory:")
		Expect(err).ToNot(HaveOccurred())
		s, err := NewGormStore(db)
		Expect(err).ToNot(HaveOccurred())
		return s
	})

	// Two frontends share the database, so a save through one is read through
	// the other.
	It("is read through a second frontend", func() {
		db, err := auth.InitDB(":memory:")
		Expect(err).ToNot(HaveOccurred())
		a, _ := NewGormStore(db)
		b, _ := NewGormStore(db)
		Expect(a.Set(context.Background(), "u1", "memory", "save-mode", "auto")).To(Succeed())
		Expect(b.Get(context.Background(), "u1", "memory")).To(Equal(map[string]string{"save-mode": "auto"}))
	})
})
