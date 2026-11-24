package clientip

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestResolve(t *testing.T) {
	cases := []struct {
		name    string
		trusted string
		peer    string
		xff     []string
		realIP  string
		want    string
	}{
		{"empty list ignores xff", "", "192.0.2.10:1234", []string{"203.0.113.9"}, "203.0.113.8", "192.0.2.10"},
		{"untrusted peer spoofs xff", "10.0.0.0/8", "192.0.2.10:1234", []string{"203.0.113.9"}, "203.0.113.8", "192.0.2.10"},
		{"trusted peer, one hop", "10.0.0.0/8", "10.0.0.5:1", []string{"203.0.113.9"}, "", "203.0.113.9"},
		{"client forges left, proxy appends real", "10.0.0.0/8", "10.0.0.5:1", []string{"1.1.1.1, 203.0.113.9"}, "", "203.0.113.9"},
		{"multiple hops skip trusted", "10.0.0.0/8,192.168.1.1", "10.0.0.5:1", []string{"203.0.113.9, 10.1.1.1, 192.168.1.1"}, "", "203.0.113.9"},
		{"multiple header lines", "10.0.0.0/8", "10.0.0.5:1", []string{"1.1.1.1", "203.0.113.9, 10.2.2.2"}, "", "203.0.113.9"},
		{"all hops trusted gives left-most", "10.0.0.0/8", "10.0.0.5:1", []string{"10.9.9.9, 10.1.1.1"}, "", "10.9.9.9"},
		{"trusted peer, no headers", "10.0.0.0/8", "10.0.0.5:1", nil, "", "10.0.0.5"},
		{"x-real-ip fallback", "10.0.0.0/8", "10.0.0.5:1", nil, "203.0.113.8", "203.0.113.8"},
		{"x-real-ip ignored when xff present", "10.0.0.0/8", "10.0.0.5:1", []string{"203.0.113.9"}, "198.51.100.1", "203.0.113.9"},
		{"x-real-ip ignored from untrusted peer", "10.0.0.0/8", "192.0.2.10:1", nil, "203.0.113.8", "192.0.2.10"},
		{"single IP in list", "192.0.2.10", "192.0.2.10:1", []string{"203.0.113.9"}, "", "203.0.113.9"},
		{"ipv6 client", "10.0.0.0/8", "10.0.0.5:1", []string{"2001:db8::1"}, "", "2001:db8::1"},
		{"ipv6 client with brackets and port", "10.0.0.0/8", "10.0.0.5:1", []string{"[2001:db8::1]:5555"}, "", "2001:db8::1"},
		{"ipv4 client with port", "10.0.0.0/8", "10.0.0.5:1", []string{"203.0.113.9:4444"}, "", "203.0.113.9"},
		{"ipv6 trusted peer and range", "2001:db8:ff::/48", "[2001:db8:ff::7]:1", []string{"2001:db8:1::5, 2001:db8:ff::8"}, "", "2001:db8:1::5"},
		{"ipv6 untrusted peer", "2001:db8:ff::/48", "[2001:db8:aa::7]:1", []string{"203.0.113.9"}, "", "2001:db8:aa::7"},
		{"ipv4-mapped peer is trusted as ipv4", "10.0.0.0/8", "[::ffff:10.0.0.5]:1", []string{"203.0.113.9"}, "", "203.0.113.9"},
		{"ipv6 zone stripped", "10.0.0.0/8", "10.0.0.5:1", []string{"fe80::1%eth0"}, "", "fe80::1"},
		{"malformed right-most stops the walk", "10.0.0.0/8", "10.0.0.5:1", []string{"203.0.113.9, garbage"}, "", "10.0.0.5"},
		{"malformed after trusted hop returns that hop", "10.0.0.0/8", "10.0.0.5:1", []string{"garbage, 10.4.4.4"}, "", "10.4.4.4"},
		{"empty entry", "10.0.0.0/8", "10.0.0.5:1", []string{"203.0.113.9,"}, "", "10.0.0.5"},
		{"whitespace only xff", "10.0.0.0/8", "10.0.0.5:1", []string{"  "}, "", "10.0.0.5"},
		{"hostname is not an address", "10.0.0.0/8", "10.0.0.5:1", []string{"evil.example.com"}, "", "10.0.0.5"},
		{"peer without port", "10.0.0.0/8", "10.0.0.5", []string{"203.0.113.9"}, "", "203.0.113.9"},
		{"unparsable peer is returned as is", "10.0.0.0/8", "pipe", []string{"203.0.113.9"}, "", "pipe"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r, err := Parse(tc.trusted)
			if err != nil {
				t.Fatal(err)
			}
			req := httptest.NewRequest(http.MethodGet, "/", nil)
			req.RemoteAddr = tc.peer
			for _, v := range tc.xff {
				req.Header.Add("X-Forwarded-For", v)
			}
			if tc.realIP != "" {
				req.Header.Set("X-Real-Ip", tc.realIP)
			}
			if got := r.Resolve(req); got != tc.want {
				t.Fatalf("got %q, want %q", got, tc.want)
			}
		})
	}
}

func TestNilResolverTrustsNobody(t *testing.T) {
	var r *Resolver
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.RemoteAddr = "192.0.2.1:1"
	req.Header.Set("X-Forwarded-For", "203.0.113.9")
	if got := r.Resolve(req); got != "192.0.2.1" {
		t.Fatalf("got %q", got)
	}
	if r.Enabled() {
		t.Fatal("nil resolver must be disabled")
	}
}

func TestParseErrors(t *testing.T) {
	for _, s := range []string{"nope", "10.0.0.0/33", "10.0.0.1, bad", "::ffff:0:0/64"} {
		if _, err := Parse(s); err == nil {
			t.Errorf("Parse(%q): expected an error", s)
		}
	}
	for _, s := range []string{"", " , ", "10.0.0.0/8, 2001:db8::/32 ,192.0.2.1", "::ffff:10.0.0.0/104"} {
		if _, err := Parse(s); err != nil {
			t.Errorf("Parse(%q): %v", s, err)
		}
	}
}

func TestTrustsPeer(t *testing.T) {
	r, err := Parse("10.0.0.0/8")
	if err != nil {
		t.Fatal(err)
	}
	for peer, want := range map[string]bool{
		"10.0.0.5:1":          true,
		"[::ffff:10.0.0.5]:1": true,
		"192.0.2.10:1":        false,
		"garbage":             false,
	} {
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		req.RemoteAddr = peer
		if got := r.TrustsPeer(req); got != want {
			t.Errorf("TrustsPeer(%q) = %v, want %v", peer, got, want)
		}
	}
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	if (*Resolver)(nil).TrustsPeer(req) || (&Resolver{}).TrustsPeer(req) {
		t.Error("empty resolver must trust nobody")
	}
}
