// SPDX-License-Identifier: MIT
package gallery

import (
	"crypto/sha256"
	"encoding/hex"
)

// ModelLibraryDigests returns short digests of a gallery entry and of the
// library entry made from it. They identify the content for the golden file:
// when the gallery digest of an entry has not changed, its library digest must
// not change either.
func ModelLibraryDigests(m GalleryModel) (galleryDigest, libraryDigest string, err error) {
	g, err := galleryEntryDigest(stripRuntime(m))
	if err != nil {
		return "", "", err
	}
	entry, err := ModelLibraryEntryFromGallery(m)
	if err != nil {
		return "", "", err
	}
	raw, err := EncodeModelIndex([]ModelLibraryEntry{entry})
	if err != nil {
		return "", "", err
	}
	sum := sha256.Sum256(raw)
	return g[:16], hex.EncodeToString(sum[:])[:16], nil
}
