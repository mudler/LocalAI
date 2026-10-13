// SPDX-License-Identifier: MIT
package config

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// fakeSnap is a snapshot of a made-up format. The generic cache must work with
// it exactly as it does with the GGUF snapshot, without knowing its fields.
type fakeSnap struct {
	Name  string `json:"name"`
	Bytes int64  `json:"bytes"`
}

const fakePrefix = "fake-header-cache-"

// fakeSpecWith returns a spec whose parser counts its calls through parses.
func fakeSpecWith(parses *int32, fingerprint string, schema int) headerCacheSpec[fakeSnap] {
	return headerCacheSpec[fakeSnap]{
		name:        "fake header cache",
		filePrefix:  fakePrefix,
		schema:      schema,
		fingerprint: fingerprint,
		parse: func(path string) (fakeSnap, error) {
			*parses++
			info, err := os.Stat(path)
			if err != nil {
				return fakeSnap{}, err
			}
			return fakeSnap{Name: filepath.Base(path), Bytes: info.Size()}, nil
		},
		encode: func(s fakeSnap) any { return s },
		decode: func(raw json.RawMessage) (fakeSnap, error) {
			var s fakeSnap
			err := json.Unmarshal(raw, &s)
			return s, err
		},
	}
}

// newFakeCache returns a cache for the fake format attached to dataPath, the
// way startup attaches the GGUF cache. The saver is stopped in cleanup.
func newFakeCache(dataPath, instanceID string, spec headerCacheSpec[fakeSnap]) *headerCache[fakeSnap] {
	c := newHeaderCache(spec, 8)
	c.attachDisk(context.Background(), dataPath, instanceID)
	DeferCleanup(c.stopPersist)
	return c
}

func writeFakeModel(path string, size int) {
	ExpectWithOffset(1, os.WriteFile(path, make([]byte, size), 0o600)).To(Succeed())
}

func fakeCacheFile(dataPath, instanceID string) string {
	return headerCachePath(fakePrefix, dataPath, instanceID)
}

