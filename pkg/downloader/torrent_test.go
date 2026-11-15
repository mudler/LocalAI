package downloader_test

import (
	"context"
	"crypto/sha1" // #nosec G505 -- BitTorrent v1 piece hashes are SHA-1
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"time"

	"github.com/anacrolix/torrent"
	"github.com/anacrolix/torrent/bencode"
	"github.com/anacrolix/torrent/metainfo"
	"github.com/anacrolix/torrent/storage"
	. "github.com/mudler/LocalAI/pkg/downloader"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("BitTorrent downloads", func() {
	var (
		dir       string
		savedCfg  TorrentSettings
		savedStal time.Duration
		savedPF   string
		savedHF   string
		savedTry  int
		files     map[string][]byte
	)

	BeforeEach(func() {
		dir = GinkgoT().TempDir()
		savedCfg, savedStal, savedPF, savedHF, savedTry = TorrentConfig, DownloadStallTimeout, PirateFaceEndpoint, HF_ENDPOINT, DownloadRetryAttempts
		TorrentConfig = TorrentSettings{Mode: TorrentModeWebseed, MetadataTimeout: 3 * time.Second, Fallback: true}
		DownloadStallTimeout = 15 * time.Second
		DownloadRetryAttempts = 1
		files = map[string][]byte{
			"a.bin":     randBytes(100_000),
			"sub/b.bin": randBytes(150_000),
		}
	})
	AfterEach(func() {
		TorrentConfig, DownloadStallTimeout, PirateFaceEndpoint, HF_ENDPOINT, DownloadRetryAttempts = savedCfg, savedStal, savedPF, savedHF, savedTry
	})

	leftovers := func(root string) []string {
		var out []string
		_ = filepath.WalkDir(root, func(p string, d os.DirEntry, err error) error {
			if err == nil && !d.IsDir() && (strings.HasSuffix(p, ".partial") || strings.Contains(p, "torrent-scratch")) {
				out = append(out, p)
			}
			return nil
		})
		return out
	}

	Context("a .torrent file with a web seed", func() {
		var (
			tor  *testTorrent
			hits atomic.Int64
			srv  *httptest.Server
		)
		BeforeEach(func() {
			hits.Store(0)
			// The web seed URL is needed inside the torrent, so start the
			// server first and point it at the torrent afterwards.
			holder := &testTorrent{Name: "pack", Files: files}
			srv = webseedServer(holder, &hits)
			tor = newTestTorrent(filepath.Join(dir, "src"), "pack", files, srv.URL+"/")
			holder.Meta = tor.Meta
		})
		AfterEach(func() { srv.Close() })

		It("downloads only the file chosen by the #selector", func() {
			torrentPath := filepath.Join(dir, "pack.torrent")
			Expect(os.WriteFile(torrentPath, tor.Meta, 0o600)).To(Succeed())
			dest := filepath.Join(dir, "models", "out.bin")

			var lastPct float64
			err := URI(torrentPath+"#sub/b.bin").DownloadFile(dest, sha256Hex(files["sub/b.bin"]), 0, 1,
				func(_, _, _ string, pct float64) { lastPct = pct })
			Expect(err).ToNot(HaveOccurred())

			got, err := os.ReadFile(dest)
			Expect(err).ToNot(HaveOccurred())
			Expect(got).To(Equal(files["sub/b.bin"]))
			Expect(lastPct).To(BeNumerically("~", 100, 0.5))
			Expect(leftovers(dir + "/models")).To(BeEmpty())
			entries, _ := os.ReadDir(filepath.Join(dir, "models"))
			Expect(entries).To(HaveLen(1), "the other file of the torrent must not be written")
		})

		It("picks the file whose name matches the destination", func() {
			torrentPath := filepath.Join(dir, "pack.torrent")
			Expect(os.WriteFile(torrentPath, tor.Meta, 0o600)).To(Succeed())
			dest := filepath.Join(dir, "models", "a.bin")
			Expect(URI("file://"+torrentPath).DownloadFile(dest, "", 0, 1, nil)).To(Succeed())
			got, _ := os.ReadFile(dest)
			Expect(got).To(Equal(files["a.bin"]))
		})

		It("refuses to guess when several files match nothing", func() {
			torrentPath := filepath.Join(dir, "pack.torrent")
			Expect(os.WriteFile(torrentPath, tor.Meta, 0o600)).To(Succeed())
			err := URI(torrentPath).DownloadFile(filepath.Join(dir, "models", "model.gguf"), "", 0, 1, nil)
			Expect(err).To(MatchError(ErrTorrentFileSelection))
			Expect(err.Error()).To(ContainSubstring("a.bin"))
			Expect(hits.Load()).To(BeZero())
		})

		It("reads the .torrent from an HTTP server", func() {
			meta := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != "/pack.torrent" {
					http.NotFound(w, r)
					return
				}
				_, _ = w.Write(tor.Meta)
			}))
			defer meta.Close()
			dest := filepath.Join(dir, "models", "x.bin")
			Expect(URI(meta.URL+"/pack.torrent#a.bin").DownloadFile(dest, "", 0, 1, nil)).To(Succeed())
			got, _ := os.ReadFile(dest)
			Expect(got).To(Equal(files["a.bin"]))
		})

		It("rejects a wrong SHA-256 and leaves nothing behind", func() {
			torrentPath := filepath.Join(dir, "pack.torrent")
			Expect(os.WriteFile(torrentPath, tor.Meta, 0o600)).To(Succeed())
			dest := filepath.Join(dir, "models", "x.bin")
			err := URI(torrentPath+"#a.bin").DownloadFile(dest, strings.Repeat("0", 64), 0, 1, nil)
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("SHA mismatch"))
			Expect(dest).ToNot(BeAnExistingFile())
			Expect(leftovers(dir + "/models")).To(BeEmpty())
		})

		It("finishes a download that was interrupted", func() {
			torrentPath := filepath.Join(dir, "pack.torrent")
			Expect(os.WriteFile(torrentPath, tor.Meta, 0o600)).To(Succeed())
			dest := filepath.Join(dir, "models", "x.bin")
			Expect(os.MkdirAll(filepath.Dir(dest), 0o750)).To(Succeed())
			// Two pieces of the file are already on disk.
			Expect(os.WriteFile(dest+".partial", files["a.bin"][:2*testPieceLength], 0o600)).To(Succeed())
			hits.Store(0)
			Expect(URI(torrentPath+"#a.bin").DownloadFile(dest, sha256Hex(files["a.bin"]), 0, 1, nil)).To(Succeed())
			got, _ := os.ReadFile(dest)
			Expect(got).To(Equal(files["a.bin"]))
			// Fetching a.bin from scratch takes 5 requests: pieces 0 to 2, and
			// piece 3, which also holds the first bytes of the next file and so
			// needs a request for each file. The 2 pieces on disk must not be
			// fetched again, which leaves 3.
			Expect(hits.Load()).To(BeNumerically("<=", 3))
			// The piece-completion store is in memory: nothing but the file is left.
			entries, _ := os.ReadDir(filepath.Dir(dest))
			Expect(entries).To(HaveLen(1))
		})

		It("honours a user cancel by dropping the partial", func() {
			slow := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				select {
				case <-r.Context().Done():
				case <-time.After(10 * time.Second):
				}
			}))
			defer slow.Close()
			slowTor := newTestTorrent(filepath.Join(dir, "src2"), "pack", files, slow.URL+"/")
			torrentPath := filepath.Join(dir, "slow.torrent")
			Expect(os.WriteFile(torrentPath, slowTor.Meta, 0o600)).To(Succeed())
			dest := filepath.Join(dir, "models", "x.bin")

			ctx, cancel := context.WithCancelCause(context.Background())
			go func() {
				time.Sleep(600 * time.Millisecond)
				cancel(ErrUserCancelled)
			}()
			err := URI(torrentPath+"#a.bin").DownloadFileWithContext(ctx, dest, "", 0, 1, nil)
			Expect(err).To(HaveOccurred())
			Expect(dest).ToNot(BeAnExistingFile())
			Expect(leftovers(dir + "/models")).To(BeEmpty())
		})
	})

	Context("hostile torrents", func() {
		It("refuses file names that climb out of the target directory", func() {
			info := metainfo.Info{
				PieceLength: testPieceLength,
				Name:        "pack",
				Files:       []metainfo.FileInfo{{Length: 10, Path: []string{"..", "evil.txt"}}},
			}
			h := sha1.Sum(make([]byte, 10)) // #nosec G401 -- test fixture
			info.Pieces = h[:]
			raw, err := bencode.Marshal(info)
			Expect(err).ToNot(HaveOccurred())
			mi := metainfo.MetaInfo{InfoBytes: raw}
			var sb strings.Builder
			Expect(mi.Write(&sb)).To(Succeed())
			p := filepath.Join(dir, "evil.torrent")
			Expect(os.WriteFile(p, []byte(sb.String()), 0o600)).To(Succeed())

			dest := filepath.Join(dir, "models", "evil.txt")
			err = URI(p+"#../evil.txt").DownloadFile(dest, "", 0, 1, nil)
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("unsafe"))
			Expect(filepath.Join(dir, "evil.txt")).ToNot(BeAnExistingFile())
			Expect(dest).ToNot(BeAnExistingFile())
		})

		It("refuses a torrent whose top-level name is a path", func() {
			info := metainfo.Info{PieceLength: testPieceLength, Name: "../up", Length: 10}
			h := sha1.Sum(make([]byte, 10)) // #nosec G401 -- test fixture
			info.Pieces = h[:]
			raw, _ := bencode.Marshal(info)
			mi := metainfo.MetaInfo{InfoBytes: raw}
			var sb strings.Builder
			Expect(mi.Write(&sb)).To(Succeed())
			p := filepath.Join(dir, "evil2.torrent")
			Expect(os.WriteFile(p, []byte(sb.String()), 0o600)).To(Succeed())
			err := URI(p).DownloadFile(filepath.Join(dir, "m", "x"), "", 0, 1, nil)
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("unsafe"))
		})
	})

	Context("a plain magnet link with no web seed", func() {
		var (
			tor    *testTorrent
			seeder *torrent.Client
			port   int
		)
		BeforeEach(func() {
			tor = newTestTorrent(filepath.Join(dir, "src"), "pack", files, "")
			cfg := torrent.NewDefaultClientConfig()
			cfg.ListenHost = func(string) string { return "127.0.0.1" }
			cfg.ListenPort = 0
			cfg.NoDHT, cfg.DisableTrackers, cfg.NoDefaultPortForwarding = true, true, true
			cfg.DisableIPv6 = true
			cfg.Seed = true
			cfg.DefaultStorage = storage.NewFileOpts(storage.NewFileClientOpts{
				ClientBaseDir:   tor.Root,
				PieceCompletion: storage.NewMapPieceCompletion(),
			})
			var err error
			seeder, err = torrent.NewClient(cfg)
			Expect(err).ToNot(HaveOccurred())
			mi, err := metainfo.Load(strings.NewReader(string(tor.Meta)))
			Expect(err).ToNot(HaveOccurred())
			t, err := seeder.AddTorrent(mi)
			Expect(err).ToNot(HaveOccurred())
			Eventually(func() int64 { return t.BytesMissing() }, 10*time.Second, 50*time.Millisecond).Should(BeZero())
			port = seeder.LocalPort()
		})
		AfterEach(func() { seeder.Close() })

		It("gets the file list and the data from a peer", func() {
			TorrentConfig.Mode = TorrentModeSwarm
			magnet := fmt.Sprintf("magnet:?xt=urn:btih:%s&x.pe=127.0.0.1:%d#sub/b.bin", tor.InfoHash, port)
			dest := filepath.Join(dir, "models", "got.bin")
			Expect(URI(magnet).DownloadFile(dest, sha256Hex(files["sub/b.bin"]), 0, 1, nil)).To(Succeed())
			got, _ := os.ReadFile(dest)
			Expect(got).To(Equal(files["sub/b.bin"]))
			Expect(leftovers(dir + "/models")).To(BeEmpty())
		})

		It("fails with a clear error when nobody answers", func() {
			TorrentConfig.Mode = TorrentModeSwarm
			TorrentConfig.MetadataTimeout = time.Second
			magnet := "magnet:?xt=urn:btih:" + strings.Repeat("ab", 20) + "&x.pe=127.0.0.1:1"
			err := URI(magnet).DownloadFile(filepath.Join(dir, "models", "x.bin"), "", 0, 1, nil)
			Expect(err).To(MatchError(ErrNoPeers))
		})

		It("cannot read a magnet's file list in web seed mode", func() {
			magnet := "magnet:?xt=urn:btih:" + strings.Repeat("ab", 20)
			err := URI(magnet).DownloadFile(filepath.Join(dir, "models", "x.bin"), "", 0, 1, nil)
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("needs a .torrent"))
		})
	})

	Context("pirateface://", func() {
		const owner, repo = "acme", "tiny-model"
		var (
			tor     *testTorrent
			pf      *httptest.Server
			hf      *httptest.Server
			wsFails atomic.Bool
			hfHits  atomic.Int64
			rev     = strings.Repeat("c", 40)
		)
		BeforeEach(func() {
			wsFails.Store(false)
			hfHits.Store(0)
			name := owner + "__" + repo
			holder := &testTorrent{Name: name, Files: files}
			ws := webseedServer(holder, nil)
			DeferCleanup(ws.Close)
			wsHandler := ws.Config.Handler
			pf = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch {
				case r.URL.Path == "/api/torrents" && r.URL.Query().Get("repo") == owner+"/"+repo:
					list := []map[string]string{}
					for p, d := range files {
						list = append(list, map[string]string{"path": p, "sha256": sha256Hex(d)})
					}
					magnet := fmt.Sprintf("magnet:?xt=urn:btih:%s&ws=%s", tor.InfoHash, "http://"+r.Host+"/api/ws/"+owner+"/"+repo+"/"+rev+"/")
					_ = json.NewEncoder(w).Encode(map[string]any{
						"repoId": owner + "/" + repo, "revision": rev, "infohash": tor.InfoHash,
						"magnet": magnet, "torrent": "/api/torrents/" + tor.InfoHash + ".torrent",
						"hf": true, "quant": "q", "files": list,
					})
				case r.URL.Path == "/api/torrents/"+tor.InfoHash+".torrent":
					_, _ = w.Write(tor.Meta)
				case strings.HasPrefix(r.URL.Path, "/api/ws/"):
					if wsFails.Load() {
						http.Error(w, "gone", http.StatusNotFound)
						return
					}
					r.URL.Path = strings.TrimPrefix(r.URL.Path, "/api/ws/"+owner+"/"+repo+"/"+rev)
					wsHandler.ServeHTTP(w, r)
				default:
					http.NotFound(w, r)
				}
			}))
			DeferCleanup(pf.Close)
			tor = newTestTorrent(filepath.Join(dir, "src"), name, files, pf.URL+"/api/ws/"+owner+"/"+repo+"/"+rev+"/")
			holder.Meta = tor.Meta
			PirateFaceEndpoint = pf.URL

			hf = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				hfHits.Add(1)
				prefix := fmt.Sprintf("/%s/%s/resolve/%s/", owner, repo, rev)
				p, ok := strings.CutPrefix(r.URL.Path, prefix)
				data, found := files[p]
				if !ok || !found {
					http.NotFound(w, r)
					return
				}
				_, _ = w.Write(data)
			}))
			DeferCleanup(hf.Close)
			HF_ENDPOINT = hf.URL
		})

		It("resolves the repository, downloads over the web seed and checks the index SHA-256", func() {
			dest := filepath.Join(dir, "models", "b.bin")
			Expect(URI(fmt.Sprintf("pirateface://%s/%s/sub/b.bin", owner, repo)).DownloadFile(dest, "", 0, 1, nil)).To(Succeed())
			got, _ := os.ReadFile(dest)
			Expect(got).To(Equal(files["sub/b.bin"]))
			Expect(hfHits.Load()).To(BeZero(), "no Hugging Face request when the torrent works")
		})

		It("accepts a magnet link copied from the model page", func() {
			magnet := fmt.Sprintf("magnet:?xt=urn:btih:%s&ws=%s#a.bin", tor.InfoHash,
				pf.URL+"/api/ws/"+owner+"/"+repo+"/"+rev+"/")
			dest := filepath.Join(dir, "models", "a.bin")
			Expect(URI(magnet).DownloadFile(dest, sha256Hex(files["a.bin"]), 0, 1, nil)).To(Succeed())
			got, _ := os.ReadFile(dest)
			Expect(got).To(Equal(files["a.bin"]))
			Expect(hfHits.Load()).To(BeZero())
		})

		It("falls back to the pinned Hugging Face revision when the web seed fails", func() {
			wsFails.Store(true)
			dest := filepath.Join(dir, "models", "b.bin")
			Expect(URI(fmt.Sprintf("pirateface://%s/%s/sub/b.bin", owner, repo)).DownloadFile(dest, "", 0, 1, nil)).To(Succeed())
			got, _ := os.ReadFile(dest)
			Expect(got).To(Equal(files["sub/b.bin"]))
			Expect(hfHits.Load()).To(BeNumerically(">", 0))
			Expect(leftovers(dir + "/models")).To(BeEmpty())
		})

		It("does not fall back when the fallback is switched off", func() {
			wsFails.Store(true)
			TorrentConfig.Fallback = false
			err := URI(fmt.Sprintf("pirateface://%s/%s/a.bin", owner, repo)).DownloadFile(filepath.Join(dir, "m", "a.bin"), "", 0, 1, nil)
			Expect(err).To(HaveOccurred())
			Expect(hfHits.Load()).To(BeZero())
		})

		It("reports a model Pirate Face does not list", func() {
			err := URI("pirateface://nobody/nothing/x.gguf").DownloadFile(filepath.Join(dir, "m", "x"), "", 0, 1, nil)
			Expect(err).To(MatchError(ErrPirateFaceNotListed))
		})

		It("lists the files of a repository for the importer", func() {
			list, err := PirateFaceFiles(context.Background(), owner+"/"+repo)
			Expect(err).ToNot(HaveOccurred())
			Expect(list).To(HaveLen(2))
		})
	})
})
