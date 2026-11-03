package studio

import (
	"github.com/mudler/LocalAI/core/services/studio/connectors"
	"github.com/mudler/LocalAI/pkg/addons/contract"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("the file limit in effect", func() {
	declared := func(n int64) *contract.FileAbility { return &contract.FileAbility{MaxBytes: n} }

	DescribeTable("is the smallest of the host ceiling, the declared ability and the owner setting",
		func(ceiling int64, ability *contract.FileAbility, owner int64, want FileLimit) {
			Expect(EffectiveFileLimit(ceiling, ability, owner)).To(Equal(want))
		},
		Entry("the declared ability above the old inline cap stands", int64(100<<20), declared(20<<20), int64(0), FileLimit{Bytes: 20 << 20, Layer: LayerAddon}),
		Entry("the host ceiling below the rest wins", int64(8<<20), declared(20<<20), int64(0), FileLimit{Bytes: 8 << 20, Layer: LayerHost}),
		Entry("the owner setting below the rest wins", int64(100<<20), declared(512<<10), int64(64<<10), FileLimit{Bytes: 64 << 10, Layer: LayerOwner}),
		Entry("the owner setting cannot raise the limit", int64(100<<20), declared(256<<10), int64(50<<20), FileLimit{Bytes: 256 << 10, Layer: LayerAddon}),
		Entry("a tie names the layer the owner can change first", int64(100<<20), declared(256<<10), int64(256<<10), FileLimit{Bytes: 256 << 10, Layer: LayerOwner}),
		Entry("a ceiling of zero means the default ceiling", int64(0), declared(256<<10), int64(0), FileLimit{Bytes: 256 << 10, Layer: LayerAddon}),
		Entry("a kind that declares no ability for this direction gets the ceiling", int64(100<<20), nil, int64(0), FileLimit{Bytes: 100 << 20, Layer: LayerHost}),
	)

	It("gives a kind that declares no files ability no limit at all", func() {
		l := resolveFileLimits(100<<20, nil, "Slack", connectors.Binding{})
		Expect([]int64{l.Receive.Bytes, l.Send.Bytes, l.ReceiveMax, l.SendMax}).To(Equal([]int64{0, 0, 0, 0}))
	})

	DescribeTable("writes a size as a person reads it",
		func(n int64, want string) { Expect(formatFileSize(n)).To(Equal(want)) },
		Entry("bytes", int64(900), "900 bytes"),
		Entry("one byte", int64(1), "1 byte"),
		Entry("KiB", int64(512<<10), "512 KiB"),
		Entry("a fraction of KiB", int64(1536), "1.5 KiB"),
		Entry("MiB", int64(1<<20), "1 MiB"),
		Entry("a fraction of MiB", int64(5<<20+512<<10), "5.5 MiB"),
		Entry("GiB", int64(3<<30), "3 GiB"),
		Entry("TiB", int64(1<<40), "1 TiB"),
	)

	It("names the layer in words a person knows", func() {
		Expect(FileLimit{Bytes: 1 << 20, Layer: LayerOwner}.sentence("Slack")).To(Equal("1 MiB (limit set for this bot)"))
		Expect(FileLimit{Bytes: 1 << 20, Layer: LayerHost}.sentence("Slack")).To(Equal("1 MiB (limit of the platform)"))
		Expect(FileLimit{Bytes: 1 << 20, Layer: LayerAddon}.sentence("Slack")).To(Equal("1 MiB (limit of Slack)"))
		Expect(FileLimit{Bytes: 1 << 20, Layer: LayerAddon}.sentence("")).To(Equal("1 MiB (limit of this add-on)"))
	})
})
