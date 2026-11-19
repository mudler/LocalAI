package p2ppair

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"fmt"
	"strings"
)

const (
	// codeAlphabet has no 0, O, 1, I or L, so a code read aloud or from a
	// small screen is hard to get wrong. It is the alphabet of the client
	// pairing codes. 31 symbols over 10 places is about 49 bits.
	codeAlphabet = "23456789ABCDEFGHJKMNPQRSTUVWXYZ"
	// CodeLen is the length of an invite code.
	CodeLen = 10

	codeHashDomain = "p2p-invite-code:"
)

// newCode returns a random invite code.
func newCode() (string, error) {
	out := make([]byte, 0, CodeLen)
	// Rejection sampling: 248 is the largest multiple of 31 below 256.
	buf := make([]byte, CodeLen*2)
	for len(out) < CodeLen {
		if _, err := rand.Read(buf); err != nil {
			return "", fmt.Errorf("p2ppair: generate code: %w", err)
		}
		for _, v := range buf {
			if v >= 248 {
				continue
			}
			out = append(out, codeAlphabet[int(v)%len(codeAlphabet)])
			if len(out) == CodeLen {
				break
			}
		}
	}
	return string(out), nil
}

// NormalizeCode upper-cases a code and drops separators and spaces.
func NormalizeCode(code string) string {
	var b strings.Builder
	for _, r := range strings.ToUpper(code) {
		if r == '-' || r == ' ' {
			continue
		}
		b.WriteRune(r)
	}
	return b.String()
}

// validCodeFormat reports whether a normalized code has the right length and
// alphabet.
func validCodeFormat(n string) bool {
	if len(n) != CodeLen {
		return false
	}
	for i := range len(n) {
		if !strings.ContainsRune(codeAlphabet, rune(n[i])) {
			return false
		}
	}
	return true
}

// hashCode returns the keyed hash that stores keep for a code. It follows the
// client pairing codes of the auth package (HMAC-SHA256 under the server
// secret, hex encoded) with its own domain prefix, so the two kinds of code
// cannot be swapped. An empty secret gives a plain SHA-256.
func hashCode(code, hmacSecret string) string {
	plain := codeHashDomain + NormalizeCode(code)
	if hmacSecret == "" {
		h := sha256.Sum256([]byte(plain))
		return hex.EncodeToString(h[:])
	}
	mac := hmac.New(sha256.New, []byte(hmacSecret))
	mac.Write([]byte(plain))
	return hex.EncodeToString(mac.Sum(nil))
}

func constantTimeEq(a, b string) bool {
	return subtle.ConstantTimeCompare([]byte(a), []byte(b)) == 1
}