var _ = Describe("Generic header cache", func() {
	var parses int32

	BeforeEach(func() {
		parses = 0
		// Each test flushes explicitly, so the timer-driven saver stays out of the way.
		prev := headerCacheSaveDebounce
		headerCacheSaveDebounce = time.Hour
		DeferCleanup(func() { headerCacheSaveDebounce = prev })
	})

	It("serves a snapshot from the file after a restart without parsing", func() {
		dir := GinkgoT().TempDir()
		model := filepath.Join(dir, "model.bin")
		writeFakeModel(model, 42)

		first := newFakeCache(dir, "inst-a", fakeSpecWith(&parses, "v1", 1))
		snap, err := first.load(model)
		Expect(err).NotTo(HaveOccurred())
		Expect(snap).To(Equal(fakeSnap{Name: "model.bin", Bytes: 42}))
		Expect(parses).To(Equal(int32(1)))
		Expect(first.flush()).To(Succeed())
		first.stopPersist()

		restarted := newFakeCache(dir, "inst-a", fakeSpecWith(&parses, "v1", 1))
		Expect(restarted.len()).To(Equal(1))
		again, err := restarted.load(model)
		Expect(err).NotTo(HaveOccurred())
		Expect(again).To(Equal(snap))
		Expect(parses).To(Equal(int32(1)), "the restarted cache must not parse again")
	})

	It("ignores a file whose schema version differs", func() {
		dir := GinkgoT().TempDir()
		model := filepath.Join(dir, "model.bin")
		writeFakeModel(model, 8)
		c := newFakeCache(dir, "inst-a", fakeSpecWith(&parses, "v1", 1))
		_, _ = c.load(model)
		Expect(c.flush()).To(Succeed())
		c.stopPersist()

		other := newFakeCache(dir, "inst-a", fakeSpecWith(&parses, "v1", 2))
		Expect(other.len()).To(Equal(0), "a file with another schema must be ignored")
	})

	It("ignores a file whose format fingerprint differs", func() {
		dir := GinkgoT().TempDir()
		model := filepath.Join(dir, "model.bin")
		writeFakeModel(model, 8)
		c := newFakeCache(dir, "inst-a", fakeSpecWith(&parses, "v1", 1))
		_, _ = c.load(model)
		Expect(c.flush()).To(Succeed())
		c.stopPersist()

		other := newFakeCache(dir, "inst-a", fakeSpecWith(&parses, "v2", 1))
		Expect(other.len()).To(Equal(0), "a file with another fingerprint must be ignored")
	})

	It("misses when the file identity no longer matches", func() {
		dir := GinkgoT().TempDir()
		model := filepath.Join(dir, "model.bin")
		writeFakeModel(model, 8)
		c := newFakeCache(dir, "inst-a", fakeSpecWith(&parses, "v1", 1))
		_, _ = c.load(model)
		Expect(parses).To(Equal(int32(1)))

		writeFakeModel(model, 16)
		snap, err := c.load(model)
		Expect(err).NotTo(HaveOccurred())
		Expect(parses).To(Equal(int32(2)), "a changed size must be a miss")
		Expect(snap.Bytes).To(Equal(int64(16)))
	})

	It("ignores a corrupt file without panicking", func() {
		dir := GinkgoT().TempDir()
		model := filepath.Join(dir, "model.bin")
		writeFakeModel(model, 8)
		path := fakeCacheFile(dir, "inst-a")

		for _, content := range []string{
			"{not json",
			`{"schema":1,"estimator":"v1","entries":[{"path":42}]}`,
			`{"schema":1,"estimator":"v1","entries":[{"path":"/x","header":{"name":5}}]}`,
		} {
			ExpectWithOffset(1, os.WriteFile(path, []byte(content), 0o600)).To(Succeed())
			var c *headerCache[fakeSnap]
			Expect(func() { c = newFakeCache(dir, "inst-a", fakeSpecWith(&parses, "v1", 1)) }).NotTo(Panic())
			Expect(c.len()).To(Equal(0), "content %q must be ignored", content)
			c.stopPersist()
		}
	})

	It("leaves no temp file behind after a write", func() {
		dir := GinkgoT().TempDir()
		model := filepath.Join(dir, "model.bin")
		writeFakeModel(model, 8)
		c := newFakeCache(dir, "inst-a", fakeSpecWith(&parses, "v1", 1))
		_, _ = c.load(model)
		Expect(c.flush()).To(Succeed())

		entries, err := os.ReadDir(dir)
		Expect(err).NotTo(HaveOccurred())
		for _, e := range entries {
			Expect(strings.Contains(e.Name(), ".tmp")).To(BeFalse(), "temp file %s left behind", e.Name())
		}
		Expect(fakeCacheFile(dir, "inst-a")).To(BeAnExistingFile())
	})

	It("works without a data path: memory only, nothing is written", func() {
		dir := GinkgoT().TempDir()
		model := filepath.Join(dir, "model.bin")
		writeFakeModel(model, 8)
		c := newFakeCache("", "", fakeSpecWith(&parses, "v1", 1))

		_, _ = c.load(model)
		_, _ = c.load(model)
		Expect(parses).To(Equal(int32(1)), "the in-memory cache still works")
		Expect(c.flush()).To(Succeed())
		entries, err := os.ReadDir(dir)
		Expect(err).NotTo(HaveOccurred())
		Expect(entries).To(HaveLen(1), "only the model file is present")
	})

	It("names the standalone file without the instance dash and hashes unusual IDs", func() {
		Expect(headerCachePath(fakePrefix, "/data", "")).To(Equal(filepath.Join("/data", "fake-header-cache.json")))
		Expect(headerCachePath(fakePrefix, "/data", "inst-a")).To(Equal(filepath.Join("/data", "fake-header-cache-inst-a.json")))
		hashed := filepath.Base(headerCachePath(fakePrefix, "/data", "../escape"))
		Expect(hashed).To(HavePrefix("fake-header-cache-h-"))
		Expect(hashed).NotTo(ContainSubstring(".."))
	})
})
