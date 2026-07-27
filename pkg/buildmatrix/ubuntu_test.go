package buildmatrix_test

import (
	"fmt"
	"os"
	"path/filepath"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"gopkg.in/yaml.v3"

	"github.com/mudler/LocalAI/pkg/buildmatrix"
)

// matrixEntry is a single build matrix entry that pins both a base image and
// the Ubuntu release its apt repositories are configured for.
type matrixEntry struct {
	file           string
	line           int
	baseImage      string
	ubuntuVersion  string
	ubuntuCodename string
}

func (e matrixEntry) String() string {
	return fmt.Sprintf("%s:%d (base-image: %s)", e.file, e.line, e.baseImage)
}

// collectMatrixEntries walks a YAML document and picks up every mapping that
// carries both `base-image` and `ubuntu-version`, wherever it is nested. The
// matrices live in different shapes across the workflows (job matrices, reusable
// workflow inputs, the flat backend matrix), so shape-agnostic walking keeps the
// invariant applying to all of them.
func collectMatrixEntries(node *yaml.Node, file string, out *[]matrixEntry) {
	if node.Kind == yaml.MappingNode {
		e := matrixEntry{file: file, line: node.Line}
		for i := 0; i+1 < len(node.Content); i += 2 {
			key, value := node.Content[i], node.Content[i+1]
			if value.Kind != yaml.ScalarNode {
				continue
			}
			switch key.Value {
			case "base-image":
				e.baseImage = value.Value
				e.line = value.Line
			case "ubuntu-version":
				e.ubuntuVersion = value.Value
			case "ubuntu-codename":
				e.ubuntuCodename = value.Value
			}
		}
		if e.baseImage != "" && e.ubuntuVersion != "" {
			*out = append(*out, e)
		}
	}

	for _, child := range node.Content {
		collectMatrixEntries(child, file, out)
	}
}

func loadMatrixEntries() []matrixEntry {
	GinkgoHelper()

	files, err := filepath.Glob(filepath.Join("..", "..", ".github", "workflows", "*.yml"))
	Expect(err).ToNot(HaveOccurred())
	files = append(files, filepath.Join("..", "..", ".github", "backend-matrix.yml"))

	entries := []matrixEntry{}
	for _, file := range files {
		raw, err := os.ReadFile(file)
		Expect(err).ToNot(HaveOccurred(), "reading %s", file)

		var doc yaml.Node
		Expect(yaml.Unmarshal(raw, &doc)).To(Succeed(), "parsing %s", file)

		collectMatrixEntries(&doc, file, &entries)
	}

	return entries
}

var _ = Describe("UbuntuReleaseFromBaseImage", func() {
	It("reads the release out of the base image reference", func() {
		version, codename, ok := buildmatrix.UbuntuReleaseFromBaseImage("ubuntu:24.04")
		Expect(ok).To(BeTrue())
		Expect(version).To(Equal("2404"))
		Expect(codename).To(Equal("noble"))

		version, codename, ok = buildmatrix.UbuntuReleaseFromBaseImage("nvidia/cuda:13.0.0-devel-ubuntu22.04")
		Expect(ok).To(BeTrue())
		Expect(version).To(Equal("2204"))
		Expect(codename).To(Equal("jammy"))

		version, codename, ok = buildmatrix.UbuntuReleaseFromBaseImage("rocm/dev-ubuntu-24.04:7.2.1")
		Expect(ok).To(BeTrue())
		Expect(version).To(Equal("2404"))
		Expect(codename).To(Equal("noble"))
	})

	It("reports images that do not name an Ubuntu release", func() {
		_, _, ok := buildmatrix.UbuntuReleaseFromBaseImage("nvcr.io/nvidia/l4t-jetpack:r36.4.0")
		Expect(ok).To(BeFalse())
	})
})

var _ = Describe("CI build matrices", func() {
	var entries []matrixEntry

	BeforeEach(func() {
		entries = loadMatrixEntries()
	})

	// A base image on one Ubuntu release with apt repositories pinned to another
	// produces an image whose glibc does not match the one the backends were
	// built against: the backends then fail to dlopen their bundled libraries
	// (see https://github.com/mudler/LocalAI/issues/11059).
	It("pins every base image to the Ubuntu release its entry targets", func() {
		Expect(entries).ToNot(BeEmpty())

		for _, entry := range entries {
			version, codename, ok := buildmatrix.UbuntuReleaseFromBaseImage(entry.baseImage)
			if !ok {
				continue
			}

			Expect(entry.ubuntuVersion).To(Equal(version),
				"%s is Ubuntu %s but the entry sets ubuntu-version: %q", entry, version, entry.ubuntuVersion)

			if codename != "" && entry.ubuntuCodename != "" {
				Expect(entry.ubuntuCodename).To(Equal(codename),
					"%s is Ubuntu %s but the entry sets ubuntu-codename: %q", entry, version, entry.ubuntuCodename)
			}
		}
	})

	// Backends are all built on the same release as the runtime images they are
	// unpacked into. Keeping the GPU runtime images aligned with the CPU one is
	// what guarantees that.
	It("builds every Ubuntu-based runtime image on the same release", func() {
		releases := map[string][]string{}

		for _, entry := range entries {
			if filepath.Base(entry.file) != "image.yml" && filepath.Base(entry.file) != "image-pr.yml" {
				continue
			}
			version, _, ok := buildmatrix.UbuntuReleaseFromBaseImage(entry.baseImage)
			if !ok {
				continue
			}
			releases[version] = append(releases[version], entry.String())
		}

		Expect(releases).To(HaveLen(1), "runtime images are split across Ubuntu releases: %v", releases)
	})
})
