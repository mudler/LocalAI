package promptcache_test

import (
	"os"
	"path/filepath"
	"runtime"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/mudler/LocalAI/pkg/promptcache"
)

const gib = uint64(1) << 30

// fakeStats gives fixed numbers per path prefix; free follows the files the
// janitor deletes under the cache root, like a real disk.
type fakeStats struct {
	root      string
	total     uint64
	baseFree  uint64 // free space with an empty cache
	rootDev   uint64
	modelsDev uint64
}

func (f *fakeStats) Stat(path string) (promptcache.FSStats, error) {
	u, _ := promptcache.NewDiskStore(f.root).Stat(ctx)
	dev := f.rootDev
	if filepath.Base(path) == "models" {
		dev = f.modelsDev
	}
	return promptcache.FSStats{Free: f.baseFree - uint64(u.Bytes), Total: f.total, Device: dev}, nil
}

var _ = Describe("the free-space watermarks", func() {
	It("uses max(15% of the disk, 10 GiB) and 5% of the disk", func() {
		Expect(promptcache.LowWatermark(1000 * gib)).To(Equal(150 * gib))
		Expect(promptcache.LowWatermark(20 * gib)).To(Equal(10 * gib))
		Expect(promptcache.CriticalWatermark(1000 * gib)).To(Equal(50 * gib))
	})

	It("takes overrides of the watermarks, the critical one never above the low one", func() {
		low, critical := promptcache.Watermarks(1000*gib, 2*gib, 0)
		Expect(low).To(Equal(2 * gib))
		Expect(critical).To(Equal(2*gib), "5% of 1000 GiB is clamped to the low watermark")
		low, critical = promptcache.Watermarks(1000*gib, 0, gib)
		Expect(low).To(Equal(150 * gib))
		Expect(critical).To(Equal(gib))
	})

	var (
		root string
		fs   *fakeStats
		j    *promptcache.Janitor
	)
	clock := func() time.Time { return now }

	BeforeEach(func() {
		root = filepath.Join(GinkgoT().TempDir(), "prompt-cache")
		Expect(os.MkdirAll(root, 0o700)).To(Succeed())
		// A 100 GiB disk: low watermark 15 GiB, critical 5 GiB.
		fs = &fakeStats{root: root, total: 100 * gib, rootDev: 1, modelsDev: 2}
		j = &promptcache.Janitor{Root: root, Stats: fs}
	})

	It("is on with room on the disk, and the effective cap is the configured cap", func() {
		fs.baseFree = 50 * gib
		st, err := j.Sweep(ctx, promptcache.Policy{MaxBytes: 1 << 30, Now: clock})
		Expect(err).ToNot(HaveOccurred())
		Expect(st.State).To(Equal(promptcache.StateOn))
		Expect(st.EffectiveCapBytes).To(Equal(int64(1 << 30)))
		Expect(st.LowWatermarkBytes).To(Equal(15 * gib))
		Expect(st.CriticalWatermarkBytes).To(Equal(5 * gib))
	})

	It("shrinks the effective cap to the free space over the low watermark", func() {
		fs.baseFree = 15*gib + 300
		put(root, modelA, nsX, "1", 100, 3*time.Minute, false)
		st, err := j.Sweep(ctx, promptcache.Policy{MaxBytes: 1 << 30, Now: clock})
		Expect(err).ToNot(HaveOccurred())
		Expect(st.State).To(Equal(promptcache.StateOn))
		// cache 100 + free (15 GiB + 200) - low (15 GiB)
		Expect(st.EffectiveCapBytes).To(Equal(int64(300)))
		Expect(st.Removed.Files).To(BeZero())
	})

	It("pauses under the low watermark and evicts the oldest files only until it is met", func() {
		// Free with an empty cache: low watermark + 150 bytes. Three files
		// of 100 bytes leave 150 bytes too few: two of them must go, the
		// newest stays.
		fs.baseFree = 15*gib + 150
		a := put(root, modelA, nsX, "1", 100, 5*time.Minute, false)
		b := put(root, modelB, nsY, "2", 100, 4*time.Minute, false)
		c := put(root, modelA, nsY, "3", 100, 3*time.Minute, false)
		st, err := j.Sweep(ctx, promptcache.Policy{Now: clock})
		Expect(err).ToNot(HaveOccurred())
		Expect(st.State).To(Equal(promptcache.StatePaused))
		Expect(st.Removed.Files).To(Equal(2))
		Expect(exists(a)).To(BeFalse())
		Expect(exists(b)).To(BeFalse())
		Expect(exists(c)).To(BeTrue())
		Expect(st.CacheBytes).To(Equal(int64(100)))
	})

	It("counts what the time to live freed before it evicts for the watermark", func() {
		// 150 bytes short of the low mark with all files. The expired file
		// frees 100, so one more file of 100 must go, not two.
		fs.baseFree = 15*gib + 250
		expired := put(root, modelA, nsX, "1", 100, 30*24*time.Hour, false)
		a := put(root, modelA, nsX, "2", 100, 5*time.Minute, false)
		b := put(root, modelB, nsY, "3", 100, 4*time.Minute, false)
		c := put(root, modelA, nsY, "4", 100, 3*time.Minute, false)
		st, err := j.Sweep(ctx, promptcache.Policy{TTL: 24 * time.Hour, Now: clock})
		Expect(err).ToNot(HaveOccurred())
		Expect(exists(expired)).To(BeFalse())
		Expect(exists(a)).To(BeFalse())
		Expect(exists(b)).To(BeTrue())
		Expect(exists(c)).To(BeTrue())
		Expect(st.Removed.Files).To(Equal(2))
		Expect(st.FreeBytes).To(Equal(15*gib + 50))
	})

	It("empties the cache when even an empty cache leaves too little free space", func() {
		fs.baseFree = 10 * gib
		put(root, modelA, nsX, "1", 100, time.Minute, false)
		st, err := j.Sweep(ctx, promptcache.Policy{Now: clock})
		Expect(err).ToNot(HaveOccurred())
		Expect(st.State).To(Equal(promptcache.StatePaused))
		Expect(st.EffectiveCapBytes).To(BeZero())
		Expect(st.CacheBytes).To(BeZero())
	})

	It("disables under the critical watermark and comes back only above the low one", func() {
		fs.baseFree = 4 * gib
		st, _ := j.Sweep(ctx, promptcache.Policy{Now: clock})
		Expect(st.State).To(Equal(promptcache.StateDisabled))

		fs.baseFree = 10 * gib // out of the critical zone, still under low
		st, _ = j.Sweep(ctx, promptcache.Policy{Now: clock})
		Expect(st.State).To(Equal(promptcache.StateDisabled), "hysteresis")

		fs.baseFree = 20 * gib
		st, _ = j.Sweep(ctx, promptcache.Policy{Now: clock})
		Expect(st.State).To(Equal(promptcache.StateOn))

		fs.baseFree = 10 * gib
		st, _ = j.Sweep(ctx, promptcache.Policy{Now: clock})
		Expect(st.State).To(Equal(promptcache.StatePaused), "from on, the low watermark only pauses")
	})

	It("applies the watermark overrides of the policy", func() {
		fs.baseFree = 3 * gib
		st, _ := j.Sweep(ctx, promptcache.Policy{Now: clock, LowFreeBytes: 2 * gib, CriticalFreeBytes: gib})
		Expect(st.State).To(Equal(promptcache.StateOn))
		Expect(st.LowWatermarkBytes).To(Equal(2 * gib))
		Expect(st.CriticalWatermarkBytes).To(Equal(gib))
	})

	It("uses the numbers of the models filesystem when the models share it", func() {
		fs.baseFree = 50 * gib
		j.ModelsPath = filepath.Join(filepath.Dir(root), "models")
		Expect(os.MkdirAll(j.ModelsPath, 0o700)).To(Succeed())
		st, _ := j.Sweep(ctx, promptcache.Policy{Now: clock})
		Expect(st.SharesModelsDisk).To(BeFalse())

		fs.modelsDev = fs.rootDev
		st, _ = j.Sweep(ctx, promptcache.Policy{Now: clock})
		Expect(st.SharesModelsDisk).To(BeTrue())
	})

	It("reports the usage with the status of the last sweep", func() {
		fs.baseFree = 50 * gib
		put(root, modelA, nsX, "1", 100, time.Minute, false)
		_, err := j.Sweep(ctx, promptcache.Policy{Now: clock})
		Expect(err).ToNot(HaveOccurred())
		u, err := j.Usage(ctx)
		Expect(err).ToNot(HaveOccurred())
		Expect(u.Bytes).To(Equal(int64(100)))
		Expect(u.Status.State).To(Equal(promptcache.StateOn))
		Expect(u.Status.FreeBytes).To(Equal(50*gib - 100))
	})

	It("runs without watermarks when the filesystem cannot be read", func() {
		j.Stats = nil
		st, err := j.Sweep(ctx, promptcache.Policy{MaxBytes: 10, Now: clock})
		Expect(err).ToNot(HaveOccurred())
		Expect(st.State).To(Equal(promptcache.StateUnknown))
		Expect(st.EffectiveCapBytes).To(Equal(int64(10)))
	})

	It("reads a real filesystem", func() {
		s, err := promptcache.OSStats{}.Stat(root)
		if err != nil {
			Skip("no filesystem stats on this platform")
		}
		Expect(s.Total).To(BeNumerically(">", 0))
	})

	It("names the type of a real filesystem and does not call a temporary directory remote", func() {
		s, err := promptcache.OSStats{}.Stat(root)
		if err != nil {
			Skip("no filesystem stats on this platform")
		}
		if runtime.GOOS == "linux" || runtime.GOOS == "darwin" {
			Expect(s.FSType).ToNot(BeEmpty())
		}
		if s.FSType != "fuse" && s.FSType != "nfs" {
			Expect(s.Network).To(BeFalse())
		}
	})
})
