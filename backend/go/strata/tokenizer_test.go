package main

import (
	"os"
	"testing"
	"time"
)

// TestHelperTokenizer drives the real Python helper. It needs a Strata
// source checkout (STRATA_ROOT) and a pack directory whose tokenizer/
// folder exists (STRATA_TEST_PACK); it is skipped otherwise.
func TestHelperTokenizer(t *testing.T) {
	root, pack := os.Getenv("STRATA_ROOT"), os.Getenv("STRATA_TEST_PACK")
	if root == "" || pack == "" {
		t.Skip("set STRATA_ROOT and STRATA_TEST_PACK to run")
	}
	tok, err := newHelperTokenizer("", root, pack, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	defer tok.Close()
	ids, err := tok.Encode("hi", true)
	if err != nil || len(ids) == 0 {
		t.Fatalf("encode: %v %v", ids, err)
	}
	var back []byte
	for _, id := range ids {
		b, ok := tok.TokenBytes(id)
		if !ok {
			t.Fatalf("no bytes for id %d", id)
		}
		back = append(back, b...)
	}
	if string(back) != "hi" {
		t.Fatalf("round trip gave %q", back)
	}
	p, err := tok.ApplyChatTemplate([]ChatMessage{{Role: "user", Content: "hi"}}, "", nil)
	if err != nil || p == "" {
		t.Fatalf("template: %q %v", p, err)
	}
	if _, err := newHelperTokenizer("", root, t.TempDir(), time.Minute); err == nil {
		t.Fatal("a directory with no tokenizer must fail to load")
	}
}
