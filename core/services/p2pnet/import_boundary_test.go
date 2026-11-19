package p2pnet_test

import (
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

const module = "github.com/mudler/LocalAI"

// p2pnetImports are the imports outside the standard library that p2pnet
// may use. An entry ending in "/..." allows every path below it.
var p2pnetImports = []string{
	"github.com/libp2p/...",
	"github.com/multiformats/...",
	"github.com/mudler/xlog",
	module + "/pkg/httpclient",
	// The identity key is written with the atomic file write of the project.
	module + "/pkg/atomicfile",
}

// libp2pImporters are the packages that may import libp2p. Everything else
// uses p2pnet.
var libp2pImporters = []string{
	module + "/core/services/p2pnet/...",
	module + "/core/services/p2prelay/...",
	// The relay manager connects to relays and runs a throwaway peer for
	// the circuit test.
	module + "/core/services/p2prelaymgr/...",
}

// p2ppairPath is the pairing service. It parses peer IDs and multiaddrs in
// invite links and opens no connection, so it may use only the libp2p and
// multiaddr packages in p2ppairP2PImports.
const p2ppairPath = module + "/core/services/p2ppair"

var p2ppairP2PImports = []string{
	"github.com/libp2p/go-libp2p/core/peer",
	"github.com/multiformats/go-multiaddr",
}

// standaloneLibp2pModules are modules of the libp2p organisation that are
// not part of the libp2p stack and open no peer connection. Any package may
// import them. go-yamux is the stream multiplexer that the cluster tunnel runs
// over a websocket.
var standaloneLibp2pModules = []string{
	"github.com/libp2p/go-yamux/...",
}

func isP2PImport(p string) bool {
	return strings.HasPrefix(p, "github.com/libp2p/") || strings.HasPrefix(p, "github.com/multiformats/")
}

func matches(p string, list []string) bool {
	for _, a := range list {
		if base, ok := strings.CutSuffix(a, "/..."); ok {
			if p == base || strings.HasPrefix(p, base+"/") {
				return true
			}
		} else if p == a {
			return true
		}
	}
	return false
}

func isStdlib(p string) bool {
	first, _, _ := strings.Cut(p, "/")
	return !strings.Contains(first, ".")
}

// moduleRoot finds the directory of go.mod above the working directory.
func moduleRoot() string {
	GinkgoHelper()
	dir, err := os.Getwd()
	Expect(err).ToNot(HaveOccurred())
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			Skip("the test does not run inside the module")
		}
		dir = parent
	}
}

// moduleImports returns the imports of the non-test Go files of each
// package below dir, whatever their build tags. Reading the files is much
// faster than go list on a module of this size.
func moduleImports(root, dir string) map[string][]string {
	GinkgoHelper()
	pkgs := map[string][]string{}
	fset := token.NewFileSet()
	err := filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		name := d.Name()
		if d.IsDir() {
			if path != dir && (strings.HasPrefix(name, ".") || strings.HasPrefix(name, "_") || name == "testdata" || name == "node_modules" || name == "vendor") {
				return filepath.SkipDir
			}
			if _, err := os.Stat(filepath.Join(path, "go.mod")); err == nil && path != root {
				return filepath.SkipDir // another module
			}
			return nil
		}
		if !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			return nil
		}
		f, err := parser.ParseFile(fset, path, nil, parser.ImportsOnly)
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(root, filepath.Dir(path))
		if err != nil {
			return err
		}
		pkg := module
		if rel != "." {
			pkg += "/" + filepath.ToSlash(rel)
		}
		for _, imp := range f.Imports {
			p, err := strconv.Unquote(imp.Path.Value)
			if err != nil {
				return err
			}
			pkgs[pkg] = append(pkgs[pkg], p)
		}
		return nil
	})
	Expect(err).ToNot(HaveOccurred())
	Expect(pkgs).ToNot(BeEmpty())
	return pkgs
}

var _ = Describe("Import boundary", func() {
	It("p2pnet imports only the standard library, libp2p, multiaddr, logging and the HTTP client", func() {
		root := moduleRoot()
		for pkg, imports := range moduleImports(root, filepath.Join(root, "core", "services", "p2pnet")) {
			for _, imp := range imports {
				if isStdlib(imp) {
					continue
				}
				Expect(matches(imp, p2pnetImports)).To(BeTrue(), "%s imports %s, which is not allowed", pkg, imp)
			}
		}
	})

	It("only the P2P packages import libp2p", func() {
		root := moduleRoot()
		for pkg, imports := range moduleImports(root, root) {
			if matches(pkg, libp2pImporters) {
				continue
			}
			for _, imp := range imports {
				if pkg == p2ppairPath && matches(imp, p2ppairP2PImports) {
					continue
				}
				if matches(imp, standaloneLibp2pModules) {
					continue
				}
				Expect(strings.HasPrefix(imp, "github.com/libp2p/")).To(BeFalse(), "%s imports %s; use core/services/p2pnet", pkg, imp)
			}
		}
	})

	It("p2ppair imports only the peer ID and multiaddr packages", func() {
		root := moduleRoot()
		pkgs := moduleImports(root, filepath.Join(root, "core", "services", "p2ppair"))
		Expect(pkgs).To(HaveKey(p2ppairPath))
		for pkg, imports := range pkgs {
			for _, imp := range imports {
				if !isP2PImport(imp) {
					continue
				}
				Expect(matches(imp, p2ppairP2PImports)).To(BeTrue(), "%s imports %s; only %v are allowed", pkg, imp, p2ppairP2PImports)
			}
		}
	})
})
