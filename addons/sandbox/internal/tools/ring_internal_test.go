// SPDX-License-Identifier: MIT
package tools

import (
	"strings"
	"testing"
	"time"
)

func TestRingOffsets(t *testing.T) {
	r := newRing(16, time.Now)
	_, _ = r.Write([]byte("0123456789"))
	data, from, next := r.page(0, 4)
	if string(data) != "0123" || from != 0 || next != 4 {
		t.Fatalf("page(0,4) = %q %d %d", data, from, next)
	}
	_, _ = r.Write([]byte("abcdefghij")) // 20 bytes written, 16 kept
	data, from, next = r.page(0, 100)
	if from != 4 || next != 20 || string(data) != "456789abcdefghij" {
		t.Fatalf("a lost offset moves to the oldest kept byte: %q %d %d", data, from, next)
	}
	data, from, next = r.page(500, 10)
	if len(data) != 0 || from != 20 || next != 20 {
		t.Fatalf("an offset past the end: %q %d %d", data, from, next)
	}
	if total, _ := r.state(); total != 20 {
		t.Fatalf("total = %d", total)
	}
	// One write larger than the ring keeps its last bytes.
	_, _ = r.Write([]byte(strings.Repeat("x", 40) + "END"))
	data, from, _ = r.page(0, 100)
	if from != 47 || !strings.HasSuffix(string(data), "END") || len(data) != 16 {
		t.Fatalf("a large write: %q from %d", data, from)
	}
	if got := string(r.tail(3)); got != "END" {
		t.Fatalf("tail = %q", got)
	}
}

func TestRingNeverSplitsACharacter(t *testing.T) {
	r := newRing(64, time.Now)
	_, _ = r.Write([]byte("aé€😀b")) // 1 + 2 + 3 + 4 + 1 bytes
	for max := 1; max <= 11; max++ {
		data, _, next := r.page(0, max)
		if !strings.HasPrefix("aé€😀b", string(data)) || int(next) != len(data) {
			t.Fatalf("max %d: %q next %d", max, data, next)
		}
		for _, c := range string(data) {
			if c == '�' {
				t.Fatalf("max %d split a character: %q", max, data)
			}
		}
	}
	// A lost offset that lands inside a character moves past it.
	small := newRing(5, time.Now)
	_, _ = small.Write([]byte("ab€😀")) // 9 bytes; the ring keeps the last 5: two bytes of € and 😀... one byte of €
	data, from, _ := small.page(0, 10)
	if string(data) != "😀" || from != 5 {
		t.Fatalf("lost start inside a character: %q from %d", data, from)
	}
	if got := string(small.tail(5)); got != "😀" {
		t.Fatalf("tail starts on a character: %q", got)
	}
	// Bytes that are not UTF-8 are passed on, not dropped.
	bad := newRing(8, time.Now)
	_, _ = bad.Write([]byte{0xff, 0xfe, 'x'})
	if data, _, next := bad.page(0, 2); len(data) != 2 || next != 2 {
		t.Fatalf("invalid bytes: %v %d", data, next)
	}
}
