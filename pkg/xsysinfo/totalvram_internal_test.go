package xsysinfo

import (
	"github.com/jaypipes/ghw/pkg/gpu"
	"github.com/jaypipes/ghw/pkg/memory"
	"github.com/jaypipes/ghw/pkg/topology"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("totalVRAM", func() {
	const gib = uint64(1) << 30

	card := func(bytes uint64) *gpu.GraphicsCard {
		return &gpu.GraphicsCard{Node: &topology.Node{Memory: &memory.Area{TotalUsableBytes: int64(bytes)}}}
	}

	It("prefers the vendor tools over ghw's NUMA node memory", func() {
		// An 8 GiB card in an LXC container: ghw reports the host node's
		// 62 GiB of RAM as the card's memory, nvidia-smi the real VRAM.
		infos := []GPUMemoryInfo{{TotalVRAM: 8 * gib}}
		Expect(totalVRAM(infos, []*gpu.GraphicsCard{card(62 * gib)})).To(Equal(8 * gib))
	})

	It("sums every device the vendor tools report", func() {
		infos := []GPUMemoryInfo{{TotalVRAM: 16 * gib}, {TotalVRAM: 12 * gib}}
		Expect(totalVRAM(infos, nil)).To(Equal(28 * gib))
	})

	It("falls back to ghw when the vendor tools report nothing", func() {
		cards := []*gpu.GraphicsCard{card(8 * gib), nil, {}, card(4 * gib)}
		Expect(totalVRAM(nil, cards)).To(Equal(12 * gib))
		Expect(totalVRAM([]GPUMemoryInfo{{TotalVRAM: 0}}, cards)).To(Equal(12 * gib))
	})

	It("returns 0 when nothing reports VRAM", func() {
		Expect(totalVRAM(nil, nil)).To(BeZero())
	})
})
