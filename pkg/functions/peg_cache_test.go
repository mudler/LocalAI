package functions

import (
	"fmt"
	"math/rand"
	"strings"
	"sync"
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("preset PEG parser cache", func() {
	It("parses as a parser built for the call, from many goroutines", func() {
		r := rand.New(rand.NewSource(13))
		inputs := make([]string, 300)
		for i := range inputs {
			var b strings.Builder
			for range 1 + r.Intn(10) {
				b.WriteString(streamFragments[r.Intn(len(streamFragments))])
			}
			inputs[i] = b.String()
		}
		var wg sync.WaitGroup
		failures := make(chan string, len(inputs))
		for w := range 4 {
			wg.Go(func() {
				defer GinkgoRecover()
				for i, input := range inputs {
					if i%4 != w {
						continue
					}
					for _, preset := range getAllXMLFormats() {
						for _, pegType := range []PEGFormatType{FormatJSONNative, FormatTagWithJSON, FormatTagWithTagged} {
							fresh := BuildPEGParserFromFormat(preset.format, pegType)
							cached := presetPEGParser(preset.name, preset.format, pegType)
							if (fresh == nil) != (cached == nil) {
								failures <- fmt.Sprintf("%s/%d: built %v, cached %v", preset.name, pegType, fresh != nil, cached != nil)
								continue
							}
							if fresh == nil {
								continue
							}
							want := fmt.Sprintf("%+v", parsePEG(fresh, input))
							got := fmt.Sprintf("%+v", parsePEG(cached, input))
							if got != want {
								failures <- fmt.Sprintf("%s/%d input %q: got %s want %s", preset.name, pegType, input, got, want)
							}
						}
					}
				}
			})
		}
		wg.Wait()
		close(failures)
		var all []string
		for f := range failures {
			all = append(all, f)
		}
		Expect(all).To(BeEmpty())
	})
})

// BenchmarkParseFunctionCallPEG parses the end of a reply that holds a tool
// call, as the stream workers do once per request and the Anthropic stream
// for each token of a call: with the preset parsers built for every call, as
// before the cache, and cached.
func BenchmarkParseFunctionCallPEG(b *testing.B) {
	cfg := FunctionsConfig{}
	s := strings.Repeat(" word", 300) + `{"name": "get_weather", "arguments": {"location": "San Francisco", "unit": "celsius", "days": 3}}`
	b.Run("build-per-call", func(b *testing.B) {
		b.ReportAllocs()
		for b.Loop() {
			presetPEGParsers.Clear()
			ParseFunctionCall(s, cfg)
		}
	})
	b.Run("cached", func(b *testing.B) {
		b.ReportAllocs()
		for b.Loop() {
			ParseFunctionCall(s, cfg)
		}
	})
}
