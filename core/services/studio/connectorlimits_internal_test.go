package studio

import (
	"github.com/mudler/LocalAI/pkg/addons/contract"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("the file limit in effect", func() {
	declared := func(n int64) *contract.FileAbility { return &contract.FileAbility{MaxBytes: n} }

	DescribeTable("is the smallest of the host ceiling, the declared ability, the owner setting and the inline cap",
		func(ceiling int64, ability *contract.FileAbility, owner int64, want FileLimit) {
			Expect(EffectiveFileLimit(ceiling, ability, owner)).To(Equal(want))
		},
		Entry("an add-on that declares nothing gets the inline cap", int64(100<<20), nil, int64(0), FileLimit{Bytes: contract.MaxConnectorFileBytes, Layer: LayerInline}),
		Entry("a declared ability above the inline cap leaves the inline cap", int64(100<<20), declared(20<<20), int64(0), FileLimit{Bytes: contract.MaxConnectorFileBytes, Layer: LayerInline}),
		Entry("a declared ability below the inline cap wins", int64(100<<20), declared(256<<10), int64(0), FileLimit{Bytes: 256 << 10, Layer: LayerAddon}),
		Entry("the host ceiling below the rest wins", int64(128<<10), declared(256<<10), int64(0), FileLimit{Bytes: 128 << 10, Layer: LayerHost}),
		Entry("the owner setting below the rest wins", int64(100<<20), declared(512<<10), int64(64<<10), FileLimit{Bytes: 64 << 10, Layer: LayerOwner}),
		Entry("the owner setting cannot raise the limit", int64(100<<20), declared(256<<10), int64(50<<20), FileLimit{Bytes: 256 << 10, Layer: LayerAddon}),
		Entry("a tie names the layer the owner can change first", int64(100<<20), declared(256<<10), int64(256<<10), FileLimit{Bytes: 256 << 10, Layer: LayerOwner}),
		Entry("a ceiling of zero means the default ceiling", int64(0), declared(256<<10), int64(0), FileLimit{Bytes: 256 << 10, Layer: LayerAddon}),
	)

	It("lets the host ceiling bind an add-on that declares nothing", func() {
		Expect(EffectiveFileLimit(10<<10, nil, 0)).To(Equal(FileLimit{Bytes: 10 << 10, Layer: LayerHost}))
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
		Expect(FileLimit{Bytes: 1 << 20, Layer: LayerInline}.sentence("Slack")).To(Equal("1 MiB (limit of this add-on version)"))
		Expect(FileLimit{Bytes: 1 << 20, Layer: LayerOwner}.sentence("Slack")).To(Equal("1 MiB (limit set for this bot)"))
		Expect(FileLimit{Bytes: 1 << 20, Layer: LayerHost}.sentence("Slack")).To(Equal("1 MiB (limit of the platform)"))
		Expect(FileLimit{Bytes: 1 << 20, Layer: LayerAddon}.sentence("Slack")).To(Equal("1 MiB (limit of Slack)"))
		Expect(FileLimit{Bytes: 1 << 20, Layer: LayerAddon}.sentence("")).To(Equal("1 MiB (limit of this add-on)"))
	})
})
