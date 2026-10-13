// SPDX-License-Identifier: MIT
package config

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// newPersistentCache returns a fresh cache attached to dataPath for instanceID,
// the way startup attaches the process cache. The saver is stopped in cleanup.
func newPersistentCache(dataPath string) *ggufHeaderCache {
	c := newGGUFHeaderCache(ggufHeaderCacheSize)
	c.attachDisk(context.Background(), dataPath)
	DeferCleanup(c.stopPersist)
	return c
}

// useHeaderCache swaps the process cache that llamaCppDefaultsWithParser reads,
// so a test can drive a new instance through the existing seam.
func useHeaderCache(c *ggufHeaderCache) {
	prev := ggufHeaders
	ggufHeaders = c
	DeferCleanup(func() { ggufHeaders = prev })
}

// persistFirstEntry parses weights.gguf through a persistent cache and writes
// the file, so later tests start from a real cache file.
func persistFirstEntry(dir string) {
	c := newPersistentCache(dir)
	useHeaderCache(c)
	p := &countingParser{}
	llamaCppDefaultsWithParser(presetConfig("weights.gguf"), dir, p.parse)
	ExpectWithOffset(1, p.calls.Load()).To(Equal(int32(1)))
	ExpectWithOffset(1, c.flush()).To(Succeed())
	c.stopPersist()
}

