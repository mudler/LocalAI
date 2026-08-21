package main

import "testing"

func TestResolveAddr(t *testing.T) {
	tests := []struct {
		name     string
		flagAddr string
		args     []string
		want     string
	}{
		{
			name:     "explicit flag wins over positional",
			flagAddr: "127.0.0.1:12345",
			args:     []string{"127.0.0.1:59999"},
			want:     "127.0.0.1:12345",
		},
		{
			name:     "positional used when flag is at default",
			flagAddr: defaultAddr,
			args:     []string{"127.0.0.1:59999"},
			want:     "127.0.0.1:59999",
		},
		{
			name:     "default kept when no positional",
			flagAddr: defaultAddr,
			args:     nil,
			want:     defaultAddr,
		},
		{
			name:     "first positional wins among several",
			flagAddr: defaultAddr,
			args:     []string{"127.0.0.1:59999", "extra"},
			want:     "127.0.0.1:59999",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := resolveAddr(tt.flagAddr, tt.args); got != tt.want {
				t.Errorf("resolveAddr(%q, %v) = %q, want %q", tt.flagAddr, tt.args, got, tt.want)
			}
		})
	}
}
