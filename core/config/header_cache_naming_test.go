package config

import (
	"path/filepath"
	"strings"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("header cache file naming", func() {
	It("uses the shared file when no instance ID is set", func() {
		Expect(headerCachePath("/data", "")).To(Equal(filepath.Join("/data", "header-cache.json")))
	})

	It("uses a file of its own for a plain instance ID", func() {
		Expect(headerCachePath("/data", "local-ai-frontend-0")).To(Equal(filepath.Join("/data", "header-cache-local-ai-frontend-0.json")))
	})

	It("hashes an instance ID that is not a plain token", func() {
		path := headerCachePath("/data", "a/b c")
		Expect(filepath.Base(path)).To(HavePrefix("header-cache-x"))
		Expect(strings.ContainsAny(filepath.Base(path), "/ ")).To(BeFalse())
		Expect(headerCachePath("/data", "a/b c")).To(Equal(path))
		Expect(headerCachePath("/data", "a/b d")).NotTo(Equal(path))
	})
})
