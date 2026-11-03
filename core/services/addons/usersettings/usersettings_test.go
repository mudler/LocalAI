// SPDX-License-Identifier: MIT
package usersettings

import (
	"context"
	"path/filepath"
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func TestUserSettings(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Add-on user settings suite")
}

// registerStoreContract runs the same specs against every store.
func registerStoreContract(open func() Store) {
	var (
		ctx context.Context
		s   Store
	)
	BeforeEach(func() {
		ctx = context.Background()
		s = open()
	})

	It("starts empty", func() {
		Expect(s.Get(ctx, "u1", "memory")).To(BeEmpty())
	})

	It("stores one key and replaces it", func() {
		Expect(s.Set(ctx, "u1", "memory", "save-mode", "auto")).To(Succeed())
		Expect(s.Get(ctx, "u1", "memory")).To(Equal(map[string]string{"save-mode": "auto"}))
		Expect(s.Set(ctx, "u1", "memory", "save-mode", "never")).To(Succeed())
		Expect(s.Get(ctx, "u1", "memory")).To(Equal(map[string]string{"save-mode": "never"}))
	})

	It("keeps keys apart and does not touch the others on a write", func() {
		Expect(s.Set(ctx, "u1", "memory", "a-key", "one")).To(Succeed())
		Expect(s.Set(ctx, "u1", "memory", "save-mode", "auto")).To(Succeed())
		Expect(s.Get(ctx, "u1", "memory")).To(Equal(map[string]string{"a-key": "one", "save-mode": "auto"}))
	})

	It("does not leak across users or add-ons", func() {
		Expect(s.Set(ctx, "u1", "memory", "save-mode", "auto")).To(Succeed())
		Expect(s.Get(ctx, "u2", "memory")).To(BeEmpty())
		Expect(s.Get(ctx, "u1", "other")).To(BeEmpty())
	})

	It("repeats a write without change", func() {
		Expect(s.Set(ctx, "u1", "memory", "save-mode", "auto")).To(Succeed())
		Expect(s.Set(ctx, "u1", "memory", "save-mode", "auto")).To(Succeed())
		Expect(s.Get(ctx, "u1", "memory")).To(Equal(map[string]string{"save-mode": "auto"}))
	})
}

var _ = Describe("file store", func() {
	var path string
	BeforeEach(func() { path = filepath.Join(GinkgoT().TempDir(), "s.json") })
	registerStoreContract(func() Store {
		s, err := NewFileStore(filepath.Join(GinkgoT().TempDir(), "s.json"))
		Expect(err).ToNot(HaveOccurred())
		return s
	})
	It("survives a restart", func() {
		a, _ := NewFileStore(path)
		Expect(a.Set(context.Background(), "u1", "memory", "save-mode", "auto")).To(Succeed())
		b, err := NewFileStore(path)
		Expect(err).ToNot(HaveOccurred())
		Expect(b.Get(context.Background(), "u1", "memory")).To(Equal(map[string]string{"save-mode": "auto"}))
	})
})
