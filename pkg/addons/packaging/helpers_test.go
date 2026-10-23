// SPDX-License-Identifier: MIT
package packaging

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/google/go-containerregistry/pkg/registry"
	v1 "github.com/google/go-containerregistry/pkg/v1"
	"github.com/google/go-containerregistry/pkg/v1/types"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func TestPackaging(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Add-on packaging")
}

// pngBytes is the smallest valid signature of a PNG file; the checks read the
// signature, not the pixels.
var pngBytes = []byte("\x89PNG\r\n\x1a\n" + strings.Repeat("x", 32))

// project is a hermetic repository root: the real Tables manifest, a small
// fake UI build and a platform matrix file.
type project struct {
	root   string
	config string
}

func newProject(enabled map[string][]string) *project {
	GinkgoHelper()
	return newProjectFor("tables", enabled)
}

// newProjectFor is newProject for the first-party add-on id: its real manifest
// and a fake UI build named after it.
func newProjectFor(id string, enabled map[string][]string) *project {
	GinkgoHelper()
	root := GinkgoT().TempDir()
	manifest, err := os.ReadFile("../../../addons/" + id + "/manifest.json")
	Expect(err).NotTo(HaveOccurred())
	write := func(rel string, b []byte) {
		p := filepath.Join(root, rel)
		Expect(os.MkdirAll(filepath.Dir(p), 0o755)).To(Succeed())
		Expect(os.WriteFile(p, b, 0o644)).To(Succeed())
	}
	write("addons/"+id+"/manifest.json", manifest)
	write("dist/ui/"+id+".js", []byte("export default function mount() {}\n"))
	write("dist/ui/"+id+".css", []byte(".x{}\n"))
	write("dist/ui/icon.png", pngBytes)
	var b strings.Builder
	fmt.Fprintf(&b, "schema: 1\naddon: %s\nexecutable: %s\nmanifest: addons/%s/manifest.json\nui: dist\nplatforms:\n", id, id, id)
	for _, p := range []string{"linux/amd64", "linux/arm64", "darwin/arm64"} {
		rt, on := enabled[p]
		fmt.Fprintf(&b, "  - platform: %s\n    enabled: %t\n    runner: runner-%s\n    native_check: executable-start\n", p, on, strings.ReplaceAll(p, "/", "-"))
		if len(rt) > 0 {
			b.WriteString("    runtime:\n")
			for _, r := range rt {
				fmt.Fprintf(&b, "      - %s\n", r)
			}
		}
	}
	write("platforms.yaml", []byte(b.String()))
	return &project{root: root, config: filepath.Join(root, "platforms.yaml")}
}

// run executes an addonpkg subcommand and returns its output and error.
func run(args ...string) (string, error) {
	var out bytes.Buffer
	err := Run(args, &out)
	return out.String(), err
}

func exe(content string) string {
	GinkgoHelper()
	p := filepath.Join(GinkgoT().TempDir(), "tables")
	Expect(os.WriteFile(p, []byte(content), 0o755)).To(Succeed())
	return p
}

func (p *project) build(platform, version, executable string, extra ...string) (string, error) {
	dir := filepath.Join(GinkgoT().TempDir(), strings.ReplaceAll(platform, "/", "-"))
	args := []string{"build", "--config", p.config, "--root", p.root, "--layout", dir, "--version", version, "--platform", platform, "--executable", executable}
	_, err := run(append(args, extra...)...)
	return dir, err
}

func (p *project) mustBuild(platform, version, content string, extra ...string) string {
	GinkgoHelper()
	dir, err := p.build(platform, version, exe(content), extra...)
	Expect(err).NotTo(HaveOccurred())
	return dir
}

func (p *project) verify(dir, platform, version string) error {
	_, err := run("verify", "--config", p.config, "--layout", dir, "--platform", platform, "--version", version)
	return err
}

func expectCode(err error, code string) {
	GinkgoHelper()
	Expect(err).To(HaveOccurred())
	Expect(err.Error()).To(HavePrefix(code+":"), "got %v", err)
}

