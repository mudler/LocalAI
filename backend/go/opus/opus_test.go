package main

import (
	"encoding/binary"
	"fmt"
	"io"
	"math"
	"math/rand/v2"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"testing"
	"time"

	pb "github.com/mudler/LocalAI/pkg/grpc/proto"
	"github.com/mudler/LocalAI/pkg/sound"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/pion/rtp"
	"github.com/pion/webrtc/v4"
)

func TestOpusBackend(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Opus Backend Suite")
}

// --- helpers ---

func generateSineWave(freq float64, sampleRate, numSamples int) []int16 {
	out := make([]int16, numSamples)
	for i := range out {
		t := float64(i) / float64(sampleRate)
		out[i] = int16(math.MaxInt16 / 2 * math.Sin(2*math.Pi*freq*t))
	}
	return out
}

func computeRMS(samples []int16) float64 {
	if len(samples) == 0 {
		return 0
	}
	var sum float64
	for _, s := range samples {
		v := float64(s)
		sum += v * v
	}
	return math.Sqrt(sum / float64(len(samples)))
}

func estimateFrequency(samples []int16, sampleRate int) float64 {
	if len(samples) < 2 {
		return 0
	}
	crossings := 0
	for i := 1; i < len(samples); i++ {
		if (samples[i-1] >= 0 && samples[i] < 0) || (samples[i-1] < 0 && samples[i] >= 0) {
			crossings++
		}
	}
	duration := float64(len(samples)) / float64(sampleRate)
	return float64(crossings) / (2 * duration)
}

// encodeDecodeRoundtrip uses the Opus backend to encode PCM and decode all
// resulting frames, returning the concatenated decoded samples.
func encodeDecodeRoundtrip(o *Opus, pcmBytes []byte, sampleRate int) []int16 {
	encResult, err := o.AudioEncode(&pb.AudioEncodeRequest{
		PcmData:    pcmBytes,
		SampleRate: int32(sampleRate),
		Channels:   1,
	})
	Expect(err).ToNot(HaveOccurred(), "AudioEncode")

	if len(encResult.Frames) == 0 {
		return nil
	}

	decResult, err := o.AudioDecode(&pb.AudioDecodeRequest{
		Frames: encResult.Frames,
	})
	Expect(err).ToNot(HaveOccurred(), "AudioDecode")

	return sound.BytesToInt16sLE(decResult.PcmData)
}

func extractOpusFramesFromOgg(data []byte) [][]byte {
	var frames [][]byte
	pos := 0
	pageNum := 0

	for pos+27 <= len(data) {
		Expect(string(data[pos:pos+4])).To(Equal("OggS"), fmt.Sprintf("invalid Ogg page at offset %d", pos))

		nSegments := int(data[pos+26])
		if pos+27+nSegments > len(data) {
			break
		}

		segTable := data[pos+27 : pos+27+nSegments]
		dataStart := pos + 27 + nSegments

		var totalDataSize int
		for _, s := range segTable {
			totalDataSize += int(s)
		}

		if dataStart+totalDataSize > len(data) {
			break
		}

		if pageNum >= 2 {
			pageData := data[dataStart : dataStart+totalDataSize]
			offset := 0
			var packet []byte
			for _, segSize := range segTable {
				packet = append(packet, pageData[offset:offset+int(segSize)]...)
				offset += int(segSize)
				if segSize < 255 {
					if len(packet) > 0 {
						frameCopy := make([]byte, len(packet))
						copy(frameCopy, packet)
						frames = append(frames, frameCopy)
					}
					packet = nil
				}
			}
			if len(packet) > 0 {
				frameCopy := make([]byte, len(packet))
				copy(frameCopy, packet)
				frames = append(frames, frameCopy)
			}
		}

		pos = dataStart + totalDataSize
		pageNum++
	}

	return frames
}

func parseTestWAV(data []byte) (pcm []byte, sampleRate int) {
	if len(data) < 44 || string(data[0:4]) != "RIFF" {
		return data, 0
	}
	pos := 12
	sr := int(binary.LittleEndian.Uint32(data[24:28]))
	for pos+8 <= len(data) {
		id := string(data[pos : pos+4])
		sz := int(binary.LittleEndian.Uint32(data[pos+4 : pos+8]))
		if id == "data" {
			end := pos + 8 + sz
			if end > len(data) {
				end = len(data)
			}
			return data[pos+8 : end], sr
		}
		pos += 8 + sz
		if sz%2 != 0 {
			pos++
		}
	}
	return data[44:], sr
}

func writeOggOpus(path string, frames [][]byte, sampleRate, channels int) error {
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer f.Close()

	serial := uint32(0x4C6F6341) // "LocA"
	var pageSeq uint32
	const preSkip = 312

	opusHead := make([]byte, 19)
	copy(opusHead[0:8], "OpusHead")
	opusHead[8] = 1
	opusHead[9] = byte(channels)
	binary.LittleEndian.PutUint16(opusHead[10:12], uint16(preSkip))
	binary.LittleEndian.PutUint32(opusHead[12:16], uint32(sampleRate))
	binary.LittleEndian.PutUint16(opusHead[16:18], 0)
	opusHead[18] = 0
	if err := writeOggPage(f, serial, pageSeq, 0, 0x02, [][]byte{opusHead}); err != nil {
		return err
	}
	pageSeq++

	opusTags := make([]byte, 16)
	copy(opusTags[0:8], "OpusTags")
	binary.LittleEndian.PutUint32(opusTags[8:12], 0)
	binary.LittleEndian.PutUint32(opusTags[12:16], 0)
	if err := writeOggPage(f, serial, pageSeq, 0, 0x00, [][]byte{opusTags}); err != nil {
		return err
	}
	pageSeq++

	var granulePos uint64
	for i, frame := range frames {
		granulePos += 960
		headerType := byte(0x00)
		if i == len(frames)-1 {
			headerType = 0x04
		}
		if err := writeOggPage(f, serial, pageSeq, granulePos, headerType, [][]byte{frame}); err != nil {
			return err
		}
		pageSeq++
	}

	return nil
}

func writeOggPage(w io.Writer, serial, pageSeq uint32, granulePos uint64, headerType byte, packets [][]byte) error {
	var segments []byte
	var pageData []byte
	for _, pkt := range packets {
		remaining := len(pkt)
		for remaining >= 255 {
			segments = append(segments, 255)
			remaining -= 255
		}
		segments = append(segments, byte(remaining))
		pageData = append(pageData, pkt...)
	}

	hdr := make([]byte, 27+len(segments))
	copy(hdr[0:4], "OggS")
	hdr[4] = 0
	hdr[5] = headerType
	binary.LittleEndian.PutUint64(hdr[6:14], granulePos)
	binary.LittleEndian.PutUint32(hdr[14:18], serial)
	binary.LittleEndian.PutUint32(hdr[18:22], pageSeq)
	hdr[26] = byte(len(segments))
	copy(hdr[27:], segments)

	crc := oggCRC32(hdr, pageData)
	binary.LittleEndian.PutUint32(hdr[22:26], crc)

	if _, err := w.Write(hdr); err != nil {
		return err
	}
	_, err := w.Write(pageData)
	return err
}

