package promptcache_test

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/mudler/LocalAI/pkg/promptcache"
)

func TestPromptCacheJanitor(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Prompt cache janitor test suite")
}

const (
	pin    = "1111111111111111"
	modelA = "aaaaaaaaaaaaaaaa"
	modelB = "bbbbbbbbbbbbbbbb"
	nsX    = "0123456789abcdef"
	nsY    = "fedcba9876543210"
	compat = "cccccccccccccccc"
)

var ctx = context.Background()

var now = time.Date(2026, 11, 27, 12, 0, 0, 0, time.UTC)

// put writes a cache file of size bytes last used age ago and returns its path.
func put(root, model, ns, hash string, size int, age time.Duration, tmp bool) string {
	dir := filepath.Join(root, "llama-cpp", pin+"-"+model)
	Expect(os.MkdirAll(dir, 0o700)).To(Succeed())
	name := compat + "-" + ns + "-" + strings.Repeat(hash, 32/len(hash)) + ".cache"
	if tmp {
		name += ".tmp"
	}
	p := filepath.Join(dir, name)
	Expect(os.WriteFile(p, make([]byte, size), 0o600)).To(Succeed())
	t := now.Add(-age)
	Expect(os.Chtimes(p, t, t)).To(Succeed())
	return p
}

func exists(p string) bool {
	_, err := os.Stat(p)
	return err == nil
}

