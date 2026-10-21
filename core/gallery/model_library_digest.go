// SPDX-License-Identifier: MIT
package gallery

import (
	"crypto/sha256"
	"encoding/hex"
)

// ModelLibraryDigests returns short digests of a library entry and of the
// gallery entry generated from it. They identify the content for the golden
// file: when the library digest of an entry has not changed, its generated
// gallery digest must not change either.
func ModelLibraryDigests(e ModelLibraryEntry) (libraryDigest, galleryDigest string, err error) {
	m, err := e.ToGallery()
	if err != nil {
		return "", "", err
	}
	g, err := galleryEntryDigest(stripRuntime(m))
	if err != nil {
		return "", "", err
	}
	raw, err := EncodeModelIndex([]ModelLibraryEntry{e})
	if err != nil {
		return "", "", err
	}
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])[:16], g[:16], nil
}