func oggCRC32(header, data []byte) uint32 {
	var crc uint32
	for _, b := range header {
		crc = (crc << 8) ^ oggCRCTable[byte(crc>>24)^b]
	}
	for _, b := range data {
		crc = (crc << 8) ^ oggCRCTable[byte(crc>>24)^b]
	}
	return crc
}

var oggCRCTable = func() [256]uint32 {
	var t [256]uint32
	for i := range 256 {
		r := uint32(i) << 24
		for range 8 {
			if r&0x80000000 != 0 {
				r = (r << 1) ^ 0x04C11DB7
			} else {
				r <<= 1
			}
		}
		t[i] = r
	}
	return t
}()

func goertzel(samples []int16, targetFreq float64, sampleRate int) float64 {
	N := len(samples)
	if N == 0 {
		return 0
	}
	k := 0.5 + float64(N)*targetFreq/float64(sampleRate)
	w := 2 * math.Pi * k / float64(N)
	coeff := 2 * math.Cos(w)
	var s1, s2 float64
	for _, sample := range samples {
		s0 := float64(sample) + coeff*s1 - s2
		s2 = s1
		s1 = s0
	}
	return s1*s1 + s2*s2 - coeff*s1*s2
}

func computeTHD(samples []int16, fundamentalHz float64, sampleRate, numHarmonics int) float64 {
	fundPower := goertzel(samples, fundamentalHz, sampleRate)
	if fundPower <= 0 {
		return 0
	}
	var harmonicSum float64
	for h := 2; h <= numHarmonics; h++ {
		harmonicSum += goertzel(samples, fundamentalHz*float64(h), sampleRate)
	}
	return math.Sqrt(harmonicSum/fundPower) * 100
}

// --- Opus specs ---