var _ = Describe("the prompt cache janitor", func() {
	var root string
	clock := func() time.Time { return now }

	BeforeEach(func() {
		root = GinkgoT().TempDir()
	})

	It("treats a missing root as an empty cache", func() {
		res, err := promptcache.Sweep(ctx, promptcache.NewDiskStore(filepath.Join(root, "missing")), promptcache.Policy{TTL: time.Hour})
		Expect(err).ToNot(HaveOccurred())
		Expect(res.Files).To(BeZero())
		u, err := promptcache.NewDiskStore(filepath.Join(root, "missing")).Stat(ctx)
		Expect(err).ToNot(HaveOccurred())
		Expect(u.Files).To(BeZero())
	})

	It("deletes files past the time to live, with a per-model override", func() {
		oldA := put(root, modelA, nsX, "1", 10, 3*time.Hour, false)
		newA := put(root, modelA, nsX, "2", 10, 30*time.Minute, false)
		oldB := put(root, modelB, nsX, "3", 10, 3*time.Hour, false)
		res, err := promptcache.Sweep(ctx, promptcache.NewDiskStore(root), promptcache.Policy{
			TTL: 2 * time.Hour, ModelTTL: map[string]time.Duration{modelB: 4 * time.Hour}, Now: clock,
		})
		Expect(err).ToNot(HaveOccurred())
		Expect(res).To(Equal(promptcache.Result{Files: 1, Bytes: 10}))
		Expect(exists(oldA)).To(BeFalse())
		Expect(exists(newA)).To(BeTrue())
		Expect(exists(oldB)).To(BeTrue())
	})

	It("deletes partial writes older than an hour only", func() {
		stale := put(root, modelA, nsX, "1", 10, 2*time.Hour, true)
		fresh := put(root, modelA, nsX, "2", 10, 10*time.Minute, true)
		_, err := promptcache.Sweep(ctx, promptcache.NewDiskStore(root), promptcache.Policy{Now: clock})
		Expect(err).ToNot(HaveOccurred())
		Expect(exists(stale)).To(BeFalse())
		Expect(exists(fresh)).To(BeTrue())
	})

	It("evicts the least recently used files of a namespace over its quota, over all models", func() {
		x1 := put(root, modelA, nsX, "1", 100, 5*time.Minute, false)
		x2 := put(root, modelB, nsX, "2", 100, 4*time.Minute, false)
		x3 := put(root, modelA, nsX, "3", 100, 3*time.Minute, false)
		y1 := put(root, modelA, nsY, "4", 100, 10*time.Minute, false)
		shared := put(root, modelA, promptcache.SharedNSID, "5", 500, 20*time.Minute, false)
		_, err := promptcache.Sweep(ctx, promptcache.NewDiskStore(root), promptcache.Policy{QuotaBytes: 200, Now: clock})
		Expect(err).ToNot(HaveOccurred())
		Expect(exists(x1)).To(BeFalse())
		Expect(exists(x2)).To(BeTrue())
		Expect(exists(x3)).To(BeTrue())
		Expect(exists(y1)).To(BeTrue())
		Expect(exists(shared)).To(BeTrue(), "the quota is per user: the shared namespace has none")
	})

	It("evicts the least recently used files over the global cap", func() {
		a := put(root, modelA, nsX, "1", 100, 5*time.Minute, false)
		b := put(root, modelB, nsY, "2", 100, 4*time.Minute, false)
		c := put(root, modelA, nsY, "3", 100, 3*time.Minute, false)
		res, err := promptcache.Sweep(ctx, promptcache.NewDiskStore(root), promptcache.Policy{MaxBytes: 150, Now: clock})
		Expect(err).ToNot(HaveOccurred())
		Expect(res.Files).To(Equal(2))
		Expect(exists(a)).To(BeFalse())
		Expect(exists(b)).To(BeFalse())
		Expect(exists(c)).To(BeTrue())
	})

	It("touches only the names of the cache layout, and the metadata only on a whole purge", func() {
		dir := filepath.Join(root, "llama-cpp", pin+"-"+modelA)
		dirB := filepath.Join(root, "llama-cpp", pin+"-"+modelB)
		Expect(os.MkdirAll(dir, 0o700)).To(Succeed())
		Expect(os.MkdirAll(dirB, 0o700)).To(Succeed())
		meta := []string{filepath.Join(dir, ".lock"), filepath.Join(dir, ".speed"), filepath.Join(dir, "manifest.json")}
		metaB := filepath.Join(dirB, "manifest.json")
		foreign := []string{
			filepath.Join(dir, "notes.cache"),
			filepath.Join(root, "llama-cpp", "README"),
			filepath.Join(root, "other.cache"),
		}
		for _, f := range append(append(foreign, meta...), metaB) {
			Expect(os.WriteFile(f, []byte("x"), 0o600)).To(Succeed())
			t := now.Add(-1000 * time.Hour)
			Expect(os.Chtimes(f, t, t)).To(Succeed())
		}
		odd := filepath.Join(root, "llama-cpp", "not-a-model-dir")
		Expect(os.MkdirAll(odd, 0o700)).To(Succeed())
		oddFile := filepath.Join(odd, compat+"-"+nsX+"-"+strings.Repeat("1", 32)+".cache")
		Expect(os.WriteFile(oddFile, []byte("x"), 0o600)).To(Succeed())
		store := promptcache.NewDiskStore(root)

		_, err := promptcache.Sweep(ctx, store, promptcache.Policy{TTL: time.Second, MaxBytes: 1, QuotaBytes: 1, Now: clock})
		Expect(err).ToNot(HaveOccurred())
		_, err = promptcache.PurgeNamespace(ctx, store, nsX)
		Expect(err).ToNot(HaveOccurred())
		for _, f := range append(append(foreign, meta...), oddFile, metaB) {
			Expect(exists(f)).To(BeTrue(), f)
		}

		_, err = promptcache.PurgeModel(ctx, store, modelA)
		Expect(err).ToNot(HaveOccurred())
		for _, f := range meta {
			Expect(exists(f)).To(BeFalse(), f)
		}
		Expect(exists(metaB)).To(BeTrue(), "another model keeps its manifest")

		_, err = promptcache.PurgeAll(ctx, store)
		Expect(err).ToNot(HaveOccurred())
		Expect(exists(metaB)).To(BeFalse())
		for _, f := range append(foreign, oddFile) {
			Expect(exists(f)).To(BeTrue(), f)
		}
	})

	It("puts, gets and deletes files of the layout and refuses other names", func() {
		store := promptcache.NewDiskStore(root)
		k := promptcache.Key{Backend: "llama-cpp", ModelDir: pin + "-" + modelA, Name: compat + "-" + nsX + "-" + strings.Repeat("e", 32) + ".cache"}
		Expect(store.Put(ctx, k, strings.NewReader("state"))).To(Succeed())
		r, err := store.Get(ctx, k)
		Expect(err).ToNot(HaveOccurred())
		b, _ := io.ReadAll(r)
		r.Close()
		Expect(string(b)).To(Equal("state"))
		list, err := store.List(ctx, nsX)
		Expect(err).ToNot(HaveOccurred())
		Expect(list).To(HaveLen(1))
		Expect(list[0].Model16).To(Equal(modelA))
		Expect(list[0].Size).To(Equal(int64(5)))
		Expect(store.Delete(ctx, k)).To(Succeed())
		Expect(store.Delete(ctx, k)).To(Succeed(), "a file already gone is no error")

		for _, bad := range []promptcache.Key{
			{Backend: "llama-cpp", ModelDir: pin + "-" + modelA, Name: "../escape"},
			{Backend: "..", ModelDir: pin + "-" + modelA, Name: k.Name},
			{Backend: "llama-cpp", ModelDir: "x", Name: k.Name},
		} {
			Expect(store.Put(ctx, bad, strings.NewReader("x"))).To(MatchError(promptcache.ErrBadKey))
			Expect(store.Delete(ctx, bad)).To(MatchError(promptcache.ErrBadKey))
		}
	})

	It("purges everything, one model or one namespace", func() {
		ax := put(root, modelA, nsX, "1", 10, time.Minute, false)
		ay := put(root, modelA, nsY, "2", 10, time.Minute, true)
		bx := put(root, modelB, nsX, "3", 10, time.Minute, false)
		by := put(root, modelB, nsY, "4", 10, time.Minute, false)

		res, err := promptcache.PurgeNamespace(ctx, promptcache.NewDiskStore(root), nsY)
		Expect(err).ToNot(HaveOccurred())
		Expect(res.Files).To(Equal(2))
		Expect(exists(ay)).To(BeFalse())
		Expect(exists(by)).To(BeFalse())
		Expect(exists(ax)).To(BeTrue())

		res, err = promptcache.PurgeModel(ctx, promptcache.NewDiskStore(root), modelA)
		Expect(err).ToNot(HaveOccurred())
		Expect(res.Files).To(Equal(1))
		Expect(exists(ax)).To(BeFalse())
		Expect(exists(bx)).To(BeTrue())

		res, err = promptcache.PurgeAll(ctx, promptcache.NewDiskStore(root))
		Expect(err).ToNot(HaveOccurred())
		Expect(res).To(Equal(promptcache.Result{Files: 1, Bytes: 10}))
		Expect(exists(bx)).To(BeFalse())
	})

	It("purges only what was used before the time of the request, so a late or repeated purge keeps newer files", func() {
		old := put(root, modelA, nsX, "1", 10, 2*time.Hour, false)
		fresh := put(root, modelA, nsY, "2", 10, time.Minute, false)
		oldB := put(root, modelB, nsX, "3", 10, 2*time.Hour, false)
		manifestA := filepath.Join(root, "llama-cpp", pin+"-"+modelA, promptcache.ManifestName)
		manifestB := filepath.Join(root, "llama-cpp", pin+"-"+modelB, promptcache.ManifestName)
		for _, f := range []string{manifestA, manifestB} {
			Expect(os.WriteFile(f, []byte("{}"), 0o600)).To(Succeed())
		}
		store := promptcache.NewDiskStore(root)

		res, err := promptcache.PurgeModelBefore(ctx, store, modelA, now.Add(-time.Hour))
		Expect(err).ToNot(HaveOccurred())
		Expect(res.Files).To(Equal(1))
		Expect(exists(old)).To(BeFalse())
		Expect(exists(fresh)).To(BeTrue(), "used after the purge was asked for")
		Expect(exists(manifestA)).To(BeTrue(), "the directory still holds a file that needs its manifest")
		Expect(exists(oldB)).To(BeTrue(), "another model")

		res, err = promptcache.PurgeAllBefore(ctx, store, now.Add(-time.Hour))
		Expect(err).ToNot(HaveOccurred())
		Expect(res.Files).To(Equal(1))
		Expect(exists(oldB)).To(BeFalse())
		Expect(exists(manifestB)).To(BeFalse(), "an emptied directory loses its metadata")
		Expect(exists(fresh)).To(BeTrue())

		_, err = promptcache.PurgeModelBefore(ctx, store, "../x", now)
		Expect(err).To(MatchError(promptcache.ErrBadID))
	})

	It("refuses an id that is not 16 hex characters", func() {
		_, err := promptcache.PurgeModel(ctx, promptcache.NewDiskStore(root), "../../etc")
		Expect(err).To(MatchError(promptcache.ErrBadID))
		_, err = promptcache.PurgeNamespace(ctx, promptcache.NewDiskStore(root), "ABCDEF0123456789")
		Expect(err).To(MatchError(promptcache.ErrBadID))
	})

	It("reports the usage in total, per model and per namespace", func() {
		put(root, modelA, nsX, "1", 10, time.Minute, false)
		put(root, modelA, nsY, "2", 20, time.Minute, false)
		put(root, modelB, nsX, "3", 30, time.Minute, true)
		u, err := promptcache.NewDiskStore(root).Stat(ctx)
		Expect(err).ToNot(HaveOccurred())
		Expect(u.Bytes).To(Equal(int64(60)))
		Expect(u.Files).To(Equal(3))
		Expect(u.Models[modelA]).To(Equal(promptcache.Usage{Bytes: 30, Files: 2}))
		Expect(u.Models[modelB]).To(Equal(promptcache.Usage{Bytes: 30, Files: 1}))
		Expect(u.Namespaces[nsX]).To(Equal(promptcache.Usage{Bytes: 40, Files: 2}))
		Expect(u.Namespaces[nsY]).To(Equal(promptcache.Usage{Bytes: 20, Files: 1}))
	})
})

