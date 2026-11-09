// SPDX-License-Identifier: MIT

// Package profiles holds the sandbox profiles of the Sandbox add-on: the
// environments a chat can run commands in. A profile is a name, a
// description, the limits of its sandboxes and the tools it has. The table is
// the one source of the manifest (go test with UPDATE_MANIFEST=1 writes it
// there), of the note the model reads about the tools and of the build check
// of the guest images (check-tools.sh reads the manifest). The image of each
// profile is not in the table: a release sets it by digest at build time.
package profiles

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/mudler/LocalAI/pkg/addons/contract"
)

const (
	// Base is the fallback profile: the image the add-on always had.
	Base = contract.FallbackProfile
	// Media is Base with the tools to cut and convert audio, video and images.
	Media = "media"
	// Video is Media with a Rust toolchain and the fframes video renderer.
	Video = "video"
)

func tool(name, detail string, check ...string) contract.ProfileTool {
	return contract.ProfileTool{Name: name, Detail: detail, Check: check}
}

// baseTools is what every profile has. The guest user cannot install
// packages, so a program that is not listed is not there.
func baseTools() []contract.ProfileTool {
	return []contract.ProfileTool{
		tool("python3", "pandas, matplotlib, openpyxl, docx, pptx, pypdf, Pillow", "python3 --version"),
		tool("pip", "", "pip --version"),
		tool("jq", "", "jq --version"),
		tool("ripgrep", "rg", "rg --version"),
		tool("sqlite3", "", "sqlite3 --version"),
		tool("zip", "", "zip -v"),
		tool("unzip", "", "unzip -v"),
		tool("file", "", "file --version"),
		tool("tree", "", "tree --version"),
		tool("poppler", "pdftotext, pdftoppm", "pdftotext -v", "pdftoppm -v"),
		tool("git", "", "git --version"),
		tool("curl", "", "curl --version"),
	}
}

func mediaTools() []contract.ProfileTool {
	return append(baseTools(),
		tool("ffmpeg", "with ffprobe", "ffmpeg -version", "ffprobe -version"),
		tool("sox", "", "sox --version"),
		tool("ImageMagick", "magick, convert", "magick -version", "convert -version"),
	)
}

func videoTools() []contract.ProfileTool {
	return append(mediaTools(),
		tool("Rust", "rustc and cargo, 1.88 or newer", "rustc --version", "cargo --version"),
		tool("fframes", "cargo fframes, renders video from code", "cargo-fframes --help"),
	)
}

// Declared returns the profiles of this release, in the order the pickers
// show them. The first is the fallback.
func Declared() []contract.ExecutionProfile {
	return []contract.ExecutionProfile{
		{ID: Base, Title: "Base", Description: "Python, documents, PDF and the usual command-line tools.",
			CPUs: 2, MemoryMiB: 2048, DiskMiB: 10240, Tools: baseTools()},
		{ID: Media, Title: "Media", Description: "Base plus ffmpeg, sox and ImageMagick to cut and convert audio, video and images.",
			CPUs: 2, MemoryMiB: 4096, DiskMiB: 20480, Tools: mediaTools()},
		{ID: Video, Title: "Video", Description: "Media plus a Rust toolchain and the fframes renderer to make videos from code.",
			CPUs: 4, MemoryMiB: 8192, DiskMiB: 30720, Tools: videoTools()},
	}
}

var imageShape = regexp.MustCompile(`^[A-Za-z0-9._/:-]+@sha256:[0-9a-f]{64}$`)

// ValidImage says whether ref is a registry reference with a digest.
func ValidImage(ref string) bool { return imageShape.MatchString(ref) }

// Images maps a profile ID to the OCI reference, by digest, of its image.
type Images map[string]string

// ParseImages reads the build-time list "media=ref,video=ref". The base image
// has its own variable and is added by the caller.
func ParseImages(list string) (Images, error) {
	out := Images{}
	for _, item := range strings.Split(list, ",") {
		item = strings.TrimSpace(item)
		if item == "" {
			continue
		}
		id, ref, ok := strings.Cut(item, "=")
		if !ok || !contract.ValidProfileID(id) || !ValidImage(ref) {
			return nil, fmt.Errorf("profiles: %q is not <profile>=<repository>@sha256:<digest>", item)
		}
		if _, dup := out[id]; dup {
			return nil, fmt.Errorf("profiles: profile %q has two images", id)
		}
		out[id] = ref
	}
	return out, nil
}
