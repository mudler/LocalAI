package fetch_test

import (
	"context"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/mudler/nib/plugin/claudeplugin"

	"github.com/mudler/LocalAI/core/services/plugins/fetch"
)

var _ = Describe("Fetcher", func() {
	var (
		ctx  context.Context
		root string
		f    *fetch.Fetcher
	)
	BeforeEach(func() {
		ctx = context.Background()
		root = filepath.Join(GinkgoT().TempDir(), "scratch")
		var err error
		f, err = fetch.New(fetch.Options{ScratchRoot: root, AllowFileURLs: true})
		Expect(err).NotTo(HaveOccurred())
	})

	urlEntry := func(name, u string) *claudeplugin.Entry {
		return &claudeplugin.Entry{Name: name, Source: claudeplugin.Source{Kind: claudeplugin.SourceURL, URL: u}}
	}
	read := func(dir, p string) string {
		b, err := os.ReadFile(filepath.Join(dir, filepath.FromSlash(p)))
		Expect(err).NotTo(HaveOccurred())
		return string(b)
	}

	Describe("FetchPlugin", func() {
		It("pins a plugin to a SHA and removes its scratch folder when done", func() {
			r := newRepo()
			first := r.commit(map[string][]byte{"skills/a/SKILL.md": []byte("one")})
			r.commit(map[string][]byte{"skills/a/SKILL.md": []byte("two")})

			got, err := f.FetchPlugin(ctx, fetch.MarketplaceSource{}, nil, urlEntry("p", r.url()), first)
			Expect(err).NotTo(HaveOccurred())
			Expect(got.SHA).To(Equal(first))
			Expect(read(got.Dir, "skills/a/SKILL.md")).To(Equal("one"))
			Expect(filepath.Join(got.Dir, ".git")).NotTo(BeADirectory())
			got.Done()
			got.Done()
			Expect(scratchEntries(root)).To(BeEmpty())
		})

		It("follows the default branch without a pin", func() {
			r := newRepo()
			r.commit(map[string][]byte{"f": []byte("one")})
			head := r.commit(map[string][]byte{"f": []byte("two")})
			got, err := f.FetchPlugin(ctx, fetch.MarketplaceSource{}, nil, urlEntry("p", r.url()), "")
			Expect(err).NotTo(HaveOccurred())
			defer got.Done()
			Expect(got.SHA).To(Equal(head))
			Expect(read(got.Dir, "f")).To(Equal("two"))
		})

		It("checks out only the folder of a git-subdir source, with no blob from outside it", func() {
			r := newRepo()
			sha := r.commit(map[string][]byte{
				"plugins/a/skills/s/SKILL.md": []byte("a skill"),
				"plugins/a/README.md":         []byte("a"),
				"plugins/b/README.md":         []byte("b"),
				"big/blob.bin":                randomBytes(64 << 10),
				"README.md":                   []byte("root"),
			})
			inside := r.blobsUnder(sha, "plugins/a")
			var present []string
			f.SetWrapGit(func(g claudeplugin.GitFunc) claudeplugin.GitFunc {
				return func(ctx context.Context, dir string, args ...string) (string, error) {
					out, err := g(ctx, dir, args...)
					if err == nil && len(args) > 0 && args[0] == "rev-parse" {
						cmd := exec.Command("git", "cat-file", "--batch-all-objects", "--batch-check=%(objecttype) %(objectname)")
						cmd.Dir = dir
						cmd.Env = []string{"PATH=" + os.Getenv("PATH"), "GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL=/dev/null", "HOME=" + dir}
						b, cerr := cmd.Output()
						Expect(cerr).NotTo(HaveOccurred())
						for _, l := range strings.Split(strings.TrimSpace(string(b)), "\n") {
							if typ, id, ok := strings.Cut(l, " "); ok && typ == "blob" {
								present = append(present, id)
							}
						}
					}
					return out, err
				}
			})
			e := &claudeplugin.Entry{Name: "a", Source: claudeplugin.Source{
				Kind: claudeplugin.SourceGitSubdir, URL: r.url(), Path: "plugins/a", SHA: sha,
			}}
			got, err := f.FetchPlugin(ctx, fetch.MarketplaceSource{}, nil, e, "")
			Expect(err).NotTo(HaveOccurred())
			defer got.Done()
			Expect(read(got.Dir, "skills/s/SKILL.md")).To(Equal("a skill"))
			Expect(filepath.Join(got.Dir, "plugins")).NotTo(BeAnExistingFile())
			Expect(present).NotTo(BeEmpty())
			for _, id := range present {
				Expect(inside).To(HaveKey(id), "blob %s is from outside the plugin folder", id)
			}
		})

		It("fetches a relative source from the marketplace repository at the snapshot's commit", func() {
			r := newRepo()
			snap := r.commit(map[string][]byte{
				".claude-plugin/marketplace.json": []byte(`{"name":"m","plugins":[{"name":"a","source":"./plugins/a"}]}`),
				"plugins/a/skills/s/SKILL.md":     []byte("old"),
			})
			r.commit(map[string][]byte{"plugins/a/skills/s/SKILL.md": []byte("new")})
			m, err := claudeplugin.ParseMarketplace([]byte(`{"name":"m","plugins":[{"name":"a","source":"./plugins/a"}]}`))
			Expect(err).NotTo(HaveOccurred())

			got, err := f.FetchPlugin(ctx, fetch.MarketplaceSource{Kind: fetch.KindGit, Location: r.url(), SHA: snap}, m, &m.Plugins[0], "")
			Expect(err).NotTo(HaveOccurred())
			defer got.Done()
			Expect(got.SHA).To(Equal(snap))
			Expect(read(got.Dir, "skills/s/SKILL.md")).To(Equal("old"))
		})

		It("resolves a bare relative name under metadata.pluginRoot", func() {
			r := newRepo()
			snap := r.commit(map[string][]byte{"plugins/a/f": []byte("x")})
			m, err := claudeplugin.ParseMarketplace([]byte(`{"name":"m","metadata":{"pluginRoot":"./plugins"},"plugins":[{"name":"a","source":"a"}]}`))
			Expect(err).NotTo(HaveOccurred())
			got, err := f.FetchPlugin(ctx, fetch.MarketplaceSource{Kind: fetch.KindGit, Location: r.url(), SHA: snap}, m, &m.Plugins[0], "")
			Expect(err).NotTo(HaveOccurred())
			defer got.Done()
			Expect(read(got.Dir, "f")).To(Equal("x"))
		})

		It("refuses a relative source in a url marketplace", func() {
			m, err := claudeplugin.ParseMarketplace([]byte(`{"name":"m","plugins":[{"name":"a","source":"./a"}]}`))
			Expect(err).NotTo(HaveOccurred())
			_, err = f.FetchPlugin(ctx, fetch.MarketplaceSource{Kind: fetch.KindURL, Location: "https://example.test/m.json"}, m, &m.Plugins[0], "")
			Expect(err).To(MatchError(ContainSubstring("relative sources")))
		})

		It("refuses unsupported source kinds", func() {
			e := &claudeplugin.Entry{Name: "n", Source: claudeplugin.Source{Kind: claudeplugin.SourceNPM, Package: "x"}}
			_, err := f.FetchPlugin(ctx, fetch.MarketplaceSource{}, nil, e, "")
			Expect(errors.Is(err, claudeplugin.ErrUnsupportedSource)).To(BeTrue())
		})

		It("runs no hook, filter or configuration from the environment or the repository", func() {
			r := newRepo()
			marker := filepath.Join(GinkgoT().TempDir(), "ran")
			hooks := GinkgoT().TempDir()
			Expect(os.WriteFile(filepath.Join(hooks, "post-checkout"), []byte("#!/bin/sh\ntouch "+marker+"\n"), 0o755)).To(Succeed())
			sha := r.commit(map[string][]byte{
				".gitattributes": []byte("* filter=evil\n"),
				"f":              []byte("data"),
			})
			// The server's environment names a hook folder and a filter
			// driver; a plain git honours both.
			env := []string{
				"GIT_CONFIG_COUNT=2",
				"GIT_CONFIG_KEY_0=core.hooksPath", "GIT_CONFIG_VALUE_0=" + hooks,
				"GIT_CONFIG_KEY_1=filter.evil.smudge", "GIT_CONFIG_VALUE_1=touch " + marker + "; cat",
			}
			control := GinkgoT().TempDir()
			gitIn(control, env, "clone", "-q", r.url(), "c")
			Expect(marker).To(BeAnExistingFile(), "the fixture must run with a plain git")
			Expect(os.Remove(marker)).To(Succeed())

			for _, kv := range env {
				k, v, _ := strings.Cut(kv, "=")
				GinkgoT().Setenv(k, v)
			}
			got, err := f.FetchPlugin(ctx, fetch.MarketplaceSource{}, nil, urlEntry("p", r.url()), sha)
			Expect(err).NotTo(HaveOccurred())
			defer got.Done()
			Expect(read(got.Dir, "f")).To(Equal("data"))
			Expect(marker).NotTo(BeAnExistingFile())
		})

		It("fails a repository over the size cap and leaves no scratch folder", func() {
			small, err := fetch.New(fetch.Options{ScratchRoot: root, AllowFileURLs: true, MaxScratchBytes: 256 << 10})
			Expect(err).NotTo(HaveOccurred())
			r := newRepo()
			sha := r.commit(map[string][]byte{"big.bin": randomBytes(2 << 20)})
			_, err = small.FetchPlugin(ctx, fetch.MarketplaceSource{}, nil, urlEntry("p", r.url()), sha)
			Expect(errors.Is(err, claudeplugin.ErrTooLarge)).To(BeTrue(), "%v", err)
			Expect(scratchEntries(root)).To(BeEmpty())
		})

		It("stops a git process at the size cap at once, not at the next poll of the folder", func() {
			if runtime.GOOS != "linux" {
				Skip("the hard file size limit is Linux only")
			}
			// A git that writes one huge file as fast as it can, outside the
			// scratch folder, so the test can measure what it wrote.
			bin := GinkgoT().TempDir()
			out := filepath.Join(GinkgoT().TempDir(), "pack")
			script := "#!/bin/sh\nexec head -c 100000000 /dev/zero > " + out + "\n"
			Expect(os.WriteFile(filepath.Join(bin, "git"), []byte(script), 0o755)).To(Succeed())
			GinkgoT().Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
			const limit = 256 << 10
			small, err := fetch.New(fetch.Options{ScratchRoot: root, MaxScratchBytes: limit})
			Expect(err).NotTo(HaveOccurred())
			_, err = small.ResolveRef(ctx, "https://example.test/a.git", "")
			Expect(errors.Is(err, claudeplugin.ErrTooLarge)).To(BeTrue(), "%v", err)
			info, err := os.Stat(out)
			Expect(err).NotTo(HaveOccurred())
			Expect(info.Size()).To(BeNumerically("<=", limit))
		})

		It("fails a plugin over the file limits of the copy", func() {
			lim := fetch.Options{ScratchRoot: root, AllowFileURLs: true}
			lim.Limits.MaxFiles, lim.Limits.MaxTotalBytes = 2, 1<<20
			few, err := fetch.New(lim)
			Expect(err).NotTo(HaveOccurred())
			r := newRepo()
			sha := r.commit(map[string][]byte{"a": []byte("a"), "b": []byte("b"), "c": []byte("c")})
			_, err = few.FetchPlugin(ctx, fetch.MarketplaceSource{}, nil, urlEntry("p", r.url()), sha)
			Expect(errors.Is(err, claudeplugin.ErrTooLarge)).To(BeTrue(), "%v", err)
			Expect(scratchEntries(root)).To(BeEmpty())
		})

		DescribeTable("refuses locations that are not https",
			func(u string) {
				strict, err := fetch.New(fetch.Options{ScratchRoot: root})
				Expect(err).NotTo(HaveOccurred())
				_, err = strict.FetchPlugin(ctx, fetch.MarketplaceSource{}, nil, urlEntry("p", u), "")
				Expect(errors.Is(err, fetch.ErrRefusedURL)).To(BeTrue(), "%v", err)
				Expect(err.Error()).NotTo(ContainSubstring("secret"))
				Expect(scratchEntries(root)).To(BeEmpty())
			},
			Entry("ssh", "ssh://git@example.test/a.git"),
			Entry("scp-like", "git@example.test:a.git"),
			Entry("http", "http://example.test/a.git"),
			Entry("file without the test option", "file:///srv/a.git"),
			Entry("a local path", "/srv/a.git"),
			Entry("credentials in the URL", "https://user:secret@example.test/a.git"),
		)

		Describe("private addresses", func() {
			refusing := func(on bool, answer string) *fetch.Fetcher {
				f, err := fetch.New(fetch.Options{ScratchRoot: root, RefusePrivate: func() bool { return on }})
				Expect(err).NotTo(HaveOccurred())
				f.SetLookup(func(context.Context, string) ([]net.IPAddr, error) {
					return []net.IPAddr{{IP: net.ParseIP(answer)}}, nil
				})
				return f
			}
			const sha = "0123456789abcdef0123456789abcdef01234567"

			DescribeTable("refuses a git location on a private network",
				func(u, answer string) {
					_, err := refusing(true, answer).FetchPlugin(ctx, fetch.MarketplaceSource{}, nil, urlEntry("p", u), sha)
					Expect(errors.Is(err, fetch.ErrPrivateURL)).To(BeTrue(), "%v", err)
					Expect(errors.Is(err, fetch.ErrRefusedURL)).To(BeTrue(), "%v", err)
					Expect(scratchEntries(root)).To(BeEmpty())
					_, err = refusing(true, answer).ResolveRef(ctx, u, "")
					Expect(errors.Is(err, fetch.ErrPrivateURL)).To(BeTrue(), "%v", err)
				},
				Entry("a private literal address", "https://10.0.0.5/a.git", "10.0.0.5"),
				Entry("localhost", "https://localhost/a.git", "127.0.0.1"),
				Entry("a name that resolves to a private address", "https://git.example.test/a.git", "192.168.1.10"),
			)

			It("refuses a marketplace.json URL on a private network", func() {
				_, _, err := refusing(true, "10.1.2.3").FetchMarketplace(ctx, fetch.KindURL, "https://intranet.example.test/marketplace.json", "")
				Expect(errors.Is(err, fetch.ErrPrivateURL)).To(BeTrue(), "%v", err)
			})

			It("lets a private address through when the administrator allows it", func() {
				srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {}))
				DeferCleanup(srv.Close)
				_, _, err := refusing(true, "127.0.0.1").FetchMarketplace(ctx, fetch.KindURL, srv.URL+"/marketplace.json", "")
				Expect(errors.Is(err, fetch.ErrPrivateURL)).To(BeTrue(), "%v", err)
				// Allowed: the request goes out (and fails on the test certificate).
				_, _, err = refusing(false, "127.0.0.1").FetchMarketplace(ctx, fetch.KindURL, srv.URL+"/marketplace.json", "")
				Expect(err).To(HaveOccurred())
				Expect(errors.Is(err, fetch.ErrPrivateURL)).To(BeFalse(), "%v", err)
			})
		})

		It("refuses a pin that is not a full SHA", func() {
			_, err := f.FetchPlugin(ctx, fetch.MarketplaceSource{}, nil, urlEntry("p", "https://example.test/a.git"), "abc123")
			Expect(err).To(MatchError(ContainSubstring("full commit SHA")))
		})
	})

	Describe("ResolveRef", func() {
		It("resolves a moved ref to its new SHA", func() {
			r := newRepo()
			first := r.commit(map[string][]byte{"f": []byte("1")})
			Expect(f.ResolveRef(ctx, r.url(), "main")).To(Equal(first))
			Expect(f.ResolveRef(ctx, r.url(), "")).To(Equal(first))
			second := r.commit(map[string][]byte{"f": []byte("2")})
			Expect(f.ResolveRef(ctx, r.url(), "main")).To(Equal(second))
		})

		It("resolves an annotated tag to its commit", func() {
			r := newRepo()
			sha := r.commit(map[string][]byte{"f": []byte("1")})
			gitIn(r.dir, nil, "-c", "user.email=t@example.test", "-c", "user.name=t", "tag", "-a", "v1", "-m", "v1")
			Expect(f.ResolveRef(ctx, r.url(), "v1")).To(Equal(sha))
		})

		It("refuses ssh and http", func() {
			for _, u := range []string{"ssh://example.test/a.git", "http://example.test/a.git"} {
				_, err := f.ResolveRef(ctx, u, "main")
				Expect(errors.Is(err, fetch.ErrRefusedURL)).To(BeTrue())
			}
		})
	})

	Describe("FetchMarketplace", func() {
		It("reads marketplace.json of a git marketplace at a ref, with no other blob", func() {
			r := newRepo()
			sha := r.commit(map[string][]byte{
				".claude-plugin/marketplace.json": []byte(`{"name":"m","plugins":[]}`),
				"plugins/a/big.bin":               randomBytes(64 << 10),
			})
			inside := r.blobsUnder(sha, ".claude-plugin")
			var blobs []string
			f.SetWrapGit(func(g claudeplugin.GitFunc) claudeplugin.GitFunc {
				return func(ctx context.Context, dir string, args ...string) (string, error) {
					out, err := g(ctx, dir, args...)
					if err == nil && args[0] == "rev-parse" {
						cmd := exec.Command("git", "cat-file", "--batch-all-objects", "--batch-check=%(objecttype) %(objectname)")
						cmd.Dir = dir
						cmd.Env = []string{"PATH=" + os.Getenv("PATH"), "GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL=/dev/null", "HOME=" + dir}
						b, _ := cmd.Output()
						for _, l := range strings.Split(strings.TrimSpace(string(b)), "\n") {
							if typ, id, ok := strings.Cut(l, " "); ok && typ == "blob" {
								blobs = append(blobs, id)
							}
						}
					}
					return out, err
				}
			})
			body, got, err := f.FetchMarketplace(ctx, fetch.KindGit, r.url(), "main")
			Expect(err).NotTo(HaveOccurred())
			Expect(got).To(Equal(sha))
			Expect(string(body)).To(Equal(`{"name":"m","plugins":[]}`))
			for _, id := range blobs {
				Expect(inside).To(HaveKey(id))
			}
			Expect(scratchEntries(root)).To(BeEmpty())
		})

		It("fails when the repository has no marketplace.json", func() {
			r := newRepo()
			r.commit(map[string][]byte{"f": []byte("1")})
			_, _, err := f.FetchMarketplace(ctx, fetch.KindGit, r.url(), "")
			Expect(err).To(MatchError(ContainSubstring("marketplace.json")))
			Expect(scratchEntries(root)).To(BeEmpty())
		})

		It("refuses ssh, http and a bad owner/repo", func() {
			strict, err := fetch.New(fetch.Options{ScratchRoot: root})
			Expect(err).NotTo(HaveOccurred())
			for _, c := range [][2]string{
				{fetch.KindGit, "ssh://example.test/a.git"}, {fetch.KindGit, "http://example.test/a.git"},
				{fetch.KindGit, "git@example.test:a.git"}, {fetch.KindGitHub, "-o/x"}, {fetch.KindGitHub, "a/b/c"},
				{fetch.KindURL, "http://example.test/m.json"},
			} {
				_, _, err := strict.FetchMarketplace(ctx, c[0], c[1], "")
				Expect(errors.Is(err, fetch.ErrRefusedURL)).To(BeTrue(), "%s %s: %v", c[0], c[1], err)
			}
		})

		Context("over https", func() {
			var srv *httptest.Server
			BeforeEach(func() {
				mux := http.NewServeMux()
				mux.HandleFunc("/m.json", func(w http.ResponseWriter, _ *http.Request) {
					_, _ = w.Write([]byte(`{"name":"m","plugins":[]}`))
				})
				mux.HandleFunc("/to-http", func(w http.ResponseWriter, r *http.Request) {
					http.Redirect(w, r, "http://"+r.Host+"/m.json", http.StatusFound)
				})
				mux.HandleFunc("/to-https", func(w http.ResponseWriter, r *http.Request) {
					http.Redirect(w, r, "/m.json", http.StatusFound)
				})
				mux.HandleFunc("/big", func(w http.ResponseWriter, _ *http.Request) {
					_, _ = w.Write(make([]byte, fetch.MaxMarketplaceBytes+1))
				})
				mux.HandleFunc("/missing", http.NotFound)
				srv = httptest.NewTLSServer(mux)
				DeferCleanup(srv.Close)
				var err error
				f, err = fetch.New(fetch.Options{ScratchRoot: root, HTTPClient: srv.Client()})
				Expect(err).NotTo(HaveOccurred())
			})

			It("downloads a JSON URL with no commit", func() {
				body, sha, err := f.FetchMarketplace(ctx, fetch.KindURL, srv.URL+"/m.json", "")
				Expect(err).NotTo(HaveOccurred())
				Expect(sha).To(BeEmpty())
				Expect(string(body)).To(ContainSubstring(`"name":"m"`))
			})

			It("follows a redirect that stays on https", func() {
				_, _, err := f.FetchMarketplace(ctx, fetch.KindURL, srv.URL+"/to-https", "")
				Expect(err).NotTo(HaveOccurred())
			})

			It("refuses a redirect to http", func() {
				_, _, err := f.FetchMarketplace(ctx, fetch.KindURL, srv.URL+"/to-http", "")
				Expect(errors.Is(err, fetch.ErrRefusedURL)).To(BeTrue(), "%v", err)
			})

			It("refuses a body over 1 MiB and a missing file", func() {
				_, _, err := f.FetchMarketplace(ctx, fetch.KindURL, srv.URL+"/big", "")
				Expect(err).To(MatchError(ContainSubstring("over")))
				_, _, err = f.FetchMarketplace(ctx, fetch.KindURL, srv.URL+"/missing", "")
				Expect(err).To(MatchError(ContainSubstring("404")))
			})
		})
	})

	It("removes leftover scratch folders at startup", func() {
		Expect(os.MkdirAll(filepath.Join(root, "plugin-dead", "x"), 0o755)).To(Succeed())
		Expect(f.CleanScratch()).To(Succeed())
		Expect(scratchEntries(root)).To(BeEmpty())
	})
})