// tfile is one entry of a rewritten layer.
type tfile struct {
	name     string
	data     []byte
	mode     int64
	typeflag byte
	link     string
}

type layerSpec struct {
	role      string
	mediaType string
	files     []tfile
}

func sha(b []byte) (string, string) {
	s := sha256.Sum256(b)
	return hex.EncodeToString(s[:]), "sha256:" + hex.EncodeToString(s[:])
}

func readSpec(dir string) []layerSpec {
	GinkgoHelper()
	l, _, err := loadLeg(dir)
	Expect(err).NotTo(HaveOccurred())
	var out []layerSpec
	for _, d := range l.manifest.Layers {
		blob, err := readBlob(dir, d.Digest, d.Size)
		Expect(err).NotTo(HaveOccurred())
		gz, err := gzip.NewReader(bytes.NewReader(blob))
		Expect(err).NotTo(HaveOccurred())
		tr := tar.NewReader(gz)
		ls := layerSpec{role: d.Annotations[AnnotationLayer], mediaType: string(d.MediaType)}
		for {
			h, err := tr.Next()
			if err == io.EOF {
				break
			}
			Expect(err).NotTo(HaveOccurred())
			b, _ := io.ReadAll(tr)
			ls.files = append(ls.files, tfile{name: h.Name, data: b, mode: h.Mode, typeflag: h.Typeflag})
		}
		out = append(out, ls)
	}
	return out
}

// rewrite replaces the layers of a layout and keeps manifest, config, index
// and receipt consistent with them, so only the semantic defect is left.
// edit may change the manifest, config and receipt too.
func rewrite(dir string, specs []layerSpec, edit func(*v1.Manifest, *v1.ConfigFile, *Receipt)) {
	GinkgoHelper()
	l, rec, err := loadLeg(dir)
	Expect(err).NotTo(HaveOccurred())
	put := func(b []byte) (v1.Hash, int64) {
		h, full := sha(b)
		Expect(os.WriteFile(filepath.Join(dir, "blobs", "sha256", h), b, 0o644)).To(Succeed())
		hash, err := v1.NewHash(full)
		Expect(err).NotTo(HaveOccurred())
		return hash, int64(len(b))
	}
	m := l.manifest
	m.Layers = nil
	rec.Layers = nil
	var diffIDs []v1.Hash
	for _, s := range specs {
		var tb bytes.Buffer
		tw := tar.NewWriter(&tb)
		for _, f := range s.files {
			h := &tar.Header{Name: f.name, Mode: f.mode, Size: int64(len(f.data)), Typeflag: f.typeflag, Linkname: f.link}
			if f.typeflag == 0 {
				h.Typeflag = tar.TypeReg
			}
			if h.Typeflag != tar.TypeReg {
				h.Size = 0
			}
			Expect(tw.WriteHeader(h)).To(Succeed())
			if h.Typeflag == tar.TypeReg {
				_, err := tw.Write(f.data)
				Expect(err).NotTo(HaveOccurred())
			}
		}
		Expect(tw.Close()).To(Succeed())
		_, full := sha(tb.Bytes())
		diff, _ := v1.NewHash(full)
		diffIDs = append(diffIDs, diff)
		var gb bytes.Buffer
		gw := gzip.NewWriter(&gb)
		_, err := gw.Write(tb.Bytes())
		Expect(err).NotTo(HaveOccurred())
		Expect(gw.Close()).To(Succeed())
		hash, size := put(gb.Bytes())
		d := v1.Descriptor{MediaType: "application/vnd.oci.image.layer.v1.tar+gzip", Digest: hash, Size: size, Annotations: map[string]string{AnnotationLayer: s.role}}
		if s.mediaType != "" {
			d.MediaType = types.MediaType(s.mediaType)
		}
		m.Layers = append(m.Layers, d)
		rec.Layers = append(rec.Layers, ReceiptLayer{Role: s.role, Digest: hash.String(), Size: size, MediaType: string(d.MediaType)})
	}
	cfg := l.config
	cfg.RootFS.DiffIDs = diffIDs
	if edit != nil {
		edit(&m, &cfg, rec)
	}
	cb, err := json.Marshal(cfg)
	Expect(err).NotTo(HaveOccurred())
	m.Config.Digest, m.Config.Size = put(cb)
	mb, err := json.Marshal(m)
	Expect(err).NotTo(HaveOccurred())
	mh, ms := put(mb)
	rec.ManifestDigest, rec.ConfigDigest = mh.String(), m.Config.Digest.String()
	idx, err := readRootIndex(dir)
	Expect(err).NotTo(HaveOccurred())
	idx.Manifests[0].Digest, idx.Manifests[0].Size = mh, ms
	ib, err := json.Marshal(idx)
	Expect(err).NotTo(HaveOccurred())
	Expect(os.WriteFile(filepath.Join(dir, "index.json"), ib, 0o644)).To(Succeed())
	rb, err := json.Marshal(rec)
	Expect(err).NotTo(HaveOccurred())
	Expect(os.WriteFile(filepath.Join(dir, receiptFile), rb, 0o644)).To(Succeed())
}

