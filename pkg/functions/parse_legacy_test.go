package functions

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/rand"
	"strings"
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// parseJSONLegacyDecoderOnly is parseJSONLegacy before skipNonJSON: one
// decoder for every position it stops at.
func parseJSONLegacyDecoderOnly(s string) ([]map[string]any, error) {
	var objs []map[string]any
	offset := 0
	for offset < len(s) {
		var obj map[string]any
		decoder := json.NewDecoder(strings.NewReader(s[offset:]))
		err := decoder.Decode(&obj)
		switch {
		case errors.Is(err, io.EOF):
			return objs, nil
		case err == nil:
			offset += int(decoder.InputOffset())
			objs = append(objs, obj)
		default:
			var syntaxErr *json.SyntaxError
			var unmarshalTypeErr *json.UnmarshalTypeError
			switch {
			case errors.As(err, &syntaxErr):
				offset += int(syntaxErr.Offset)
			case errors.As(err, &unmarshalTypeErr):
				offset += int(unmarshalTypeErr.Offset)
			default:
				return objs, err
			}
		}
	}
	return objs, nil
}

var legacyFragments = []string{
	" ", "\n", "\t", "\r", "word", "the", "fun", "nope", "tru", "true", "fals", "false", "nul", "null", "nulls",
	"t", "f", "n", "tr", "fa", "nu", "x", "ü", "日本", "\xff", "\xc3",
	"{", "}", "[", "]", ":", ",", "\"", "\"text\"", "-", "-1", "12", "3.5e2", "0x",
	`{"name": "get_weather", "arguments": {"city": "Rome"}}`, `{"name": "f"`, `[{"name": "g", "arguments": {}}]`,
	`{"a": [1, 2, {"b": null}]}`, `{"bad": }`, `{"k": tru}`, "\\", "\\n",
}

var _ = Describe("parseJSONLegacy", func() {
	It("returns what a decoder at every stop returns", func() {
		r := rand.New(rand.NewSource(7))
		for run := range 20000 {
			var b strings.Builder
			for range 1 + r.Intn(12) {
				b.WriteString(legacyFragments[r.Intn(len(legacyFragments))])
			}
			s := b.String()
			want, wantErr := parseJSONLegacyDecoderOnly(s)
			got, gotErr := parseJSONLegacy(s)
			at := fmt.Sprintf("run %d input %q", run, s)
			Expect(got).To(Equal(want), at)
			Expect(gotErr == nil).To(Equal(wantErr == nil), at)
			if wantErr != nil {
				Expect(gotErr.Error()).To(Equal(wantErr.Error()), at)
			}
		}
	})
})

// BenchmarkParseJSONLegacy parses a prose answer that ends in a tool call, as
// the stream worker does for each token once the call starts.
func BenchmarkParseJSONLegacy(b *testing.B) {
	prose := strings.Repeat(" the function is not here but the answer follows, 42 times.", 40)
	s := prose + `{"name": "get_weather", "arguments": {"location": "Rome"}}`
	b.Run("decoder-only", func(b *testing.B) {
		b.ReportAllocs()
		for b.Loop() {
			_, _ = parseJSONLegacyDecoderOnly(s)
		}
	})
	b.Run("skip", func(b *testing.B) {
		b.ReportAllocs()
		for b.Loop() {
			_, _ = parseJSONLegacy(s)
		}
	})
}
