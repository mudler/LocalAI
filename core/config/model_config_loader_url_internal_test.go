package config

import "testing"

func TestFileURLPath(t *testing.T) {
	for _, tc := range []struct {
		raw  string
		want string
		ok   bool
	}{
		// Plain POSIX-style file URLs are unchanged.
		{"file:///tmp/gallery.yaml", "/tmp/gallery.yaml", true},
		{"file:///models/dir/gallery.yaml", "/models/dir/gallery.yaml", true},
		// Windows drive in the authority, with forward or back separators.
		{"file://C:/models/gallery.yaml", "C:/models/gallery.yaml", true},
		{"file://c:/models/gallery.yaml", "c:/models/gallery.yaml", true},
		{"file://C:\\models\\gallery.yaml", "C:/models/gallery.yaml", true},
		// Windows drive after the root slash (the canonical RFC 8089 form).
		{"file:///C:/models/gallery.yaml", "C:/models/gallery.yaml", true},
		// UNC authority is preserved.
		{"file://server/share/gallery.yaml", "//server/share/gallery.yaml", true},
		{"file://SERVER/share/gallery.yaml", "//SERVER/share/gallery.yaml", true},
		// Not local files.
		{"https://example.com/gallery.yaml", "", false},
		{"github:example/model.yaml", "", false},
		{"file://", "", false},
		{"file:C:/models/gallery.yaml", "", false},
		{"file://C:/models/gallery.yaml?query=1", "C:/models/gallery.yaml", true},
	} {
		got, ok := fileURLPath(tc.raw)
		if ok != tc.ok || got != tc.want {
			t.Errorf("fileURLPath(%q) = (%q, %v), want (%q, %v)", tc.raw, got, ok, tc.want, tc.ok)
		}
	}
}
