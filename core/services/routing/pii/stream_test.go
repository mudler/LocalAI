package pii

import (
	"context"
	"fmt"
	"math/rand"
	"strings"
	"unicode/utf8"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func newStreamRedactor(ids ...string) *Redactor {
	all := DefaultPatterns()
	chosen := all
	if len(ids) > 0 {
		chosen = pick(all, ids)
	}
	patterns, err := Compile(chosen)
	ExpectWithOffset(1, err).NotTo(HaveOccurred(), "compile")
	return NewRedactor(patterns)
}

var _ = Describe("StreamFilter", func() {
	It("masks across chunks", func() {
		// The most important streaming test: an email split arbitrarily
		// across chunk boundaries must mask exactly the same way as one
		// arriving in a single Push.
		red := newStreamRedactor("email")
		sf := NewStreamFilter(red, nil, nil, "", "")

		// "alice@example.com" (17 bytes) split between '@' and 'e'.
		out := ""
		out += sf.Push("hi alice@")
		out += sf.Push("example.com! end")
		out += sf.Drain()

		Expect(out).NotTo(ContainSubstring("alice@example.com"), "stream leaked email across chunk boundary")
		Expect(out).To(ContainSubstring("[REDACTED:email]"))
	})

	It("block becomes mask", func() {
		// api_key_prefix is block by default. In stream mode the earlier
		// chunks are already on the wire so block is impossible — the
		// filter remaps to mask while still recording action="block" so
		// the audit log keeps the original intent.
		red := newStreamRedactor("api_key_prefix")
		store := NewMemoryEventStore(0)
		defer func() { _ = store.Close() }()
		sf := NewStreamFilter(red, nil, store, "corr-1", "user-1")

		out := sf.Push("here is your token: sk-abcdefghijklmnopqrstuvwxyz0123456789 done")
		out += sf.Drain()

		Expect(out).NotTo(ContainSubstring("abcdefghijklmnopqrstuvwxyz0123456789"), "block-in-stream must mask, leaked the value")
		Expect(out).To(ContainSubstring("[REDACTED:api_key_prefix]"))

		events, _ := store.List(context.Background(), ListQuery{Limit: 10})
		Expect(events).To(HaveLen(1))
		Expect(events[0].Action).To(Equal(ActionBlock), "audit must record original block action")
		Expect(events[0].Direction).To(Equal(DirectionOut), "stream events must be DirectionOut")
	})

	It("no match passthrough", func() {
		red := newStreamRedactor("email")
		sf := NewStreamFilter(red, nil, nil, "", "")
		out := sf.Push("perfectly clean text that should") + sf.Push(" pass through unchanged.") + sf.Drain()
		Expect(out).To(Equal("perfectly clean text that should pass through unchanged."))
	})

	It("nil redactor passthrough", func() {
		// --disable-pii path: NewStreamFilter(nil, ...) returns a filter
		// that just forwards Push input verbatim.
		sf := NewStreamFilter(nil, nil, nil, "", "")
		out := sf.Push("any old text including alice@example.com") + sf.Drain()
		Expect(out).To(Equal("any old text including alice@example.com"))
	})

	It("per-model overrides", func() {
		// email defaults to mask; per-model override upgrades to block.
		// In stream mode the override still maps to mask placeholder, but
		// the audit event records action="block".
		red := newStreamRedactor("email")
		store := NewMemoryEventStore(0)
		defer func() { _ = store.Close() }()
		sf := NewStreamFilter(red, map[string]Action{"email": ActionBlock}, store, "corr-2", "user-2")

		out := sf.Push("contact alice@example.com please") + sf.Drain()
		Expect(out).NotTo(ContainSubstring("alice@example.com"), "override block-in-stream must mask")
		events, _ := store.List(context.Background(), ListQuery{Limit: 10})
		Expect(events).To(HaveLen(1))
		Expect(events[0].Action).To(Equal(ActionBlock))
	})

	// StreamFilter_BufferedEmitInvariant feeds the redactor a corpus
	// one rune at a time, randomly chunked, and asserts:
	//
	//   1. Across all (input, splitting) pairs, the cumulative emitted
	//      output never contains any of the secret values that were
	//      embedded in the input.
	//   2. The output, fully drained, equals what Redact would have
	//      produced on the unsplit input.
	//
	// This is the load-bearing property of streaming PII: regardless of
	// where chunks split, the emitted bytes cannot contain a value that a
	// single-shot redactor would have masked.
	It("buffered emit invariant", func() {
		corpus := []struct {
			text    string
			secrets []string
		}{
			{"contact alice@example.com or bob@example.org", []string{"alice@example.com", "bob@example.org"}},
			{"my SSN is 123-45-6789 and his is 987-65-4321", []string{"123-45-6789", "987-65-4321"}},
			{"sk-abcdefghijklmnopqrstuvwxyz0123456789 leaked", []string{"sk-abcdefghijklmnopqrstuvwxyz0123456789"}},
			{"repeats: alice@example.com / alice@example.com / alice@example.com", []string{"alice@example.com"}},
			// Multibyte UTF-8 corpora pin the rune-boundary snap in
			// StreamFilter.Push: holdLen is byte-sized, so a chunk boundary
			// may land mid-codepoint. Without the snap, the retained tail
			// has a partial codepoint and the next regex scan can mis-align.
			// Each entry mixes ASCII secrets with surrounding multibyte text
			// so a byte-aligned cut would land inside a CJK or accented
			// character on at least some splits.
			{"こんにちは alice@example.com さようなら", []string{"alice@example.com"}},
			{"クレジットカード: 4111-1111-1111-1111 終わり", []string{"4111-1111-1111-1111"}},
			{"naïve résumé: alice@example.com, façade", []string{"alice@example.com"}},
		}

		red := newStreamRedactor()        // all default patterns
		rng := rand.New(rand.NewSource(1)) // seeded for reproducibility

		for _, tc := range corpus {
			for trial := 0; trial < 10; trial++ {
				sf := NewStreamFilter(red, nil, nil, "", "")
				var out strings.Builder
				for i := 0; i < utf8.RuneCountInString(tc.text); {
					// Random chunk size 1-8 runes, never crossing the end.
					chunk := 1 + rng.Intn(8)
					if i+chunk > utf8.RuneCountInString(tc.text) {
						chunk = utf8.RuneCountInString(tc.text) - i
					}
					out.WriteString(sf.Push(stringSlice(tc.text, i, i+chunk)))
					i += chunk
				}
				out.WriteString(sf.Drain())
				result := out.String()

				// Property 1: no secret value appears anywhere in the
				// output.
				for _, secret := range tc.secrets {
					Expect(result).NotTo(ContainSubstring(secret),
						fmt.Sprintf("trial %d: secret %q leaked through streaming\n  input: %q\n  output: %q", trial, secret, tc.text, result))
				}

				// Property 2: the streamed output equals what a single-shot
				// Redact would have produced on the same input. (Block
				// patterns get masked in stream mode, so we compare against
				// a remapped redaction.)
				expected := singleShotMaskAll(red, tc.text)
				Expect(result).To(Equal(expected),
					fmt.Sprintf("trial %d: stream != single-shot\n  input: %q", trial, tc.text))
			}
		}
	})
})

// singleShotMaskAll runs the redactor in one pass with all blocks
// remapped to mask — the same view the StreamFilter produces.
func singleShotMaskAll(red *Redactor, text string) string {
	patterns := red.Patterns()
	overrides := make(map[string]Action, len(patterns))
	for _, p := range patterns {
		if p.Action == ActionBlock {
			overrides[p.ID] = ActionMask
		}
	}
	res := red.RedactWithOverrides(text, overrides)
	return res.Redacted
}

func stringSlice(s string, fromRune, toRune int) string {
	runes := []rune(s)
	return string(runes[fromRune:toRune])
}
