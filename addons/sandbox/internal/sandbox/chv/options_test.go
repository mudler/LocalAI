package chv

import "testing"

func TestParseVersion(t *testing.T) {
	for out, want := range map[string][2]int{
		"cloud-hypervisor v54.0\n": {54, 0},
		"virtiofsd 1.14.0\n":       {1, 14},
		"bubblewrap 0.11.1\n":      {0, 11},
		"mke2fs 1.47.2 (1-Jan-2025)\n\tUsing EXT2FS Library version 1.47.2\n": {1, 47},
		"debugfs 1.47.2 (1-Jan-2025)\n":                                       {1, 47},
	} {
		got, ok := parseVersion([]byte(out))
		if !ok || got != want {
			t.Fatalf("%q = %v %v", out, got, ok)
		}
	}
	if _, ok := parseVersion([]byte("no version here")); ok {
		t.Fatal("found a version in text without one")
	}
}