var _ = Describe("GGUF header cache persistence", func() {
	BeforeEach(func() {
		GinkgoT().Setenv("LOCALAI_DISABLE_GUESSING", "false")
		// Keep the background saver out of the way: each test flushes explicitly,
		// so a timer-driven write cannot race the rename override or the assertions.
		prev := headerCacheSaveDebounce
		headerCacheSaveDebounce = time.Hour
		DeferCleanup(func() { headerCacheSaveDebounce = prev })
	})

	It("serves entries written by an earlier instance without parsing", func() {
		dir := GinkgoT().TempDir()
		writeHeaderCacheFile(filepath.Join(dir, "weights.gguf"), 128)
		persistFirstEntry(dir)

		restarted := newPersistentCache(dir)
		Expect(restarted.len()).To(Equal(1))
		useHeaderCache(restarted)
		p := &countingParser{}
		cfg := presetConfig("weights.gguf")
		llamaCppDefaultsWithParser(cfg, dir, p.parse)

		Expect(p.calls.Load()).To(Equal(int32(0)), "a warm restart must not parse the header")
		Expect(cfg.Name).To(Equal("header-cache-fixture"))
	})

	It("misses on a restart when the recorded size or mtime no longer matches the file", func() {
		dir := GinkgoT().TempDir()
		path := filepath.Join(dir, "weights.gguf")
		writeHeaderCacheFile(path, 128)
		persistFirstEntry(dir)

		writeHeaderCacheFile(path, 256)
		resized := newPersistentCache(dir)
		useHeaderCache(resized)
		p := &countingParser{}
		llamaCppDefaultsWithParser(presetConfig("weights.gguf"), dir, p.parse)
		Expect(p.calls.Load()).To(Equal(int32(1)), "a changed size must be a miss")

		persistFirstEntry(dir)
		later := time.Now().Add(time.Hour)
		ExpectWithOffset(1, os.Chtimes(path, later, later)).To(Succeed())
		touched := newPersistentCache(dir)
		useHeaderCache(touched)
		p = &countingParser{}
		llamaCppDefaultsWithParser(presetConfig("weights.gguf"), dir, p.parse)
		Expect(p.calls.Load()).To(Equal(int32(1)), "a changed mtime must be a miss")
	})

	It("ignores a file whose schema version or estimator fingerprint differs", func() {
		dir := GinkgoT().TempDir()
		writeHeaderCacheFile(filepath.Join(dir, "weights.gguf"), 128)
		persistFirstEntry(dir)
		path := ggufHeaderCachePath(dir)

		raw, err := os.ReadFile(path)
		ExpectWithOffset(1, err).NotTo(HaveOccurred())
		var doc map[string]any
		ExpectWithOffset(1, json.Unmarshal(raw, &doc)).To(Succeed())

		for _, tamper := range []map[string]any{{"schema": 999}, {"estimator": "other"}} {
			doc2 := map[string]any{}
			for k, v := range doc {
				doc2[k] = v
			}
			for k, v := range tamper {
				doc2[k] = v
			}
			out, err := json.Marshal(doc2)
			ExpectWithOffset(1, err).NotTo(HaveOccurred())
			ExpectWithOffset(1, os.WriteFile(path, out, 0o600)).To(Succeed())

			c := newPersistentCache(dir)
			Expect(c.len()).To(Equal(0), "tampered file %v must be ignored", tamper)
			c.stopPersist()
		}
	})

	It("ignores corrupt and truncated files without panicking", func() {
		dir := GinkgoT().TempDir()
		writeHeaderCacheFile(filepath.Join(dir, "weights.gguf"), 128)
		persistFirstEntry(dir)
		path := ggufHeaderCachePath(dir)
		valid, err := os.ReadFile(path)
		ExpectWithOffset(1, err).NotTo(HaveOccurred())

		for _, content := range [][]byte{
			[]byte("{not json"),
			valid[:len(valid)/2],
			[]byte(`{"schema":1,"estimator":"x","entries":[{"path":42}]}`),
			{},
		} {
			ExpectWithOffset(1, os.WriteFile(path, content, 0o600)).To(Succeed())
			var c *ggufHeaderCache
			Expect(func() { c = newPersistentCache(dir) }).NotTo(Panic())
			Expect(c.len()).To(Equal(0))
			c.stopPersist()
		}
	})

	It("keeps every frontend on the one shared file", func() {
		dir := GinkgoT().TempDir()
		writeHeaderCacheFile(filepath.Join(dir, "weights.gguf"), 128)
		persistFirstEntry(dir)

		Expect(ggufHeaderCachePath(dir)).To(Equal(filepath.Join(dir, "header-cache.json")))
		Expect(ggufHeaderCachePath(dir)).To(BeAnExistingFile())

		other := newPersistentCache(dir)
		Expect(other.len()).To(Equal(1), "a second frontend starts from the shared file")
	})

	It("leaves no temp file behind after a successful write", func() {
		dir := GinkgoT().TempDir()
		writeHeaderCacheFile(filepath.Join(dir, "weights.gguf"), 128)
		persistFirstEntry(dir)

		Expect(tmpFilesIn(dir)).To(BeEmpty())
		Expect(ggufHeaderCachePath(dir)).To(BeAnExistingFile())
	})

	It("keeps the previous file intact when a write fails and removes its temp file", func() {
		dir := GinkgoT().TempDir()
		writeHeaderCacheFile(filepath.Join(dir, "weights.gguf"), 128)
		persistFirstEntry(dir)
		path := ggufHeaderCachePath(dir)
		before, err := os.ReadFile(path)
		ExpectWithOffset(1, err).NotTo(HaveOccurred())

		c := newPersistentCache(dir)
		c.put(filepath.Join(dir, "other.gguf"), ggufIdentity{size: 1, mtime: 1}, ggufHeader{Name: "other"})

		prevRename := headerCacheRename
		headerCacheRename = func(string, string) error { return errors.New("simulated rename failure") }
		DeferCleanup(func() { headerCacheRename = prevRename })

		Expect(c.flush()).To(HaveOccurred())
		after, err := os.ReadFile(path)
		Expect(err).NotTo(HaveOccurred())
		Expect(after).To(Equal(before), "a failed write must not touch the previous file")
		Expect(tmpFilesIn(dir)).To(BeEmpty())
	})

	It("writes at most as many entries as the in-memory bound", func() {
		dir := GinkgoT().TempDir()
		c := newPersistentCache(dir)
		for i := 0; i < ggufHeaderCacheSize+50; i++ {
			c.put(filepath.Join(dir, fmt.Sprintf("m-%03d.gguf", i)), ggufIdentity{size: 1, mtime: 1}, ggufHeader{Name: "m"})
		}
		Expect(c.flush()).To(Succeed())

		raw, err := os.ReadFile(ggufHeaderCachePath(dir))
		Expect(err).NotTo(HaveOccurred())
		var file persistedGGUFCache
		Expect(json.Unmarshal(raw, &file)).To(Succeed())
		Expect(file.Entries).To(HaveLen(ggufHeaderCacheSize))
	})

	It("works without a data path: memory only, nothing is written", func() {
		dir := GinkgoT().TempDir()
		writeHeaderCacheFile(filepath.Join(dir, "weights.gguf"), 128)
		c := newPersistentCache("")
		useHeaderCache(c)

		p := &countingParser{}
		llamaCppDefaultsWithParser(presetConfig("weights.gguf"), dir, p.parse)
		llamaCppDefaultsWithParser(presetConfig("weights.gguf"), dir, p.parse)
		Expect(p.calls.Load()).To(Equal(int32(1)), "the in-memory cache still works")

		Expect(c.flush()).To(Succeed())
		entries, err := os.ReadDir(dir)
		Expect(err).NotTo(HaveOccurred())
		Expect(entries).To(HaveLen(1), "only the model file is present")
	})
})
