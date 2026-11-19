package updatenotify

import "testing"

func TestWithoutPaths(t *testing.T) {
	cases := []struct{ name, in, want string }{
		{"absolute path", "open /srv/models/tiny.gguf: no such file", "open tiny.gguf: no such file"},
		{"path at the start", "/srv/models/tiny.gguf is missing", "tiny.gguf is missing"},
		{"after an equals sign", "path=/srv/models/tiny.gguf failed", "path=tiny.gguf failed"},
		{"quoted", `cannot read "/srv/models/tiny.gguf"`, `cannot read "tiny.gguf"`},
		{"in parentheses", "failed (/srv/models/tiny.gguf)", "failed (tiny.gguf)"},
		{"windows path", `open C:\models\tiny.gguf: denied`, "open tiny.gguf: denied"},
		{"url", "get https://example.com/org/repo/file.gguf: 404", "get https://example.com/org/repo/file.gguf: 404"},
		{"scheme-less url", "huggingface.co/org/repo/file.gguf", "huggingface.co/org/repo/file.gguf"},
		{"relative path", "models/sub/x.gguf", "models/sub/x.gguf"},
		{"digits", "1/2/3", "1/2/3"},
		{"relative path then absolute", "models/a.gguf and /srv/x/b.gguf", "models/a.gguf and b.gguf"},
		{"no path", "disk full", "disk full"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := withoutPaths(c.in); got != c.want {
				t.Fatalf("withoutPaths(%q) = %q, want %q", c.in, got, c.want)
			}
		})
	}
}
