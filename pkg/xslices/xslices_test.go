// SPDX-License-Identifier: MIT
package xslices_test

import (
	"encoding/json"
	"testing"

	"github.com/mudler/LocalAI/pkg/xslices"
)

func TestNonNil(t *testing.T) {
	var none []string
	b, _ := json.Marshal(xslices.NonNil(none))
	if string(b) != "[]" {
		t.Fatalf("a nil list encoded as %s, want []", b)
	}
	b, _ = json.Marshal(xslices.NonNil([]string{"a"}))
	if string(b) != `["a"]` {
		t.Fatalf("a list encoded as %s", b)
	}
	type names []string
	if got := xslices.NonNil(names(nil)); got == nil {
		t.Fatal("a nil named list stayed nil")
	}
}
