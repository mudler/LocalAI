package downloader

import (
	"time"

	"github.com/anacrolix/torrent/metainfo"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("torrent internals", func() {
	entries := []torrentEntry{
		{Path: "README.md", Length: 10},
		{Path: "q4/model.gguf", Length: 100, Offset: 10},
		{Path: "q8/model.gguf", Length: 200, Offset: 110},
		{Path: "pad", Length: 5, Offset: 310, Pad: true},
	}

	Describe("selectTorrentFile", func() {
		It("takes an exact path from the selector", func() {
			e, err := selectTorrentFile(entries, "q8/model.gguf", nil, "/m/x")
			Expect(err).ToNot(HaveOccurred())
			Expect(e.Length).To(BeEquivalentTo(200))
		})
		It("takes a unique path suffix", func() {
			e, err := selectTorrentFile(entries, "README.md", nil, "/m/x")
			Expect(err).ToNot(HaveOccurred())
			Expect(e.Path).To(Equal("README.md"))
		})
		It("rejects an ambiguous selector", func() {
			_, err := selectTorrentFile(entries, "model.gguf", nil, "/m/x")
			Expect(err).To(MatchError(ErrTorrentFileSelection))
		})
		It("rejects a selector that matches nothing", func() {
			_, err := selectTorrentFile(entries, "nope", nil, "/m/x")
			Expect(err).To(MatchError(ErrTorrentFileSelection))
		})
		It("uses a single so= index", func() {
			e, err := selectTorrentFile(entries, "", []int{1}, "/m/x")
			Expect(err).ToNot(HaveOccurred())
			Expect(e.Path).To(Equal("q4/model.gguf"))
		})
		It("matches the destination path, longest first", func() {
			e, err := selectTorrentFile(entries, "", nil, "/models/q4/model.gguf")
			Expect(err).ToNot(HaveOccurred())
			Expect(e.Path).To(Equal("q4/model.gguf"))
		})
		It("matches a unique destination base name", func() {
			e, err := selectTorrentFile(entries, "", nil, "/models/README.md")
			Expect(err).ToNot(HaveOccurred())
			Expect(e.Path).To(Equal("README.md"))
		})
		It("refuses to guess in a multi-file torrent", func() {
			_, err := selectTorrentFile(entries, "", nil, "/models/other.bin")
			Expect(err).To(MatchError(ErrTorrentFileSelection))
		})
		It("takes the only real file, ignoring padding", func() {
			e, err := selectTorrentFile([]torrentEntry{{Path: "only.bin", Length: 1}, {Path: "pad", Pad: true}}, "", nil, "/m/x")
			Expect(err).ToNot(HaveOccurred())
			Expect(e.Path).To(Equal("only.bin"))
		})
	})

	Describe("torrentEntries", func() {
		DescribeTable("rejects unsafe names",
			func(info metainfo.Info) {
				_, err := torrentEntries(&info)
				Expect(err).To(HaveOccurred())
				Expect(err.Error()).To(ContainSubstring("unsafe"))
			},
			Entry("parent directory", metainfo.Info{Name: "t", Files: []metainfo.FileInfo{{Length: 1, Path: []string{"..", "x"}}}}),
			Entry("absolute path in one segment", metainfo.Info{Name: "t", Files: []metainfo.FileInfo{{Length: 1, Path: []string{"/etc/passwd"}}}}),
			Entry("segment with a slash", metainfo.Info{Name: "t", Files: []metainfo.FileInfo{{Length: 1, Path: []string{"a/../../b"}}}}),
			Entry("empty segment", metainfo.Info{Name: "t", Files: []metainfo.FileInfo{{Length: 1, Path: []string{"a", ""}}}}),
			Entry("backslash", metainfo.Info{Name: "t", Files: []metainfo.FileInfo{{Length: 1, Path: []string{`a\..\b`}}}}),
			Entry("single file named ..", metainfo.Info{Name: "..", Length: 1}),
			Entry("directory name that climbs", metainfo.Info{Name: "../x", Files: []metainfo.FileInfo{{Length: 1, Path: []string{"a"}}}}),
		)

		It("lists a single-file torrent by its name", func() {
			got, err := torrentEntries(&metainfo.Info{Name: "model.gguf", Length: 42})
			Expect(err).ToNot(HaveOccurred())
			Expect(got).To(Equal([]torrentEntry{{Path: "model.gguf", Length: 42}}))
		})
	})

	Describe("client network posture", func() {
		It("opens no socket at all in web seed mode", func() {
			cfg := newTorrentClientConfig(TorrentSettings{Mode: TorrentModeWebseed, DHT: true, ListenPort: 4242}, GinkgoT().TempDir())
			Expect(cfg.DisableTCP).To(BeTrue())
			Expect(cfg.DisableUTP).To(BeTrue())
			Expect(cfg.NoDHT).To(BeTrue())
			Expect(cfg.DisableTrackers).To(BeTrue())
			Expect(cfg.NoDefaultPortForwarding).To(BeTrue())
			Expect(cfg.DisableWebseeds).To(BeTrue(), "web seeds are served by the downloader, not the library")
		})

		It("listens on loopback only, with no DHT and no port mapping, by default", func() {
			cfg := newTorrentClientConfig(TorrentSettings{Mode: TorrentModeSwarm}, GinkgoT().TempDir())
			Expect(cfg.NoDHT).To(BeTrue())
			Expect(cfg.NoDefaultPortForwarding).To(BeTrue())
			Expect(cfg.ListenPort).To(BeZero())
			Expect(cfg.ListenHost("tcp4")).To(Equal("127.0.0.1"))
			Expect(cfg.ListenHost("tcp6")).To(Equal("::1"))
		})

		It("opens the configured port to the network only when asked", func() {
			cfg := newTorrentClientConfig(TorrentSettings{Mode: TorrentModeSwarm, ListenPort: 4242, DHT: true}, GinkgoT().TempDir())
			Expect(cfg.ListenPort).To(Equal(4242))
			Expect(cfg.ListenHost("tcp4")).To(BeEmpty())
			Expect(cfg.NoDHT).To(BeFalse())
			Expect(cfg.NoDefaultPortForwarding).To(BeFalse())
		})

		It("defaults to the safe posture", func() {
			Expect(DefaultTorrentSettings()).To(Equal(TorrentSettings{
				Mode: TorrentModeSwarm, MetadataTimeout: 90 * time.Second, Fallback: true,
			}))
		})
	})

	Describe("web seed URLs", func() {
		It("appends the torrent name and the escaped path to a directory base", func() {
			info := &metainfo.Info{Name: "my pack", Files: []metainfo.FileInfo{{Length: 1, Path: []string{"a b", "c.bin"}}}}
			u, ok := webseedFileURL("https://h/ws/", info, info.Files[0])
			Expect(ok).To(BeTrue())
			Expect(u).To(Equal("https://h/ws/my%20pack/a%20b/c.bin"))
		})
		It("uses a base without a trailing slash only for a single file", func() {
			single := &metainfo.Info{Name: "m.gguf", Length: 1}
			u, ok := webseedFileURL("https://h/m.gguf", single, metainfo.FileInfo{})
			Expect(ok).To(BeTrue())
			Expect(u).To(Equal("https://h/m.gguf"))
			multi := &metainfo.Info{Name: "p", Files: []metainfo.FileInfo{{Length: 1, Path: []string{"a"}}}}
			_, ok = webseedFileURL("https://h/m.gguf", multi, multi.Files[0])
			Expect(ok).To(BeFalse())
		})
	})
})
