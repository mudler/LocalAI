package p2ppair

import (
	"crypto/sha256"
	"strings"

	"github.com/libp2p/go-libp2p/core/peer"
)

// PhraseWords is the number of words in a confirmation phrase.
const PhraseWords = 4

// Phrase returns the confirmation phrase of a pairing: four words taken from
// the SHA-256 of the server peer ID, the device peer ID and the invite ID.
// Both sides compute it and show it, and the people compare. The inputs are
// length-prefixed so that two different triples cannot give the same digest
// input. The phrase has 40 bits, which is enough for a check that runs once,
// live, between two people who can see each other's screens.
func Phrase(serverPeer, devicePeer peer.ID, inviteID string) string {
	h := sha256.New()
	for _, part := range []string{serverPeer.String(), devicePeer.String(), inviteID} {
		var n [4]byte
		n[0], n[1], n[2], n[3] = byte(len(part)>>24), byte(len(part)>>16), byte(len(part)>>8), byte(len(part)) // #nosec G115 -- the parts are peer IDs and an invite ID, far below 4 GiB, and the shifts keep one byte each
		h.Write(n[:])
		h.Write([]byte(part))
	}
	sum := h.Sum(nil)
	// Ten bits per word from the first five bytes.
	var acc uint64
	for _, b := range sum[:5] {
		acc = acc<<8 | uint64(b)
	}
	words := make([]string, PhraseWords)
	for i := range words {
		shift := uint(10 * (PhraseWords - 1 - i))
		words[i] = wordList[(acc>>shift)&0x3ff]
	}
	return strings.Join(words, " ")
}
