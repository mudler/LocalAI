package cli

import (
	"time"

	"github.com/mudler/LocalAI/pkg/downloader"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("TorrentFlags", Serial, func() {
	var savedCfg downloader.TorrentSettings
	var savedEndpoint string
	BeforeEach(func() {
		savedCfg, savedEndpoint = downloader.TorrentConfig, downloader.PirateFaceEndpoint
	})
	AfterEach(func() {
		downloader.TorrentConfig, downloader.PirateFaceEndpoint = savedCfg, savedEndpoint
	})

	It("installs the flags in the downloader", func() {
		Expect(TorrentFlags{
			TorrentMode: "webseed", TorrentListenPort: 6000, TorrentDHT: true,
			TorrentMetadataTimeout: 5 * time.Second, TorrentFallback: false,
			PirateFaceURL: "https://mirror.example/",
		}.Apply()).To(Succeed())
		Expect(downloader.TorrentConfig).To(Equal(downloader.TorrentSettings{
			Mode: "webseed", ListenPort: 6000, DHT: true, MetadataTimeout: 5 * time.Second,
		}))
		Expect(downloader.PirateFaceEndpoint).To(Equal("https://mirror.example"))
	})

	It("rejects an unknown mode and a bad port", func() {
		Expect(TorrentFlags{TorrentMode: "turbo"}.Apply()).ToNot(Succeed())
		Expect(TorrentFlags{TorrentMode: "swarm", TorrentListenPort: 70000}.Apply()).ToNot(Succeed())
	})

	It("keeps the current settings for a command built without defaults", func() {
		Expect(TorrentFlags{}.Apply()).To(Succeed())
		Expect(downloader.TorrentConfig).To(Equal(savedCfg))
	})
})
