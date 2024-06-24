package xsync_test

import (
	. "github.com/mudler/LocalAI/pkg/xsync"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("SyncMap", func() {

	Context("Syncmap", func() {
		It("sets and gets", func() {
			m := NewSyncedMap[string, string]()
			m.Set("foo", "bar")
			Expect(m.Get("foo")).To(Equal("bar"))
		})
		It("deletes", func() {
			m := NewSyncedMap[string, string]()
			m.Set("foo", "bar")
			m.Delete("foo")
			Expect(m.Get("foo")).To(Equal(""))
			Expect(m.Exists("foo")).To(Equal(false))
		})
	})
})
