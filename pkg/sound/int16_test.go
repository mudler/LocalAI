package sound

import (
	"fmt"
	"math"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("Int16 utilities", func() {
	Describe("BytesToInt16sLE / Int16toBytesLE", func() {
		It("round-trips correctly", func() {
			values := []int16{0, 1, -1, 32767, -32768}
			b := Int16toBytesLE(values)
			got := BytesToInt16sLE(b)

			Expect(got).To(Equal(values))
		})

		It("panics on odd-length input", func() {
			Expect(func() {
				BytesToInt16sLE([]byte{0x01, 0x02, 0x03})
			}).To(Panic())
		})

		It("returns empty slice for empty bytes input", func() {
			got := BytesToInt16sLE([]byte{})
			Expect(got).To(BeEmpty())
		})

		It("returns empty slice for empty int16 input", func() {
			got := Int16toBytesLE([]int16{})
			Expect(got).To(BeEmpty())
		})
	})

	Describe("ResampleInt16", func() {
		It("returns identical output for same rate", func() {
			src := generateSineWave(440, 16000, 320)
			dst := ResampleInt16(src, 16000, 16000)

			Expect(dst).To(Equal(src))
		})

		It("downsamples 48k to 16k", func() {
			src := generateSineWave(440, 48000, 960)
			dst := ResampleInt16(src, 48000, 16000)

			Expect(dst).To(HaveLen(320))

			freq := estimateFrequency(dst, 16000)
			Expect(freq).To(BeNumerically("~", 440, 50))
		})

		It("upsamples 16k to 48k", func() {
			src := generateSineWave(440, 16000, 320)
			dst := ResampleInt16(src, 16000, 48000)

			Expect(dst).To(HaveLen(960))

			freq := estimateFrequency(dst, 48000)
			Expect(freq).To(BeNumerically("~", 440, 50))
		})

		It("preserves quality through double resampling", func() {
			src := generateSineWave(440, 48000, 4800) // 100ms

			direct := ResampleInt16(src, 48000, 16000)

			step1 := ResampleInt16(src, 48000, 24000)
			double := ResampleInt16(step1, 24000, 16000)

			minLen := len(direct)
			if len(double) < minLen {
				minLen = len(double)
			}

			corr := computeCorrelation(direct[:minLen], double[:minLen])
			Expect(corr).To(BeNumerically(">=", 0.95))
		})

		It("handles single sample", func() {
			src := []int16{1000}
			got := ResampleInt16(src, 48000, 16000)
			Expect(got).NotTo(BeEmpty())
			Expect(got[0]).To(Equal(int16(1000)))
		})

		It("returns nil for empty input", func() {
			got := ResampleInt16(nil, 48000, 16000)
			Expect(got).To(BeNil())
		})

		It("produces no discontinuity at batch boundaries (48k->16k)", func() {
			// Generate 900ms of 440Hz sine at 48kHz (simulating 3 decode batches)
			fullSine := generateSineWave(440, 48000, 48000*900/1000) // 43200 samples

			// One-shot resample (ground truth)
			oneShot := ResampleInt16(fullSine, 48000, 16000)

			// Batched resample: split into 3 batches of 300ms (14400 samples each)
			batchSize := 48000 * 300 / 1000 // 14400
			var batched []int16
			for offset := 0; offset < len(fullSine); offset += batchSize {
				end := offset + batchSize
				if end > len(fullSine) {
					end = len(fullSine)
				}
				chunk := ResampleInt16(fullSine[offset:end], 48000, 16000)
				batched = append(batched, chunk...)
			}

			// Lengths should match
			Expect(len(batched)).To(Equal(len(oneShot)))

			// Check discontinuity at each batch boundary
			batchOutSize := len(ResampleInt16(fullSine[:batchSize], 48000, 16000))
			for b := 1; b < 3; b++ {
				boundaryIdx := b * batchOutSize
				if boundaryIdx >= len(batched) || boundaryIdx < 1 {
					continue
				}
				// The sample-to-sample delta at the boundary
				jump := math.Abs(float64(batched[boundaryIdx]) - float64(batched[boundaryIdx-1]))
				// Compare with the average delta in the interior (excluding boundary)
				var avgDelta float64
				count := 0
				start := boundaryIdx - 10
				if start < 1 {
					start = 1
				}
				stop := boundaryIdx + 10
				if stop >= len(batched) {
					stop = len(batched) - 1
				}
				for i := start; i < stop; i++ {
					if i == boundaryIdx-1 || i == boundaryIdx {
						continue
					}
					avgDelta += math.Abs(float64(batched[i+1]) - float64(batched[i]))
					count++
				}
				avgDelta /= float64(count)

				GinkgoWriter.Printf("Batch boundary %d (idx %d): jump=%.0f, avg_delta=%.0f, ratio=%.1f\n",
					b, boundaryIdx, jump, avgDelta, jump/avgDelta)

				// The boundary jump should not be more than 3x the average delta
				Expect(jump).To(BeNumerically("<=", avgDelta*3),
					fmt.Sprintf("discontinuity at batch boundary %d: jump=%.0f vs avg=%.0f", b, jump, avgDelta))
			}

			// Overall correlation should be very high
			minLen := len(oneShot)
			if len(batched) < minLen {
				minLen = len(batched)
			}
			corr := computeCorrelation(oneShot[:minLen], batched[:minLen])
			Expect(corr).To(BeNumerically(">=", 0.999),
				"batched resample differs significantly from one-shot")
		})

		It("interpolates the last sample instead of using raw input value", func() {
			// Create a ramp signal where each value is unique
			input := make([]int16, 14400) // 300ms at 48kHz
			for i := range input {
				input[i] = int16(i % 32000)
			}

			output := ResampleInt16(input, 48000, 16000) // ratio 3.0

			// The last output sample should be at interpolated position (len(output)-1)*3.0
			lastIdx := len(output) - 1
			expectedPos := float64(lastIdx) * 3.0
			expectedInputIdx := int(expectedPos)
			// At integer position with frac=0, the interpolated value equals input[expectedInputIdx]
			expectedVal := input[expectedInputIdx]

			GinkgoWriter.Printf("Last output[%d]: %d, expected (interpolated at input[%d]): %d, raw last input[%d]: %d\n",
				lastIdx, output[lastIdx], expectedInputIdx, expectedVal, len(input)-1, input[len(input)-1])

			Expect(output[lastIdx]).To(Equal(expectedVal),
				"last sample should be interpolated, not raw input[last]")
		})
	})

	Describe("CalculateRMS16", func() {
		It("computes correct RMS for constant signal", func() {
			buf := make([]int16, 1000)
			for i := range buf {
				buf[i] = 1000
			}
			rms := CalculateRMS16(buf)
			Expect(rms).To(BeNumerically("~", 1000, 0.01))
		})

		It("returns zero for silence", func() {
			buf := make([]int16, 1000)
			rms := CalculateRMS16(buf)
			Expect(rms).To(BeZero())
		})

		It("computes correct RMS for known sine wave", func() {
			amplitude := float64(math.MaxInt16 / 2)
			buf := generateSineWave(440, 16000, 16000) // 1 second
			rms := CalculateRMS16(buf)
			expectedRMS := amplitude / math.Sqrt(2)

			Expect(rms).To(BeNumerically("~", expectedRMS, expectedRMS*0.02))
		})
	})
})
