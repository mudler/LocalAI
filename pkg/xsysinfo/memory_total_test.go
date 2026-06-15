package xsysinfo

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("chooseTotalMemory", func() {
	const (
		gi128 = uint64(128) * 1024 * 1024 * 1024
		gi20  = uint64(20) * 1024 * 1024 * 1024
		gi10  = uint64(10) * 1024 * 1024 * 1024
	)

	// /proc/meminfo MemTotal is in kB; build a snippet for a given byte total.
	memInfo := func(bytes uint64) string {
		kb := bytes / 1024
		return "MemTotal:       " + itoa(kb) + " kB\nMemFree:        123 kB\n"
	}

	Context("bare metal (no cgroup cap, memory.max == max)", func() {
		It("uses the host sysinfo total", func() {
			// MemTotal mirrors sysinfo on bare metal.
			got := chooseTotalMemory("max\n", string(rune(0)), memInfo(gi128), gi128)
			Expect(got).To(Equal(gi128))
		})
	})

	Context("LXD/lxcfs container (MemTotal virtualized below host, no cap)", func() {
		It("uses the virtualized MemTotal, not the host sysinfo total", func() {
			// This is issue #8059: host sysinfo says 128Gi, but lxcfs
			// virtualizes /proc/meminfo MemTotal to 20Gi and there is no
			// cgroup cap. The corrected total must be 20Gi.
			got := chooseTotalMemory("max\n", "", memInfo(gi20), gi128)
			Expect(got).To(Equal(gi20))
		})
	})

	Context("cgroup v2 cap set below MemTotal", func() {
		It("uses the cgroup cap", func() {
			got := chooseTotalMemory(itoa(gi10)+"\n", "", memInfo(gi20), gi128)
			Expect(got).To(Equal(gi10))
		})
	})

	Context("cgroup v1 with the kernel unlimited sentinel", func() {
		It("ignores the sentinel and falls back to MemTotal", func() {
			got := chooseTotalMemory("", "9223372036854771712\n", memInfo(gi20), gi128)
			Expect(got).To(Equal(gi20))
		})
	})

	Context("all candidates empty/unlimited", func() {
		It("falls back to sysinfo total", func() {
			got := chooseTotalMemory("max\n", "", "", gi128)
			Expect(got).To(Equal(gi128))
		})
	})
})

// itoa is a tiny base-10 formatter to avoid importing strconv into the test.
func itoa(v uint64) string {
	if v == 0 {
		return "0"
	}
	var buf [20]byte
	i := len(buf)
	for v > 0 {
		i--
		buf[i] = byte('0' + v%10)
		v /= 10
	}
	return string(buf[i:])
}