var _ = Describe("the read-speed probe", func() {
	It("measures a speed and leaves no file behind", func() {
		dir := GinkgoT().TempDir()
		mbps, err := promptcache.MeasureTransferSpeed(dir, 1<<20)
		if err == promptcache.ErrNoCacheDrop {
			Skip("no page cache drop on this platform")
		}
		Expect(err).ToNot(HaveOccurred())
		Expect(mbps).To(BeNumerically(">", 0))
		left, err := os.ReadDir(dir)
		Expect(err).ToNot(HaveOccurred())
		Expect(left).To(BeEmpty())
	})

	It("probes a root once and caches the result", func() {
		dir := GinkgoT().TempDir()
		_, ok := promptcache.TransferSpeed(dir)
		Expect(ok).To(BeFalse())
		done := make(chan error, 1)
		promptcache.StartTransferProbe(dir, func(_ float64, err error) { done <- err })
		var err error
		Eventually(done, 30*time.Second).Should(Receive(&err))
		if err == promptcache.ErrNoCacheDrop {
			Skip("no page cache drop on this platform")
		}
		Expect(err).ToNot(HaveOccurred())
		v, ok := promptcache.TransferSpeed(dir)
		Expect(ok).To(BeTrue())
		Expect(v).To(BeNumerically(">", 0))
		// A second start is a no-op: the callback never runs.
		promptcache.StartTransferProbe(dir, func(float64, error) { done <- nil })
		Consistently(done, 200*time.Millisecond).ShouldNot(Receive())
	})
})
