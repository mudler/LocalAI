// SPDX-License-Identifier: MIT
package randtoken_test

import (
	"regexp"
	"testing"

	"github.com/mudler/LocalAI/pkg/randtoken"
)

func TestRandtoken(t *testing.T) {
	b64, err := randtoken.Base64URL(32)
	if err != nil || !regexp.MustCompile(`^[A-Za-z0-9_-]{43}$`).MatchString(b64) {
		t.Fatalf("base64url token %q, err %v", b64, err)
	}
	h, err := randtoken.Hex(16)
	if err != nil || !regexp.MustCompile(`^[0-9a-f]{32}$`).MatchString(h) {
		t.Fatalf("hex token %q, err %v", h, err)
	}
	other, _ := randtoken.Hex(16)
	if h == other {
		t.Fatal("two tokens were equal")
	}
}
