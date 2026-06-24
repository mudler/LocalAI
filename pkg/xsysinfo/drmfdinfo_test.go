package xsysinfo

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// i915FdInfo is a captured /proc/<pid>/fdinfo/<fd> from a llama-cpp
// process holding an Intel Arc render-node fd. "local0" is i915's
// device-local VRAM region; system0 is host-visible buffer mirror.
const i915FdInfo = `pos:	0
flags:	02100002
mnt_id:	16
ino:	1234
drm-driver:	i915
drm-client-id:	42
drm-pdev:	0000:03:00.0
drm-total-system0:	312 KiB
drm-resident-system0:	312 KiB
drm-total-local0:	5396348 KiB
drm-resident-local0:	5396348 KiB
drm-total-stolen-local0:	0
drm-resident-stolen-local0:	0
drm-engine-render:	1234567 ns
drm-engine-copy:	2345 ns
drm-engine-video:	0 ns
drm-engine-capacity-video:	2
`

// amdgpuFdInfo mirrors the i915 schema with AMD's region names. amdgpu
// uses "vram0" for device-local and "gtt0" for host-pinned memory.
const amdgpuFdInfo = `pos:	0
flags:	02100002
mnt_id:	16
drm-driver:	amdgpu
drm-pdev:	0000:0a:00.0
drm-total-vram0:	8589934592 B
drm-resident-vram0:	8589934592 B
drm-total-gtt0:	1048576 B
drm-resident-gtt0:	1048576 B
drm-engine-gfx:	123456 ns
`

// systemOnlyFdInfo: a DRM client that only allocates host buffers
// (CPU-only fallback, GUI compositor, etc.). VRAM total must be 0.
const systemOnlyFdInfo = `drm-driver:	i915
drm-total-system0:	16384 KiB
drm-resident-system0:	16384 KiB
drm-total-local0:	0
`

// noDRMFdInfo: regular file fd (e.g. socket, pipe). Parser must return
// 0 without panicking.
const noDRMFdInfo = `pos:	0
flags:	02100002
mnt_id:	16
ino:	5678
`

// bareBytesFdInfo: older kernels emit byte counts without a unit
// suffix. Must be parsed as raw bytes, not multiplied by 1024.
const bareBytesFdInfo = `drm-driver:	xe
drm-total-vram0:	17179869184
drm-resident-vram0:	17179869184
`

var _ = Describe("parseDRMFdInfoVRAM", func() {
	DescribeTable("extracts device-local VRAM totals from fdinfo",
		func(input string, want uint64) {
			Expect(parseDRMFdInfoVRAM([]byte(input))).To(Equal(want))
		},
		Entry("empty input", "", uint64(0)),
		Entry("non-DRM fdinfo", noDRMFdInfo, uint64(0)),
		Entry("system-only client reports 0 VRAM", systemOnlyFdInfo, uint64(0)),
		Entry("i915 local0 in KiB", i915FdInfo, uint64(5396348*1024)),
		Entry("amdgpu vram0 in bytes", amdgpuFdInfo, uint64(8589934592)),
		Entry("xe vram0 bare bytes", bareBytesFdInfo, uint64(17179869184)),
	)
})

var _ = Describe("parseDRMFdInfoBytes", func() {
	DescribeTable("parses sizes with and without unit suffixes",
		func(in string, want uint64) {
			Expect(parseDRMFdInfoBytes(in)).To(Equal(want))
		},
		Entry("bare bytes", "\t1024", uint64(1024)),
		Entry("KiB", "\t1024 KiB", uint64(1024*1024)),
		Entry("MiB", "\t512 MiB", uint64(512*1024*1024)),
		Entry("GiB", "\t2 GiB", uint64(2*1024*1024*1024)),
		Entry("unrecognised unit falls through to raw bytes", "\t1024 B", uint64(1024)),
		Entry("empty", "", uint64(0)),
		Entry("not a number", "\tnotanumber KiB", uint64(0)),
	)
})

var _ = Describe("isVRAMRegion", func() {
	DescribeTable("recognises device-local regions",
		func(region string, want bool) {
			Expect(isVRAMRegion(region)).To(Equal(want))
		},
		Entry("local0", "local0", true),
		Entry("local1", "local1", true),
		Entry("vram0", "vram0", true),
		Entry("vram1", "vram1", true),
		Entry("system0", "system0", false),
		Entry("gtt0", "gtt0", false),
		Entry("stolen-local0", "stolen-local0", false),
		Entry("stolen-system0", "stolen-system0", false),
		Entry("cpu", "cpu", false),
	)
})

var _ = Describe("applyDRMUsage", func() {
	const total = uint64(16225243136)
	base := GPUMemoryInfo{Name: "Arc A770", TotalVRAM: total, FreeVRAM: total}

	It("leaves defaults untouched when there is no usage", func() {
		got := applyDRMUsage(base, 0)
		Expect(got.UsedVRAM).To(Equal(uint64(0)))
		Expect(got.FreeVRAM).To(Equal(total))
		Expect(got.UsagePercent).To(Equal(float64(0)))
	})

	It("rederives free and percent from usage", func() {
		used := uint64(5_396_348 * 1024)
		got := applyDRMUsage(base, used)
		Expect(got.UsedVRAM).To(Equal(used))
		Expect(got.FreeVRAM).To(Equal(total - used))
		Expect(got.UsagePercent).To(Equal(float64(used) / float64(total) * 100))
	})

	It("clamps over-commit to total", func() {
		got := applyDRMUsage(base, total*2)
		Expect(got.UsedVRAM).To(Equal(total))
		Expect(got.FreeVRAM).To(Equal(uint64(0)))
	})

	It("guards against div-by-zero on zero total", func() {
		got := applyDRMUsage(GPUMemoryInfo{}, 1024)
		Expect(got.UsagePercent).To(Equal(float64(0)))
	})
})