func find(s []layerSpec, role string) *layerSpec {
	for i := range s {
		if s[i].role == role {
			return &s[i]
		}
	}
	return nil
}

func (l *layerSpec) drop(name string) {
	var keep []tfile
	for _, f := range l.files {
		if f.name != name {
			keep = append(keep, f)
		}
	}
	l.files = keep
}

func (l *layerSpec) get(name string) *tfile {
	for i := range l.files {
		if l.files[i].name == name {
			return &l.files[i]
		}
	}
	return nil
}

// countingRegistry is an in-process registry that counts write requests.
type countingRegistry struct {
	mu     sync.Mutex
	writes int
	server *httptest.Server
	host   string
}

func newRegistry() *countingRegistry {
	GinkgoHelper()
	r := &countingRegistry{}
	handler := registry.New(registry.Logger(log.New(io.Discard, "", 0)))
	r.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		if req.Method == http.MethodPut || req.Method == http.MethodPost || req.Method == http.MethodPatch {
			r.mu.Lock()
			r.writes++
			r.mu.Unlock()
		}
		handler.ServeHTTP(w, req)
	}))
	DeferCleanup(r.server.Close)
	r.host = strings.TrimPrefix(r.server.URL, "http://")
	return r
}

func (r *countingRegistry) writeCount() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.writes
}

// repo returns a repository on this registry.
func (r *countingRegistry) repo(name string) string { return r.host + "/" + name }

// merge builds one leg per enabled platform and joins them.
func (p *project) merged(version, revision string, contents map[string]string) string {
	GinkgoHelper()
	var legs []string
	for _, plat := range []string{"linux/amd64", "linux/arm64"} {
		c := contents[plat]
		if c == "" {
			c = "executable of " + plat
		}
		legs = append(legs, p.mustBuild(plat, version, c, "--revision", revision))
	}
	out := filepath.Join(GinkgoT().TempDir(), "merged")
	args := append([]string{"merge", "--config", p.config, "--out", out, "--version", version, "--revision", revision}, legs...)
	_, err := run(args...)
	Expect(err).NotTo(HaveOccurred())
	return out
}

// git makes a history of n empty commits and returns the git dir and shas.
func gitHistory(n int) (string, []string) {
	GinkgoHelper()
	dir := GinkgoT().TempDir()
	env := append(os.Environ(), "GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_SYSTEM=/dev/null")
	git := func(args ...string) string {
		cmd := exec.Command("git", append([]string{"-C", dir, "-c", "user.name=t", "-c", "user.email=t@example.invalid", "-c", "commit.gpgsign=false"}, args...)...)
		cmd.Env = env
		out, err := cmd.CombinedOutput()
		Expect(err).NotTo(HaveOccurred(), string(out))
		return strings.TrimSpace(string(out))
	}
	git("init", "-q")
	var shas []string
	for i := 0; i < n; i++ {
		git("commit", "-q", "--allow-empty", "-m", fmt.Sprintf("c%d", i))
		shas = append(shas, git("rev-parse", "HEAD"))
	}
	return filepath.Join(dir, ".git"), shas
}