var _ = Describe("Opus", func() {
	var o *Opus

	BeforeEach(func() {
		o = &Opus{}
		Expect(o.Load(&pb.ModelOptions{})).To(Succeed())
	})

	It("decodes Chrome-like VoIP frames", func() {
		enc, err := NewEncoder(48000, 1, ApplicationVoIP)
		Expect(err).ToNot(HaveOccurred())
		defer enc.Close()
		Expect(enc.SetBitrate(32000)).To(Succeed())
		Expect(enc.SetComplexity(5)).To(Succeed())

		sine := generateSineWave(440, 48000, 48000)
		packet := make([]byte, 4000)

		var opusFrames [][]byte
		for offset := 0; offset+opusFrameSize <= len(sine); offset += opusFrameSize {
			frame := sine[offset : offset+opusFrameSize]
			n, err := enc.Encode(frame, opusFrameSize, packet)
			Expect(err).ToNot(HaveOccurred(), "VoIP encode")
			out := make([]byte, n)
			copy(out, packet[:n])
			opusFrames = append(opusFrames, out)
		}

		result, err := o.AudioDecode(&pb.AudioDecodeRequest{Frames: opusFrames})
		Expect(err).ToNot(HaveOccurred())

		allDecoded := sound.BytesToInt16sLE(result.PcmData)
		Expect(allDecoded).ToNot(BeEmpty(), "no decoded samples from VoIP encoder")

		skip := min(len(allDecoded)/4, 48000*100/1000)
		tail := allDecoded[skip:]
		rms := computeRMS(tail)

		GinkgoWriter.Printf("VoIP/SILK roundtrip: %d decoded samples, RMS=%.1f\n", len(allDecoded), rms)
		Expect(rms).To(BeNumerically(">=", 50), "VoIP decoded RMS is too low; SILK decoder may be broken")
	})

	It("decodes stereo-encoded Opus with a mono decoder", func() {
		enc, err := NewEncoder(48000, 2, ApplicationVoIP)
		Expect(err).ToNot(HaveOccurred())
		defer enc.Close()
		Expect(enc.SetBitrate(32000)).To(Succeed())

		mono := generateSineWave(440, 48000, 48000)
		stereo := make([]int16, len(mono)*2)
		for i, s := range mono {
			stereo[i*2] = s
			stereo[i*2+1] = s
		}

		packet := make([]byte, 4000)
		var opusFrames [][]byte
		for offset := 0; offset+opusFrameSize*2 <= len(stereo); offset += opusFrameSize * 2 {
			frame := stereo[offset : offset+opusFrameSize*2]
			n, err := enc.Encode(frame, opusFrameSize, packet)
			Expect(err).ToNot(HaveOccurred(), "Stereo encode")
			out := make([]byte, n)
			copy(out, packet[:n])
			opusFrames = append(opusFrames, out)
		}

		result, err := o.AudioDecode(&pb.AudioDecodeRequest{Frames: opusFrames})
		Expect(err).ToNot(HaveOccurred())

		allDecoded := sound.BytesToInt16sLE(result.PcmData)
		Expect(allDecoded).ToNot(BeEmpty(), "no decoded samples from stereo encoder")

		skip := min(len(allDecoded)/4, 48000*100/1000)
		tail := allDecoded[skip:]
		rms := computeRMS(tail)

		GinkgoWriter.Printf("Stereo->Mono: %d decoded samples, RMS=%.1f\n", len(allDecoded), rms)
		Expect(rms).To(BeNumerically(">=", 50), "Stereo->Mono decoded RMS is too low")
	})

	Describe("decoding libopus-encoded audio", func() {
		var ffmpegPath string
		var tmpDir string
		var pcmPath string
		var sine []int16

		BeforeEach(func() {
			var err error
			ffmpegPath, err = exec.LookPath("ffmpeg")
			if err != nil {
				Skip("ffmpeg not found")
			}

			tmpDir = GinkgoT().TempDir()

			sine = generateSineWave(440, 48000, 48000)
			pcmBytes := sound.Int16toBytesLE(sine)
			pcmPath = filepath.Join(tmpDir, "input.raw")
			Expect(os.WriteFile(pcmPath, pcmBytes, 0644)).To(Succeed())
		})

		for _, tc := range []struct {
			name    string
			bitrate string
			app     string
		}{
			{"voip_32k", "32000", "voip"},
			{"voip_64k", "64000", "voip"},
			{"audio_64k", "64000", "audio"},
			{"audio_128k", "128000", "audio"},
		} {
			tc := tc
			It(tc.name, func() {
				oggPath := filepath.Join(tmpDir, fmt.Sprintf("libopus_%s_%s.ogg", tc.app, tc.bitrate))
				cmd := exec.Command(ffmpegPath,
					"-y",
					"-f", "s16le", "-ar", "48000", "-ac", "1", "-i", pcmPath,
					"-c:a", "libopus",
					"-b:a", tc.bitrate,
					"-application", tc.app,
					"-frame_duration", "20",
					"-vbr", "on",
					oggPath,
				)
				out, err := cmd.CombinedOutput()
				Expect(err).ToNot(HaveOccurred(), fmt.Sprintf("ffmpeg encode: %s", out))

				oggData, err := os.ReadFile(oggPath)
				Expect(err).ToNot(HaveOccurred())

				opusFrames := extractOpusFramesFromOgg(oggData)
				Expect(opusFrames).ToNot(BeEmpty(), "no Opus frames extracted from Ogg container")
				GinkgoWriter.Printf("Extracted %d Opus frames from libopus encoder (first frame %d bytes)\n", len(opusFrames), len(opusFrames[0]))

				result, err := o.AudioDecode(&pb.AudioDecodeRequest{Frames: opusFrames})
				Expect(err).ToNot(HaveOccurred())

				allDecoded := sound.BytesToInt16sLE(result.PcmData)
				Expect(allDecoded).ToNot(BeEmpty(), "no decoded samples from libopus-encoded Opus")

				skip := min(len(allDecoded)/4, 48000*100/1000)
				tail := allDecoded[skip:]
				rms := computeRMS(tail)
				freq := estimateFrequency(tail, 48000)

				GinkgoWriter.Printf("libopus->opus-go: %d decoded samples, RMS=%.1f, freq≈%.0f Hz\n", len(allDecoded), rms, freq)

				Expect(rms).To(BeNumerically(">=", 50), "RMS is too low — opus-go cannot decode libopus output")
				Expect(freq).To(BeNumerically("~", 440, 30), fmt.Sprintf("frequency %.0f Hz deviates from expected 440 Hz", freq))
			})
		}
	})

	It("roundtrips at 48kHz", func() {
		sine := generateSineWave(440, 48000, 48000)
		pcmBytes := sound.Int16toBytesLE(sine)

		decoded := encodeDecodeRoundtrip(o, pcmBytes, 48000)
		Expect(decoded).ToNot(BeEmpty())

		decodedSR := 48000
		skipDecoded := decodedSR * 50 / 1000
		if skipDecoded > len(decoded)/2 {
			skipDecoded = len(decoded) / 4
		}
		tail := decoded[skipDecoded:]

		rms := computeRMS(tail)
		GinkgoWriter.Printf("48kHz roundtrip: %d decoded samples, RMS=%.1f\n", len(decoded), rms)

		Expect(rms).To(BeNumerically(">=", 50), "decoded audio RMS is too low; signal appears silent")
	})

	It("roundtrips at 16kHz", func() {
		sine16k := generateSineWave(440, 16000, 16000)
		pcmBytes := sound.Int16toBytesLE(sine16k)

		decoded := encodeDecodeRoundtrip(o, pcmBytes, 16000)
		Expect(decoded).ToNot(BeEmpty())

		decoded16k := sound.ResampleInt16(decoded, 48000, 16000)

		skip := min(len(decoded16k)/4, 16000*50/1000)
		tail := decoded16k[skip:]

		rms := computeRMS(tail)
		GinkgoWriter.Printf("16kHz roundtrip: %d decoded@48k -> %d resampled@16k, RMS=%.1f\n",
			len(decoded), len(decoded16k), rms)

		Expect(rms).To(BeNumerically(">=", 50), "decoded audio RMS is too low; signal appears silent")
	})

	It("returns empty frames for empty input", func() {
		result, err := o.AudioEncode(&pb.AudioEncodeRequest{
			PcmData:    []byte{},
			SampleRate: 48000,
			Channels:   1,
		})
		Expect(err).ToNot(HaveOccurred())
		Expect(result.Frames).To(BeEmpty())
	})

	It("silently drops sub-frame input", func() {
		sine := generateSineWave(440, 48000, 500) // < 960
		pcmBytes := sound.Int16toBytesLE(sine)

		result, err := o.AudioEncode(&pb.AudioEncodeRequest{
			PcmData:    pcmBytes,
			SampleRate: 48000,
			Channels:   1,
		})
		Expect(err).ToNot(HaveOccurred())
		Expect(result.Frames).To(BeEmpty(), fmt.Sprintf("expected 0 frames for %d samples (< 960)", len(sine)))
	})

	It("encodes multiple frames", func() {
		sine := generateSineWave(440, 48000, 2880) // exactly 3 frames
		pcmBytes := sound.Int16toBytesLE(sine)

		result, err := o.AudioEncode(&pb.AudioEncodeRequest{
			PcmData:    pcmBytes,
			SampleRate: 48000,
			Channels:   1,
		})
		Expect(err).ToNot(HaveOccurred())
		Expect(result.Frames).To(HaveLen(3))
	})

	It("produces expected decoded frame size", func() {
		sine := generateSineWave(440, 48000, 960)
		pcmBytes := sound.Int16toBytesLE(sine)

		encResult, err := o.AudioEncode(&pb.AudioEncodeRequest{
			PcmData:    pcmBytes,
			SampleRate: 48000,
			Channels:   1,
		})
		Expect(err).ToNot(HaveOccurred())
		Expect(encResult.Frames).To(HaveLen(1))

		decResult, err := o.AudioDecode(&pb.AudioDecodeRequest{
			Frames: encResult.Frames,
		})
		Expect(err).ToNot(HaveOccurred())

		decoded := sound.BytesToInt16sLE(decResult.PcmData)
		GinkgoWriter.Printf("Encoder input: 960 samples (20ms @ 48kHz)\n")
		GinkgoWriter.Printf("Decoder output: %d samples (%.1fms @ 48kHz)\n",
			len(decoded), float64(len(decoded))/48.0)

		Expect(len(decoded)).To(SatisfyAny(Equal(960), Equal(480)),
			fmt.Sprintf("unexpected decoded frame size %d", len(decoded)))
	})

	It("handles the full WebRTC output path", func() {
		sine16k := generateSineWave(440, 16000, 16000)
		pcmBytes := sound.Int16toBytesLE(sine16k)

		decoded := encodeDecodeRoundtrip(o, pcmBytes, 16000)
		Expect(decoded).ToNot(BeEmpty())

		rms := computeRMS(decoded)
		GinkgoWriter.Printf("WebRTC output path: %d decoded samples at 48kHz, RMS=%.1f\n", len(decoded), rms)

		Expect(rms).To(BeNumerically(">=", 50), "decoded audio RMS is too low")
	})

	It("handles the full WebRTC input path", func() {
		sine48k := generateSineWave(440, 48000, 48000)
		pcmBytes := sound.Int16toBytesLE(sine48k)

		decoded48k := encodeDecodeRoundtrip(o, pcmBytes, 48000)
		Expect(decoded48k).ToNot(BeEmpty())

		step24k := sound.ResampleInt16(decoded48k, 48000, 24000)
		webrtcPath := sound.ResampleInt16(step24k, 24000, 16000)

		rms := computeRMS(webrtcPath)
		GinkgoWriter.Printf("WebRTC input path: %d decoded@48k -> %d@24k -> %d@16k, RMS=%.1f\n",
			len(decoded48k), len(step24k), len(webrtcPath), rms)

		Expect(rms).To(BeNumerically(">=", 50), "WebRTC input path signal lost in pipeline")
	})

	Context("bug documentation", func() {
		It("documents trailing sample loss", func() {
			sine := generateSineWave(440, 48000, 1000)
			pcmBytes := sound.Int16toBytesLE(sine)

			result, err := o.AudioEncode(&pb.AudioEncodeRequest{
				PcmData:    pcmBytes,
				SampleRate: 48000,
				Channels:   1,
			})
			Expect(err).ToNot(HaveOccurred())
			Expect(result.Frames).To(HaveLen(1))

			decResult, err := o.AudioDecode(&pb.AudioDecodeRequest{Frames: result.Frames})
			Expect(err).ToNot(HaveOccurred())

			decoded := sound.BytesToInt16sLE(decResult.PcmData)
			GinkgoWriter.Printf("Input: 1000 samples, Encoded: 1 frame, Decoded: %d samples (40 samples lost)\n", len(decoded))
			Expect(len(decoded)).To(BeNumerically("<=", 960),
				fmt.Sprintf("decoded more samples (%d) than the encoder consumed (960)", len(decoded)))
		})

		It("documents TTS sample rate mismatch", func() {
			sine24k := generateSineWave(440, 24000, 24000)
			pcmBytes := sound.Int16toBytesLE(sine24k)

			decodedBug := encodeDecodeRoundtrip(o, pcmBytes, 16000)
			decodedCorrect := encodeDecodeRoundtrip(o, pcmBytes, 24000)

			skipBug := min(len(decodedBug)/4, 48000*100/1000)
			skipCorrect := min(len(decodedCorrect)/4, 48000*100/1000)

			bugTail := decodedBug[skipBug:]
			correctTail := decodedCorrect[skipCorrect:]

			bugFreq := estimateFrequency(bugTail, 48000)
			correctFreq := estimateFrequency(correctTail, 48000)

			GinkgoWriter.Printf("Bug path:     %d decoded samples, freq≈%.0f Hz (expected ~660 Hz = 440*1.5)\n", len(decodedBug), bugFreq)
			GinkgoWriter.Printf("Correct path: %d decoded samples, freq≈%.0f Hz (expected ~440 Hz)\n", len(decodedCorrect), correctFreq)

			if len(decodedBug) > 0 && len(decodedCorrect) > 0 {
				ratio := float64(len(decodedBug)) / float64(len(decodedCorrect))
				GinkgoWriter.Printf("Sample count ratio (bug/correct): %.2f (expected ~1.5)\n", ratio)
				Expect(ratio).To(BeNumerically(">=", 1.1),
					"expected bug path to produce significantly more samples due to wrong resample ratio")
			}
		})
	})

	Context("batch boundary discontinuity", func() {
		// These tests simulate the exact production pipeline:
		//   Browser encodes → RTP → batch 15 frames (300ms) → decode → resample 48k→16k → append
		// They test both with and without persistent decoders to verify
		// that the session_id persistent decoder path works correctly.

		It("batched decode+resample with persistent decoder matches one-shot", func() {
			// Encode 3 seconds of 440Hz at 48kHz — enough for 10 batches
			sine := generateSineWave(440, 48000, 48000*3)
			pcmBytes := sound.Int16toBytesLE(sine)

			encResult, err := o.AudioEncode(&pb.AudioEncodeRequest{
				PcmData:    pcmBytes,
				SampleRate: 48000,
				Channels:   1,
			})
			Expect(err).ToNot(HaveOccurred())
			GinkgoWriter.Printf("Encoded %d frames (%.0fms)\n", len(encResult.Frames),
				float64(len(encResult.Frames))*20.0)

			// Ground truth: decode ALL frames with one decoder, resample in one shot
			decAll, err := o.AudioDecode(&pb.AudioDecodeRequest{
				Frames:  encResult.Frames,
				Options: map[string]string{"session_id": "ground-truth"},
			})
			Expect(err).ToNot(HaveOccurred())
			allSamples := sound.BytesToInt16sLE(decAll.PcmData)
			oneShotResampled := sound.ResampleInt16(allSamples, 48000, 16000)

			// Production path: decode in 15-frame batches with persistent decoder,
			// resample each batch independently, concatenate
			const framesPerBatch = 15
			sessionID := "batch-test"
			var batchedResampled []int16
			batchCount := 0
			for i := 0; i < len(encResult.Frames); i += framesPerBatch {
				end := min(i+framesPerBatch, len(encResult.Frames))

				decBatch, err := o.AudioDecode(&pb.AudioDecodeRequest{
					Frames:  encResult.Frames[i:end],
					Options: map[string]string{"session_id": sessionID},
				})
				Expect(err).ToNot(HaveOccurred())

				batchSamples := sound.BytesToInt16sLE(decBatch.PcmData)
				batchResampled := sound.ResampleInt16(batchSamples, 48000, 16000)
				batchedResampled = append(batchedResampled, batchResampled...)
				batchCount++
			}

			GinkgoWriter.Printf("Decoded in %d batches, oneshot=%d samples, batched=%d samples\n",
				batchCount, len(oneShotResampled), len(batchedResampled))

			// Skip codec startup transient (first 100ms)
			skip := 16000 * 100 / 1000
			oneShotTail := oneShotResampled[skip:]
			batchedTail := batchedResampled[skip:]
			minLen := min(len(oneShotTail), len(batchedTail))

			// With persistent decoder, batched decode should be nearly identical
			// to one-shot (only difference is resampler batch boundaries).
			var maxDiff float64
			var sumDiffSq float64
			for i := range minLen {
				diff := math.Abs(float64(oneShotTail[i]) - float64(batchedTail[i]))
				if diff > maxDiff {
					maxDiff = diff
				}
				sumDiffSq += diff * diff
			}
			rmsDiff := math.Sqrt(sumDiffSq / float64(minLen))

			GinkgoWriter.Printf("Persistent decoder: maxDiff=%.0f, rmsDiff=%.1f\n", maxDiff, rmsDiff)

			// Tight threshold: with persistent decoder and fixed resampler,
			// the output should be very close to one-shot
			Expect(maxDiff).To(BeNumerically("<", 500),
				"persistent decoder batched path diverges too much from one-shot")
			Expect(rmsDiff).To(BeNumerically("<", 50),
				"RMS deviation too high between batched and one-shot")
		})

		It("fresh decoder per batch produces worse quality than persistent", func() {
			// This test proves the value of persistent decoders by showing
			// that fresh decoders produce larger deviations at batch boundaries.
			sine := generateSineWave(440, 48000, 48000*2)
			pcmBytes := sound.Int16toBytesLE(sine)

			encResult, err := o.AudioEncode(&pb.AudioEncodeRequest{
				PcmData:    pcmBytes,
				SampleRate: 48000,
				Channels:   1,
			})
			Expect(err).ToNot(HaveOccurred())

			// Ground truth: one-shot decode
			decAll, err := o.AudioDecode(&pb.AudioDecodeRequest{
				Frames:  encResult.Frames,
				Options: map[string]string{"session_id": "ref"},
			})
			Expect(err).ToNot(HaveOccurred())
			refSamples := sound.BytesToInt16sLE(decAll.PcmData)

			const framesPerBatch = 15

			// Path A: persistent decoder
			var persistentSamples []int16
			for i := 0; i < len(encResult.Frames); i += framesPerBatch {
				end := min(i+framesPerBatch, len(encResult.Frames))
				dec, err := o.AudioDecode(&pb.AudioDecodeRequest{
					Frames:  encResult.Frames[i:end],
					Options: map[string]string{"session_id": "persistent"},
				})
				Expect(err).ToNot(HaveOccurred())
				persistentSamples = append(persistentSamples, sound.BytesToInt16sLE(dec.PcmData)...)
			}

			// Path B: fresh decoder per batch (no session_id)
			var freshSamples []int16
			for i := 0; i < len(encResult.Frames); i += framesPerBatch {
				end := min(i+framesPerBatch, len(encResult.Frames))
				dec, err := o.AudioDecode(&pb.AudioDecodeRequest{
					Frames: encResult.Frames[i:end],
				})
				Expect(err).ToNot(HaveOccurred())
				freshSamples = append(freshSamples, sound.BytesToInt16sLE(dec.PcmData)...)
			}

			// Compare both to reference
			skip := 48000 * 100 / 1000
			refTail := refSamples[skip:]
			persistentTail := persistentSamples[skip:]
			freshTail := freshSamples[skip:]
			minLen := min(len(refTail), min(len(persistentTail), len(freshTail)))

			var persistentMaxDiff, freshMaxDiff float64
			for i := range minLen {
				pd := math.Abs(float64(refTail[i]) - float64(persistentTail[i]))
				fd := math.Abs(float64(refTail[i]) - float64(freshTail[i]))
				if pd > persistentMaxDiff {
					persistentMaxDiff = pd
				}
				if fd > freshMaxDiff {
					freshMaxDiff = fd
				}
			}

			GinkgoWriter.Printf("vs reference: persistent maxDiff=%.0f, fresh maxDiff=%.0f\n",
				persistentMaxDiff, freshMaxDiff)

			// Persistent decoder should be closer to reference than fresh
			Expect(persistentMaxDiff).To(BeNumerically("<=", freshMaxDiff),
				"persistent decoder should match reference at least as well as fresh decoder")
		})

		It("checks for PCM discontinuities at batch boundaries", func() {
			// Encode 2 seconds, decode in batches, resample, and check
			// for anomalous jumps at the exact batch boundaries in the output
			sine := generateSineWave(440, 48000, 48000*2)
			pcmBytes := sound.Int16toBytesLE(sine)

			encResult, err := o.AudioEncode(&pb.AudioEncodeRequest{
				PcmData:    pcmBytes,
				SampleRate: 48000,
				Channels:   1,
			})
			Expect(err).ToNot(HaveOccurred())

			const framesPerBatch = 15
			sessionID := "boundary-check"
			var batchedOutput []int16
			var batchBoundaries []int // indices where batch boundaries fall in output
			for i := 0; i < len(encResult.Frames); i += framesPerBatch {
				end := min(i+framesPerBatch, len(encResult.Frames))

				dec, err := o.AudioDecode(&pb.AudioDecodeRequest{
					Frames:  encResult.Frames[i:end],
					Options: map[string]string{"session_id": sessionID},
				})
				Expect(err).ToNot(HaveOccurred())

				batchSamples := sound.BytesToInt16sLE(dec.PcmData)
				batchResampled := sound.ResampleInt16(batchSamples, 48000, 16000)

				if len(batchedOutput) > 0 {
					batchBoundaries = append(batchBoundaries, len(batchedOutput))
				}
				batchedOutput = append(batchedOutput, batchResampled...)
			}

			GinkgoWriter.Printf("Output: %d samples, %d batch boundaries\n",
				len(batchedOutput), len(batchBoundaries))

			// For each batch boundary, check if the sample-to-sample jump
			// is anomalously large compared to neighboring deltas
			for bIdx, boundary := range batchBoundaries {
				if boundary < 10 || boundary+10 >= len(batchedOutput) {
					continue
				}

				jump := math.Abs(float64(batchedOutput[boundary]) - float64(batchedOutput[boundary-1]))

				// Compute average delta in the 20-sample neighborhood (excluding boundary)
				var avgDelta float64
				count := 0
				for i := boundary - 10; i < boundary+10; i++ {
					if i == boundary-1 || i == boundary {
						continue
					}
					if i+1 < len(batchedOutput) {
						avgDelta += math.Abs(float64(batchedOutput[i+1]) - float64(batchedOutput[i]))
						count++
					}
				}
				if count > 0 {
					avgDelta /= float64(count)
				}

				ratio := 0.0
				if avgDelta > 0 {
					ratio = jump / avgDelta
				}

				GinkgoWriter.Printf("Boundary %d (idx %d): jump=%.0f, avg_delta=%.0f, ratio=%.1f\n",
					bIdx, boundary, jump, avgDelta, ratio)

				// The boundary jump should not be more than 5x the average
				// (with codec artifacts, some variation is expected)
				Expect(jump).To(BeNumerically("<=", avgDelta*5+1),
					fmt.Sprintf("discontinuity at batch boundary %d: jump=%.0f vs avg=%.0f (ratio=%.1f)",
						bIdx, jump, avgDelta, ratio))
			}
		})

		It("maintains sine wave phase continuity across batches", func() {
			sine := generateSineWave(440, 48000, 48000*2) // 2 seconds
			pcmBytes := sound.Int16toBytesLE(sine)

			encResult, err := o.AudioEncode(&pb.AudioEncodeRequest{
				PcmData:    pcmBytes,
				SampleRate: 48000,
				Channels:   1,
			})
			Expect(err).ToNot(HaveOccurred())

			// Decode in batches with persistent decoder, resample each
			const framesPerBatch = 15
			sessionID := "phase-test"
			var fullOutput []int16
			for i := 0; i < len(encResult.Frames); i += framesPerBatch {
				end := min(i+framesPerBatch, len(encResult.Frames))
				dec, err := o.AudioDecode(&pb.AudioDecodeRequest{
					Frames:  encResult.Frames[i:end],
					Options: map[string]string{"session_id": sessionID},
				})
				Expect(err).ToNot(HaveOccurred())
				samples := sound.BytesToInt16sLE(dec.PcmData)
				resampled := sound.ResampleInt16(samples, 48000, 16000)
				fullOutput = append(fullOutput, resampled...)
			}

			// Check zero-crossing regularity after startup transient
			skip := 16000 * 200 / 1000 // skip first 200ms
			tail := fullOutput[skip:]

			var crossingPositions []int
			for i := 1; i < len(tail); i++ {
				if (tail[i-1] >= 0 && tail[i] < 0) || (tail[i-1] < 0 && tail[i] >= 0) {
					crossingPositions = append(crossingPositions, i)
				}
			}
			Expect(crossingPositions).ToNot(BeEmpty(), "no zero crossings found")

			var intervals []float64
			for i := 1; i < len(crossingPositions); i++ {
				intervals = append(intervals, float64(crossingPositions[i]-crossingPositions[i-1]))
			}

			var sum float64
			for _, v := range intervals {
				sum += v
			}
			mean := sum / float64(len(intervals))

			var variance float64
			for _, v := range intervals {
				d := v - mean
				variance += d * d
			}
			stddev := math.Sqrt(variance / float64(len(intervals)))

			GinkgoWriter.Printf("Zero-crossing intervals: mean=%.2f stddev=%.2f CV=%.3f (expected period ~%.1f)\n",
				mean, stddev, stddev/mean, 16000.0/440.0/2.0)

			Expect(stddev/mean).To(BeNumerically("<", 0.15),
				fmt.Sprintf("irregular zero crossings suggest discontinuity: CV=%.3f", stddev/mean))

			// Also check frequency is correct
			freq := estimateFrequency(tail, 16000)
			GinkgoWriter.Printf("Estimated frequency: %.0f Hz (expected 440)\n", freq)
			Expect(freq).To(BeNumerically("~", 440, 20))
		})

		It("produces identical resampled output for batched vs one-shot resample", func() {
			// Isolate the resampler from the codec: decode once, then compare
			// one-shot resample vs batched resample of the same PCM.
			sine := generateSineWave(440, 48000, 48000*3)
			pcmBytes := sound.Int16toBytesLE(sine)

			encResult, err := o.AudioEncode(&pb.AudioEncodeRequest{
				PcmData:    pcmBytes,
				SampleRate: 48000,
				Channels:   1,
			})
			Expect(err).ToNot(HaveOccurred())

			decResult, err := o.AudioDecode(&pb.AudioDecodeRequest{
				Frames:  encResult.Frames,
				Options: map[string]string{"session_id": "resample-test"},
			})
			Expect(err).ToNot(HaveOccurred())
			allSamples := sound.BytesToInt16sLE(decResult.PcmData)

			// One-shot resample
			oneShot := sound.ResampleInt16(allSamples, 48000, 16000)

			// Batched resample (300ms chunks at 48kHz = 14400 samples)
			batchSize := 48000 * 300 / 1000
			var batched []int16
			for offset := 0; offset < len(allSamples); offset += batchSize {
				end := min(offset+batchSize, len(allSamples))
				chunk := sound.ResampleInt16(allSamples[offset:end], 48000, 16000)
				batched = append(batched, chunk...)
			}

			Expect(len(batched)).To(Equal(len(oneShot)),
				fmt.Sprintf("length mismatch: batched=%d oneshot=%d", len(batched), len(oneShot)))

			// Every sample must be identical — the resampler is deterministic
			var maxDiff float64
			for i := range len(oneShot) {
				diff := math.Abs(float64(oneShot[i]) - float64(batched[i]))
				if diff > maxDiff {
					maxDiff = diff
				}
			}

			GinkgoWriter.Printf("Resample-only: batched vs one-shot maxDiff=%.0f\n", maxDiff)
			Expect(maxDiff).To(BeNumerically("==", 0),
				"batched resample should produce identical output to one-shot resample")
		})

		It("writes WAV files for manual inspection", func() {
			// This test writes WAV files of the batched vs one-shot pipeline
			// so you can visually/audibly inspect for discontinuities.
			tmpDir := GinkgoT().TempDir()

			sine := generateSineWave(440, 48000, 48000*3) // 3 seconds
			pcmBytes := sound.Int16toBytesLE(sine)

			encResult, err := o.AudioEncode(&pb.AudioEncodeRequest{
				PcmData:    pcmBytes,
				SampleRate: 48000,
				Channels:   1,
			})
			Expect(err).ToNot(HaveOccurred())

			// One-shot path (reference)
			decAll, err := o.AudioDecode(&pb.AudioDecodeRequest{
				Frames:  encResult.Frames,
				Options: map[string]string{"session_id": "wav-ref"},
			})
			Expect(err).ToNot(HaveOccurred())
			refSamples := sound.BytesToInt16sLE(decAll.PcmData)
			refResampled := sound.ResampleInt16(refSamples, 48000, 16000)

			// Batched path (production simulation)
			const framesPerBatch = 15
			var batchedResampled []int16
			for i := 0; i < len(encResult.Frames); i += framesPerBatch {
				end := min(i+framesPerBatch, len(encResult.Frames))
				dec, err := o.AudioDecode(&pb.AudioDecodeRequest{
					Frames:  encResult.Frames[i:end],
					Options: map[string]string{"session_id": "wav-batched"},
				})
				Expect(err).ToNot(HaveOccurred())
				samples := sound.BytesToInt16sLE(dec.PcmData)
				resampled := sound.ResampleInt16(samples, 48000, 16000)
				batchedResampled = append(batchedResampled, resampled...)
			}

			// Write WAV files
			writeWAV := func(path string, samples []int16, sampleRate int) {
				dataLen := len(samples) * 2
				hdr := make([]byte, 44)
				copy(hdr[0:4], "RIFF")
				binary.LittleEndian.PutUint32(hdr[4:8], uint32(36+dataLen))
				copy(hdr[8:12], "WAVE")
				copy(hdr[12:16], "fmt ")
				binary.LittleEndian.PutUint32(hdr[16:20], 16)                   // chunk size
				binary.LittleEndian.PutUint16(hdr[20:22], 1)                    // PCM
				binary.LittleEndian.PutUint16(hdr[22:24], 1)                    // mono
				binary.LittleEndian.PutUint32(hdr[24:28], uint32(sampleRate))   // sample rate
				binary.LittleEndian.PutUint32(hdr[28:32], uint32(sampleRate*2)) // byte rate
				binary.LittleEndian.PutUint16(hdr[32:34], 2)                    // block align
				binary.LittleEndian.PutUint16(hdr[34:36], 16)                   // bits per sample
				copy(hdr[36:40], "data")
				binary.LittleEndian.PutUint32(hdr[40:44], uint32(dataLen))

				f, err := os.Create(path)
				Expect(err).ToNot(HaveOccurred())
				defer f.Close()
				_, err = f.Write(hdr)
				Expect(err).ToNot(HaveOccurred())
				_, err = f.Write(sound.Int16toBytesLE(samples))
				Expect(err).ToNot(HaveOccurred())
			}

			refPath := filepath.Join(tmpDir, "oneshot_16k.wav")
			batchedPath := filepath.Join(tmpDir, "batched_16k.wav")
			writeWAV(refPath, refResampled, 16000)
			writeWAV(batchedPath, batchedResampled, 16000)

			GinkgoWriter.Printf("WAV files written for manual inspection:\n")
			GinkgoWriter.Printf("  Reference: %s\n", refPath)
			GinkgoWriter.Printf("  Batched:   %s\n", batchedPath)
			GinkgoWriter.Printf("  Ref samples: %d, Batched samples: %d\n",
				len(refResampled), len(batchedResampled))
		})
	})

	It("produces frames decodable by ffmpeg (cross-library compat)", func() {
		ffmpegPath, err := exec.LookPath("ffmpeg")
		if err != nil {
			Skip("ffmpeg not found")
		}

		sine := generateSineWave(440, 48000, 48000)
		pcmBytes := sound.Int16toBytesLE(sine)

		result, err := o.AudioEncode(&pb.AudioEncodeRequest{
			PcmData:    pcmBytes,
			SampleRate: 48000,
			Channels:   1,
		})
		Expect(err).ToNot(HaveOccurred())
		Expect(result.Frames).ToNot(BeEmpty())
		GinkgoWriter.Printf("opus-go produced %d frames (first frame %d bytes)\n", len(result.Frames), len(result.Frames[0]))

		tmpDir := GinkgoT().TempDir()
		oggPath := filepath.Join(tmpDir, "opus_go_output.ogg")
		Expect(writeOggOpus(oggPath, result.Frames, 48000, 1)).To(Succeed())

		decodedWavPath := filepath.Join(tmpDir, "ffmpeg_decoded.wav")
		cmd := exec.Command(ffmpegPath, "-y", "-i", oggPath, "-ar", "48000", "-ac", "1", "-c:a", "pcm_s16le", decodedWavPath)
		out, err := cmd.CombinedOutput()
		Expect(err).ToNot(HaveOccurred(), fmt.Sprintf("ffmpeg failed to decode opus-go output: %s", out))

		decodedData, err := os.ReadFile(decodedWavPath)
		Expect(err).ToNot(HaveOccurred())

		decodedPCM, sr := parseTestWAV(decodedData)
		Expect(sr).ToNot(BeZero(), "ffmpeg output has no WAV header")
		decodedSamples := sound.BytesToInt16sLE(decodedPCM)

		skip := min(len(decodedSamples)/4, sr*100/1000)
		if skip >= len(decodedSamples) {
			skip = 0
		}
		tail := decodedSamples[skip:]
		rms := computeRMS(tail)

		GinkgoWriter.Printf("ffmpeg decoded opus-go output: %d samples at %dHz, RMS=%.1f\n", len(decodedSamples), sr, rms)

		Expect(rms).To(BeNumerically(">=", 50),
			"ffmpeg decoded RMS is too low — opus-go frames are likely incompatible with standard decoders")
	})

	It("delivers audio through a full WebRTC pipeline", func() {
		const (
			toneFreq       = 440.0
			toneSampleRate = 24000
			toneDuration   = 1
			toneAmplitude  = 16000
			toneNumSamples = toneSampleRate * toneDuration
		)

		pcm := make([]byte, toneNumSamples*2)
		for i := range toneNumSamples {
			sample := int16(toneAmplitude * math.Sin(2*math.Pi*toneFreq*float64(i)/float64(toneSampleRate)))
			binary.LittleEndian.PutUint16(pcm[i*2:], uint16(sample))
		}

		encResult, err := o.AudioEncode(&pb.AudioEncodeRequest{
			PcmData:    pcm,
			SampleRate: toneSampleRate,
			Channels:   1,
		})
		Expect(err).ToNot(HaveOccurred())
		Expect(encResult.Frames).ToNot(BeEmpty())
		GinkgoWriter.Printf("Encoded %d Opus frames from %d PCM samples at %dHz\n", len(encResult.Frames), toneNumSamples, toneSampleRate)

		// Create sender PeerConnection
		senderME := &webrtc.MediaEngine{}
		Expect(senderME.RegisterDefaultCodecs()).To(Succeed())
		senderAPI := webrtc.NewAPI(webrtc.WithMediaEngine(senderME))
		senderPC, err := senderAPI.NewPeerConnection(webrtc.Configuration{})
		Expect(err).ToNot(HaveOccurred())
		defer senderPC.Close()

		audioTrack, err := webrtc.NewTrackLocalStaticRTP(
			webrtc.RTPCodecCapability{
				MimeType:  webrtc.MimeTypeOpus,
				ClockRate: 48000,
				Channels:  2,
			},
			"audio", "test",
		)
		Expect(err).ToNot(HaveOccurred())

		rtpSender, err := senderPC.AddTrack(audioTrack)
		Expect(err).ToNot(HaveOccurred())
		go func() {
			buf := make([]byte, 1500)
			for {
				if _, _, err := rtpSender.Read(buf); err != nil {
					return
				}
			}
		}()

		// Create receiver PeerConnection
		receiverME := &webrtc.MediaEngine{}
		Expect(receiverME.RegisterDefaultCodecs()).To(Succeed())
		receiverAPI := webrtc.NewAPI(webrtc.WithMediaEngine(receiverME))
		receiverPC, err := receiverAPI.NewPeerConnection(webrtc.Configuration{})
		Expect(err).ToNot(HaveOccurred())
		defer receiverPC.Close()

		type receivedPacket struct {
			seqNum    uint16
			timestamp uint32
			marker    bool
			payload   []byte
		}
		var (
			receivedMu      sync.Mutex
			receivedPackets []receivedPacket
			trackDone       = make(chan struct{})
		)

		receiverPC.OnTrack(func(track *webrtc.TrackRemote, receiver *webrtc.RTPReceiver) {
			defer close(trackDone)
			for {
				pkt, _, err := track.ReadRTP()
				if err != nil {
					return
				}
				payload := make([]byte, len(pkt.Payload))
				copy(payload, pkt.Payload)
				receivedMu.Lock()
				receivedPackets = append(receivedPackets, receivedPacket{
					seqNum:    pkt.Header.SequenceNumber,
					timestamp: pkt.Header.Timestamp,
					marker:    pkt.Header.Marker,
					payload:   payload,
				})
				receivedMu.Unlock()
			}
		})

		// Exchange SDP
		offer, err := senderPC.CreateOffer(nil)
		Expect(err).ToNot(HaveOccurred())
		Expect(senderPC.SetLocalDescription(offer)).To(Succeed())
		senderGatherDone := webrtc.GatheringCompletePromise(senderPC)
		Eventually(senderGatherDone, 5*time.Second).Should(BeClosed())

		Expect(receiverPC.SetRemoteDescription(*senderPC.LocalDescription())).To(Succeed())
		answer, err := receiverPC.CreateAnswer(nil)
		Expect(err).ToNot(HaveOccurred())
		Expect(receiverPC.SetLocalDescription(answer)).To(Succeed())
		receiverGatherDone := webrtc.GatheringCompletePromise(receiverPC)
		Eventually(receiverGatherDone, 5*time.Second).Should(BeClosed())

		Expect(senderPC.SetRemoteDescription(*receiverPC.LocalDescription())).To(Succeed())

		// Wait for connection
		connected := make(chan struct{})
		senderPC.OnConnectionStateChange(func(s webrtc.PeerConnectionState) {
			if s == webrtc.PeerConnectionStateConnected {
				select {
				case <-connected:
				default:
					close(connected)
				}
			}
		})
		Eventually(connected, 5*time.Second).Should(BeClosed())

		// Send test tone via RTP
		const samplesPerFrame = 960
		seqNum := uint16(rand.UintN(65536))
		timestamp := rand.Uint32()
		marker := true

		ticker := time.NewTicker(20 * time.Millisecond)
		defer ticker.Stop()

		for i, frame := range encResult.Frames {
			pkt := &rtp.Packet{
				Header: rtp.Header{
					Version:        2,
					Marker:         marker,
					SequenceNumber: seqNum,
					Timestamp:      timestamp,
				},
				Payload: frame,
			}
			seqNum++
			timestamp += samplesPerFrame
			marker = false

			Expect(audioTrack.WriteRTP(pkt)).To(Succeed(), fmt.Sprintf("WriteRTP frame %d", i))
			if i < len(encResult.Frames)-1 {
				<-ticker.C
			}
		}

		// Wait for packets to arrive
		time.Sleep(500 * time.Millisecond)

		senderPC.Close()

		select {
		case <-trackDone:
		case <-time.After(2 * time.Second):
		}

		// Decode received Opus frames via the backend
		receivedMu.Lock()
		pkts := make([]receivedPacket, len(receivedPackets))
		copy(pkts, receivedPackets)
		receivedMu.Unlock()

		Expect(pkts).ToNot(BeEmpty(), "no RTP packets received")

		var receivedFrames [][]byte
		for _, pkt := range pkts {
			receivedFrames = append(receivedFrames, pkt.payload)
		}

		decResult, err := o.AudioDecode(&pb.AudioDecodeRequest{Frames: receivedFrames})
		Expect(err).ToNot(HaveOccurred())

		allDecoded := sound.BytesToInt16sLE(decResult.PcmData)
		Expect(allDecoded).ToNot(BeEmpty(), "no decoded samples")

		// Analyse RTP packet delivery
		frameLoss := len(encResult.Frames) - len(pkts)
		seqGaps := 0
		for i := 1; i < len(pkts); i++ {
			expected := pkts[i-1].seqNum + 1
			if pkts[i].seqNum != expected {
				seqGaps++
			}
		}
		markerCount := 0
		for _, pkt := range pkts {
			if pkt.marker {
				markerCount++
			}
		}

		GinkgoWriter.Println("── RTP Delivery ──")
		GinkgoWriter.Printf("  Frames sent:     %d\n", len(encResult.Frames))
		GinkgoWriter.Printf("  Packets recv:    %d\n", len(pkts))
		GinkgoWriter.Printf("  Frame loss:      %d\n", frameLoss)
		GinkgoWriter.Printf("  Sequence gaps:   %d\n", seqGaps)
		GinkgoWriter.Printf("  Marker packets:  %d (expect 1)\n", markerCount)

		// Audio quality metrics
		skip := 48000 * 100 / 1000
		if skip > len(allDecoded)/2 {
			skip = len(allDecoded) / 4
		}
		tail := allDecoded[skip:]

		rms := computeRMS(tail)
		freq := estimateFrequency(tail, 48000)
		thd := computeTHD(tail, toneFreq, 48000, 10)

		GinkgoWriter.Println("── Audio Quality ──")
		GinkgoWriter.Printf("  Decoded samples: %d (%.1f ms at 48kHz)\n", len(allDecoded), float64(len(allDecoded))/48.0)
		GinkgoWriter.Printf("  RMS level:       %.1f\n", rms)
		GinkgoWriter.Printf("  Peak frequency:  %.0f Hz (expected %.0f Hz)\n", freq, toneFreq)
		GinkgoWriter.Printf("  THD (h2-h10):    %.1f%%\n", thd)

		Expect(frameLoss).To(BeZero(), "lost frames in localhost transport")
		Expect(seqGaps).To(BeZero(), "sequence number gaps detected")
		Expect(markerCount).To(Equal(1), "expected exactly 1 marker packet")
		Expect(rms).To(BeNumerically(">=", 50), "signal appears silent or severely attenuated")
		Expect(freq).To(BeNumerically("~", toneFreq, 20), fmt.Sprintf("peak frequency %.0f Hz deviates from expected", freq))
		Expect(thd).To(BeNumerically("<", 50), "signal is severely distorted")
	})
})
