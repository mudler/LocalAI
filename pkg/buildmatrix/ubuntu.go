// Package buildmatrix holds invariants for the container build matrices that
// drive the image and backend CI workflows. Those matrices carry the Ubuntu
// release twice: once in the base image reference and once in the
// `ubuntu-version`/`ubuntu-codename` fields used to pin apt repositories. When
// the two drift apart the resulting image mixes glibc versions, which only
// surfaces at runtime as backends failing to dlopen their bundled libraries.
package buildmatrix

import (
	"regexp"
	"strings"
)

// ubuntuReleaseRe extracts the Ubuntu release from a base image reference, e.g.
// "ubuntu:24.04", "rocm/dev-ubuntu-24.04:7.2.1" or
// "nvidia/cuda:13.0.0-devel-ubuntu24.04". Images that do not name a release
// (JetPack, for instance) simply do not match.
var ubuntuReleaseRe = regexp.MustCompile(`ubuntu[:\-]?(\d{2})\.?(\d{2})`)

// ubuntuCodenames maps the compact release used by the NVIDIA apt repositories
// (and by the `ubuntu-version` matrix field) to the Ubuntu codename.
var ubuntuCodenames = map[string]string{
	"2204": "jammy",
	"2404": "noble",
}

// UbuntuReleaseFromBaseImage reports the Ubuntu release a base image is built
// on, in the compact form used by the build matrices ("2404"), along with its
// codename. ok is false when the image reference does not name a release.
func UbuntuReleaseFromBaseImage(image string) (version string, codename string, ok bool) {
	m := ubuntuReleaseRe.FindStringSubmatch(strings.ToLower(image))
	if m == nil {
		return "", "", false
	}
	version = m[1] + m[2]
	return version, ubuntuCodenames[version], true
}
