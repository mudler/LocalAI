package downloader_test

import (
	"strings"

	. "github.com/mudler/LocalAI/pkg/downloader"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("magnet, .torrent and pirateface:// URIs", func() {
	const hash = "3accbbbe7e0070616755f5f7f4aadc3bbc4db813"
	const pfMagnet = "magnet:?xt=urn:btih:" + hash +
		"&xt=urn:btmh:12204a9b2e610ac225ecd1dc485c35177e267ec25fb871762fe71cf09b79a1714fcc" +
		"&tr=udp%3A%2F%2Ftracker.pirateface.co%3A6969%2Fannounce" +
		"&tr=http%3A%2F%2Ftracker.pirateface.co%3A6969%2Fannounce" +
		"&ws=https%3A%2F%2Fpirateface.co%2Fapi%2Fws%2Facme%2Fm%2F09816acd5d99df7be770d85ea30822623dab342c%2F"

	Describe("classification", func() {
		DescribeTable("LooksLikeTorrent",
			func(uri string, magnet, pf, file bool) {
				u := URI(uri)
				Expect(u.LooksLikeMagnet()).To(Equal(magnet))
				Expect(u.LooksLikePirateFace()).To(Equal(pf))
				Expect(u.LooksLikeTorrentFile()).To(Equal(file))
				Expect(u.LooksLikeTorrent()).To(Equal(magnet || pf || file))
			},
			Entry("magnet", pfMagnet, true, false, false),
			Entry("upper-case scheme", "MAGNET:?xt=urn:btih:"+hash, true, false, false),
			Entry("shorthand", "pirateface://acme/m/model.gguf", false, true, false),
			Entry("http .torrent", "https://example.com/a/b.torrent", false, false, true),
			Entry("http .torrent with a query", "https://example.com/b.torrent?token=1", false, false, true),
			Entry(".torrent with a selector", "https://example.com/b.torrent#dir/x.gguf", false, false, true),
			Entry("local path", "/models/b.torrent", false, false, true),
			Entry("file://", "file:///models/b.torrent", false, false, true),
			Entry("plain http file", "https://example.com/model.gguf", false, false, false),
			Entry("huggingface", "huggingface://acme/m/model.gguf", false, false, false),
			Entry("oci", "oci://example.com/x:1", false, false, false),
		)

		It("counts magnet and shorthand URIs as URLs", func() {
			Expect(URI(pfMagnet).LooksLikeURL()).To(BeTrue())
			Expect(URI("pirateface://acme/m/x.gguf").LooksLikeURL()).To(BeTrue())
		})

		It("reads the #selector", func() {
			Expect(URI(pfMagnet + "#sub/dir/x.gguf").TorrentSelector()).To(Equal("sub/dir/x.gguf"))
			Expect(URI(pfMagnet).TorrentSelector()).To(BeEmpty())
		})
	})

	Describe("ParseMagnet", func() {
		It("reads the parts of a Pirate Face magnet", func() {
			m, err := ParseMagnet(pfMagnet)
			Expect(err).ToNot(HaveOccurred())
			Expect(m.InfoHash).To(Equal(hash))
			Expect(m.Trackers).To(ConsistOf(
				"udp://tracker.pirateface.co:6969/announce",
				"http://tracker.pirateface.co:6969/announce"))
			Expect(m.WebSeeds).To(ConsistOf("https://pirateface.co/api/ws/acme/m/09816acd5d99df7be770d85ea30822623dab342c/"))
		})

		It("accepts a bare info hash", func() {
			m, err := ParseMagnet("magnet:?xt=urn:btih:" + strings.ToUpper(hash))
			Expect(err).ToNot(HaveOccurred())
			Expect(m.InfoHash).To(Equal(hash))
			Expect(m.WebSeeds).To(BeEmpty())
		})

		It("accepts a base32 info hash", func() {
			m, err := ParseMagnet("magnet:?xt=urn:btih:YOHEGP5YG5BFWKBBI2QFYNP4UQ3MO44A")
			Expect(err).ToNot(HaveOccurred())
			Expect(m.InfoHash).To(HaveLen(40))
		})

		It("reads display name, peers and file selection", func() {
			m, err := ParseMagnet("magnet:?xt=urn:btih:" + hash + "&dn=Model&x.pe=10.0.0.1:6881&so=0,2-3")
			Expect(err).ToNot(HaveOccurred())
			Expect(m.DisplayName).To(Equal("Model"))
			Expect(m.PeerAddrs).To(ConsistOf("10.0.0.1:6881"))
			Expect(m.SelectOnly).To(Equal([]int{0, 2, 3}))
		})

		DescribeTable("rejects",
			func(raw string) {
				_, err := ParseMagnet(raw)
				Expect(err).To(MatchError(ErrInvalidMagnet))
			},
			Entry("no scheme", "https://example.com"),
			Entry("no info hash", "magnet:?dn=x"),
			Entry("short hash", "magnet:?xt=urn:btih:abcd"),
			Entry("non-hex hash", "magnet:?xt=urn:btih:"+strings.Repeat("z", 40)),
			Entry("bad so", "magnet:?xt=urn:btih:"+hash+"&so=x"),
		)
	})

	Describe("pirateface:// URIs", func() {
		It("parses owner, repo and file", func() {
			ref, err := ParsePirateFaceURI("pirateface://acme/m/sub/dir/model.gguf")
			Expect(err).ToNot(HaveOccurred())
			Expect(ref).To(Equal(PirateFaceRef{Owner: "acme", Repo: "m", Path: "sub/dir/model.gguf"}))
			Expect(ref.RepoID()).To(Equal("acme/m"))
			Expect(string(ref.URI())).To(Equal("pirateface://acme/m/sub/dir/model.gguf"))
		})

		It("accepts a repository with no file", func() {
			ref, err := ParsePirateFaceURI("pirateface://acme/m")
			Expect(err).ToNot(HaveOccurred())
			Expect(ref.Path).To(BeEmpty())
		})

		DescribeTable("rejects",
			func(raw string) {
				_, err := ParsePirateFaceURI(raw)
				Expect(err).To(HaveOccurred())
			},
			Entry("owner only", "pirateface://acme"),
			Entry("empty repo", "pirateface://acme//x"),
			Entry("traversal", "pirateface://acme/m/../../etc/passwd"),
			Entry("backslash", `pirateface://acme/m/a\b`),
			Entry("other scheme", "huggingface://acme/m"),
		)

		It("recognises model page URLs", func() {
			ref, ok := PirateFaceRefFromPageURL("https://pirateface.co/bartowski/SmolLM2-135M-Instruct-GGUF")
			Expect(ok).To(BeTrue())
			Expect(ref.RepoID()).To(Equal("bartowski/SmolLM2-135M-Instruct-GGUF"))

			_, ok = PirateFaceRefFromPageURL("https://pirateface.co/bartowski/SmolLM2-135M-Instruct-GGUF/")
			Expect(ok).To(BeTrue())
			_, ok = PirateFaceRefFromPageURL("https://www.pirateface.co/a/b")
			Expect(ok).To(BeTrue())
		})

		DescribeTable("ignores other URLs",
			func(raw string) {
				_, ok := PirateFaceRefFromPageURL(raw)
				Expect(ok).To(BeFalse())
			},
			Entry("another host", "https://huggingface.co/a/b"),
			Entry("look-alike host", "https://pirateface.co.evil.example/a/b"),
			Entry("site root", "https://pirateface.co/"),
			Entry("owner page", "https://pirateface.co/bartowski"),
			Entry("api", "https://pirateface.co/api/torrents"),
			Entry("search", "https://pirateface.co/s/qwen"),
			Entry("not http", "ftp://pirateface.co/a/b"),
		)
	})

	Describe("file names", func() {
		It("names the file of a pirateface:// URI", func() {
			Expect(URI("pirateface://acme/m/sub/model.gguf").FilenameFromUrl()).To(Equal("model.gguf"))
			Expect(URI("pirateface://acme/m").FilenameFromUrl()).To(Equal("acme__m"))
		})
		It("uses the selector, then the display name, then the hash for a magnet", func() {
			Expect(URI(pfMagnet + "#sub/x.gguf").FilenameFromUrl()).To(Equal("x.gguf"))
			Expect(URI("magnet:?xt=urn:btih:" + hash + "&dn=Model.gguf").FilenameFromUrl()).To(Equal("Model.gguf"))
			Expect(URI("magnet:?xt=urn:btih:" + hash).FilenameFromUrl()).To(Equal(hash))
		})
	})
})
