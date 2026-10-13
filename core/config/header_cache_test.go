// SPDX-License-Identifier: MIT
package config

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
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

// fakeSpecWith returns a spec whose parser counts its calls through parses.
func fakeSpecWith(parses *int32, fingerprint string, schema int) headerCacheSpec[fakeSnap] {
	return headerCacheSpec[fakeSnap]{
		name:        "fake header cache",
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
func newFakeCache(dataPath string, spec headerCacheSpec[fakeSnap]) *headerCache[fakeSnap] {
	c := newHeaderCache(spec, 8)
	c.attachDisk(context.Background(), dataPath, "")
	DeferCleanup(c.stopPersist)
	return c
}

func writeFakeModel(path string, size int) {
	ExpectWithOffset(1, os.WriteFile(path, make([]byte, size), 0o600)).To(Succeed())
}

func fakeCacheFile(dataPath string) string {
	return headerCachePath(dataPath, "")
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

		first := newFakeCache(dir, fakeSpecWith(&parses, "v1", 1))
		snap, err := first.load(model)
		Expect(err).NotTo(HaveOccurred())
		Expect(snap).To(Equal(fakeSnap{Name: "model.bin", Bytes: 42}))
		Expect(parses).To(Equal(int32(1)))
		Expect(first.flush()).To(Succeed())
		first.stopPersist()

		restarted := newFakeCache(dir, fakeSpecWith(&parses, "v1", 1))
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
		c := newFakeCache(dir, fakeSpecWith(&parses, "v1", 1))
		_, _ = c.load(model)
		Expect(c.flush()).To(Succeed())
		c.stopPersist()

		other := newFakeCache(dir, fakeSpecWith(&parses, "v1", 2))
		Expect(other.len()).To(Equal(0), "a file with another schema must be ignored")
	})

	It("ignores a file whose format fingerprint differs", func() {
		dir := GinkgoT().TempDir()
		model := filepath.Join(dir, "model.bin")
		writeFakeModel(model, 8)
		c := newFakeCache(dir, fakeSpecWith(&parses, "v1", 1))
		_, _ = c.load(model)
		Expect(c.flush()).To(Succeed())
		c.stopPersist()

		other := newFakeCache(dir, fakeSpecWith(&parses, "v2", 1))
		Expect(other.len()).To(Equal(0), "a file with another fingerprint must be ignored")
	})

	It("misses when the file identity no longer matches", func() {
		dir := GinkgoT().TempDir()
		model := filepath.Join(dir, "model.bin")
		writeFakeModel(model, 8)
		c := newFakeCache(dir, fakeSpecWith(&parses, "v1", 1))
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
		path := fakeCacheFile(dir)

		for _, content := range []string{
			"{not json",
			`{"schema":1,"estimator":"v1","entries":[{"path":42}]}`,
			`{"schema":1,"estimator":"v1","entries":[{"path":"/x","header":{"name":5}}]}`,
		} {
			ExpectWithOffset(1, os.WriteFile(path, []byte(content), 0o600)).To(Succeed())
			var c *headerCache[fakeSnap]
			Expect(func() { c = newFakeCache(dir, fakeSpecWith(&parses, "v1", 1)) }).NotTo(Panic())
			Expect(c.len()).To(Equal(0), "content %q must be ignored", content)
			c.stopPersist()
		}
	})

	It("leaves no temp file behind after a write", func() {
		dir := GinkgoT().TempDir()
		model := filepath.Join(dir, "model.bin")
		writeFakeModel(model, 8)
		c := newFakeCache(dir, fakeSpecWith(&parses, "v1", 1))
		_, _ = c.load(model)
		Expect(c.flush()).To(Succeed())

		entries, err := os.ReadDir(dir)
		Expect(err).NotTo(HaveOccurred())
		for _, e := range entries {
			Expect(strings.Contains(e.Name(), ".tmp")).To(BeFalse(), "temp file %s left behind", e.Name())
		}
		Expect(fakeCacheFile(dir)).To(BeAnExistingFile())
	})

	It("works without a data path: memory only, nothing is written", func() {
		dir := GinkgoT().TempDir()
		model := filepath.Join(dir, "model.bin")
		writeFakeModel(model, 8)
		c := newFakeCache("", fakeSpecWith(&parses, "v1", 1))

		_, _ = c.load(model)
		_, _ = c.load(model)
		Expect(parses).To(Equal(int32(1)), "the in-memory cache still works")
		Expect(c.flush()).To(Succeed())
		entries, err := os.ReadDir(dir)
		Expect(err).NotTo(HaveOccurred())
		Expect(entries).To(HaveLen(1), "only the model file is present")
	})

	It("uses header-cache.json for every instance", func() {
		Expect(headerCachePath("/data", "")).To(Equal(filepath.Join("/data", "header-cache.json")))

		dir := GinkgoT().TempDir()
		model := filepath.Join(dir, "model.bin")
		writeFakeModel(model, 8)
		a := newFakeCache(dir, fakeSpecWith(&parses, "v1", 1))
		b := newFakeCache(dir, fakeSpecWith(&parses, "v1", 1))
		_, _ = a.load(model)
		Expect(a.flush()).To(Succeed())
		Expect(b.flush()).To(Succeed())

		entries, err := os.ReadDir(dir)
		Expect(err).NotTo(HaveOccurred())
		names := []string{}
		for _, e := range entries {
			names = append(names, e.Name())
		}
		Expect(names).To(ConsistOf("model.bin", "header-cache.json"))
	})

	It("serves an entry written by another instance after a reload on miss, without parsing", func() {
		dir := GinkgoT().TempDir()
		model := filepath.Join(dir, "model.bin")
		writeFakeModel(model, 42)

		reader := newFakeCache(dir, fakeSpecWith(&parses, "v1", 1))
		writer := newFakeCache(dir, fakeSpecWith(&parses, "v1", 1))
		_, err := writer.load(model)
		Expect(err).NotTo(HaveOccurred())
		Expect(parses).To(Equal(int32(1)))
		Expect(writer.flush()).To(Succeed())

		snap, err := reader.load(model)
		Expect(err).NotTo(HaveOccurred())
		Expect(snap).To(Equal(fakeSnap{Name: "model.bin", Bytes: 42}))
		Expect(parses).To(Equal(int32(1)), "the reader must reload the file on its miss instead of parsing")
	})

	It("merges on save so that the file holds the union of two instances", func() {
		dir := GinkgoT().TempDir()
		m1 := filepath.Join(dir, "one.bin")
		m2 := filepath.Join(dir, "two.bin")
		writeFakeModel(m1, 1)
		writeFakeModel(m2, 2)

		a := newFakeCache(dir, fakeSpecWith(&parses, "v1", 1))
		b := newFakeCache(dir, fakeSpecWith(&parses, "v1", 1))
		_, _ = a.load(m1)
		_, _ = b.load(m2)
		Expect(a.flush()).To(Succeed())
		Expect(b.flush()).To(Succeed())

		file := readFakeFile(dir)
		paths := []string{}
		for _, e := range file.Entries {
			paths = append(paths, e.Path)
		}
		Expect(paths).To(ConsistOf(m1, m2))
	})

	It("produces a valid file under concurrent saves from two instances", func() {
		dir := GinkgoT().TempDir()
		var models [2][3]string
		for i := range models {
			for j := range models[i] {
				models[i][j] = filepath.Join(dir, fmt.Sprintf("m-%d-%d.bin", i, j))
				writeFakeModel(models[i][j], 4+i*3+j)
			}
		}
		// Each instance counts its own parses: the counter is not synchronized, and
		// the two goroutines below run in parallel.
		var parsesA, parsesB int32
		insts := [2]*headerCache[fakeSnap]{
			newFakeCache(dir, fakeSpecWith(&parsesA, "v1", 1)),
			newFakeCache(dir, fakeSpecWith(&parsesB, "v1", 1)),
		}

		var wg sync.WaitGroup
		errs := make(chan error, 16)
		for i := range insts {
			wg.Add(1)
			go func(c *headerCache[fakeSnap], files [3]string) {
				defer wg.Done()
				for _, f := range files {
					if _, err := c.load(f); err != nil {
						errs <- err
						return
					}
					if err := c.flush(); err != nil {
						errs <- err
						return
					}
				}
			}(insts[i], models[i])
		}
		wg.Wait()
		close(errs)
		for err := range errs {
			Expect(err).NotTo(HaveOccurred())
		}

		// Two racing writers may drop each other's last entries. A final pair of
		// sequential saves merges everything back, because each save folds in what
		// is on disk. The bound (8) is larger than the six entries, so none is evicted.
		Expect(insts[0].flush()).To(Succeed())
		Expect(insts[1].flush()).To(Succeed())

		file := readFakeFile(dir)
		Expect(file.Entries).To(HaveLen(6))
		Expect(tmpFilesIn(dir)).To(BeEmpty())
	})

	It("ignores a corrupt or wrong-schema file and overwrites it with a valid save", func() {
		dir := GinkgoT().TempDir()
		model := filepath.Join(dir, "model.bin")
		writeFakeModel(model, 8)
		path := fakeCacheFile(dir)

		for _, content := range []string{
			"{not json",
			`{"schema":99,"estimator":"v1","entries":[]}`,
		} {
			ExpectWithOffset(1, os.WriteFile(path, []byte(content), 0o600)).To(Succeed())
			c := newFakeCache(dir, fakeSpecWith(&parses, "v1", 1))
			Expect(c.len()).To(Equal(0))
			_, err := c.load(model)
			Expect(err).NotTo(HaveOccurred())
			Expect(c.flush()).To(Succeed())
			c.stopPersist()

			file := readFakeFile(dir)
			Expect(file.Schema).To(Equal(1))
			Expect(file.Entries).To(HaveLen(1), "content %q must be replaced by a valid save", content)
		}
	})
})

// readFakeFile decodes the shared cache file in dir, failing the spec if it is not valid.
func readFakeFile(dir string) headerFile {
	raw, err := os.ReadFile(fakeCacheFile(dir))
	ExpectWithOffset(1, err).NotTo(HaveOccurred())
	var file headerFile
	ExpectWithOffset(1, json.Unmarshal(raw, &file)).To(Succeed())
	return file
}

// tmpFilesIn lists temp files left in dir.
func tmpFilesIn(dir string) []string {
	entries, err := os.ReadDir(dir)
	ExpectWithOffset(1, err).NotTo(HaveOccurred())
	var out []string
	for _, e := range entries {
		if strings.Contains(e.Name(), ".tmp") {
			out = append(out, e.Name())
		}
	}
	return out
}
