package openai

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

	"github.com/mudler/LocalAI/pkg/opus"
	"github.com/mudler/LocalAI/pkg/sound"
	"github.com/pion/rtp"
	"github.com/pion/webrtc/v4"
)

// --- helpers (mirror pkg/sound/testutil_test.go but in this package) ---

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

// estimateFrequency uses zero-crossing count to estimate the dominant frequency.
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

// encodeDecodeRoundtrip encodes PCM at the given sample rate and decodes
// all resulting frames, returning the concatenated decoded samples.
func encodeDecodeRoundtrip(t *testing.T, pcmBytes []byte, sampleRate int) []int16 {
	t.Helper()
	enc, err := NewOpusEncoder()
	if err != nil {
		t.Fatalf("NewOpusEncoder: %v", err)
	}
	defer enc.Close()

	dec, err := NewOpusDecoder()
	if err != nil {
		t.Fatalf("NewOpusDecoder: %v", err)
	}
	defer dec.Close()

	frames, err := enc.Encode(pcmBytes, sampleRate)
	if err != nil {
		t.Fatalf("Encode: %v", err)
	}

	var all []int16
	for _, frame := range frames {
		d, err := dec.Decode(frame)
		if err != nil {
			t.Fatalf("Decode: %v", err)
		}
		all = append(all, d...)
	}
	return all
}

// --- Opus encoder tests ---

// TestOpus_ChromeLikeVoIPDecode tests decoding Opus frames encoded with
// VoIP mode at 32kbps (similar to Chrome's WebRTC encoder settings).
// Chrome uses SILK mode for voice, which exercises different code paths
// in the decoder compared to ApplicationAudio (CELT-preferring).
func TestOpus_ChromeLikeVoIPDecode(t *testing.T) {
	// Chrome typically encodes voice at 32kbps in VoIP mode
	enc, err := opus.NewEncoder(48000, 1, opus.ApplicationVoIP)
	if err != nil {
		t.Fatalf("NewEncoder(VoIP): %v", err)
	}
	defer enc.Close()
	if err := enc.SetBitrate(32000); err != nil {
		t.Fatalf("SetBitrate: %v", err)
	}
	if err := enc.SetComplexity(5); err != nil {
		t.Fatalf("SetComplexity: %v", err)
	}

	dec, err := NewOpusDecoder()
	if err != nil {
		t.Fatalf("NewOpusDecoder: %v", err)
	}
	defer dec.Close()

	// Encode 1 second of 440Hz sine at 48kHz
	sine := generateSineWave(440, 48000, 48000)
	packet := make([]byte, 4000)

	var allDecoded []int16
	for offset := 0; offset+opusFrameSize <= len(sine); offset += opusFrameSize {
		frame := sine[offset : offset+opusFrameSize]
		n, err := enc.Encode(frame, opusFrameSize, packet)
		if err != nil {
			t.Fatalf("VoIP encode: %v", err)
		}

		decoded, err := dec.Decode(packet[:n])
		if err != nil {
			t.Fatalf("Decode VoIP frame: %v (packet size=%d)", err, n)
		}
		allDecoded = append(allDecoded, decoded...)
	}

	if len(allDecoded) == 0 {
		t.Fatal("no decoded samples from VoIP encoder")
	}

	// Skip warmup
	skip := min(len(allDecoded)/4, 48000*100/1000)
	tail := allDecoded[skip:]
	rms := computeRMS(tail)

	t.Logf("VoIP/SILK roundtrip: %d decoded samples, RMS=%.1f", len(allDecoded), rms)
	if rms < 50 {
		t.Errorf("VoIP decoded RMS=%.1f is too low; SILK decoder may be broken", rms)
	}
}

// TestOpus_StereoEncoderMonoDecoder tests decoding stereo-encoded Opus
// with a mono decoder. Chrome signals opus/48000/2 in SDP and may send
// stereo Opus. The mono decoder should downmix correctly.
func TestOpus_StereoEncoderMonoDecoder(t *testing.T) {
	// Encode as stereo (2 channels) — similar to what Chrome might send
	enc, err := opus.NewEncoder(48000, 2, opus.ApplicationVoIP)
	if err != nil {
		t.Fatalf("NewEncoder(stereo): %v", err)
	}
	defer enc.Close()
	if err := enc.SetBitrate(32000); err != nil {
		t.Fatalf("SetBitrate: %v", err)
	}

	// Decode with our standard mono decoder
	dec, err := NewOpusDecoder()
	if err != nil {
		t.Fatalf("NewOpusDecoder: %v", err)
	}
	defer dec.Close()

	// Create stereo signal: same sine in both channels (interleaved L,R,L,R...)
	mono := generateSineWave(440, 48000, 48000)
	stereo := make([]int16, len(mono)*2)
	for i, s := range mono {
		stereo[i*2] = s   // L
		stereo[i*2+1] = s // R
	}

	packet := make([]byte, 4000)
	var allDecoded []int16
	for offset := 0; offset+opusFrameSize*2 <= len(stereo); offset += opusFrameSize * 2 {
		frame := stereo[offset : offset+opusFrameSize*2]
		n, err := enc.Encode(frame, opusFrameSize, packet)
		if err != nil {
			t.Fatalf("Stereo encode: %v", err)
		}

		decoded, err := dec.Decode(packet[:n])
		if err != nil {
			t.Fatalf("Decode stereo->mono: %v (packet size=%d)", err, n)
		}
		allDecoded = append(allDecoded, decoded...)
	}

	if len(allDecoded) == 0 {
		t.Fatal("no decoded samples from stereo encoder")
	}

	skip := min(len(allDecoded)/4, 48000*100/1000)
	tail := allDecoded[skip:]
	rms := computeRMS(tail)

	t.Logf("Stereo->Mono: %d decoded samples, RMS=%.1f", len(allDecoded), rms)
	if rms < 50 {
		t.Errorf("Stereo->Mono decoded RMS=%.1f is too low; cross-channel decoding may be broken", rms)
	}
}

// TestOpus_DecodeLibopusEncoded uses ffmpeg (real libopus) to encode audio,
// then decodes with our opus-go decoder. This simulates Chrome sending Opus
// frames to the server. Skipped if ffmpeg is not available.
func TestOpus_DecodeLibopusEncoded(t *testing.T) {
	ffmpegPath, err := exec.LookPath("ffmpeg")
	if err != nil {
		t.Skip("ffmpeg not found")
	}

	tmpDir := t.TempDir()

	// Generate 1 second of 440Hz tone as raw PCM (16-bit LE mono 48kHz)
	sine := generateSineWave(440, 48000, 48000)
	pcmPath := filepath.Join(tmpDir, "input.raw")
	pcmBytes := sound.Int16toBytesLE(sine)
	if err := os.WriteFile(pcmPath, pcmBytes, 0644); err != nil {
		t.Fatalf("write PCM: %v", err)
	}

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
		t.Run(tc.name, func(t *testing.T) {
			testDecodeLibopus(t, ffmpegPath, tmpDir, pcmPath, sine, tc.bitrate, tc.app)
		})
	}
}

func testDecodeLibopus(t *testing.T, ffmpegPath, tmpDir, pcmPath string, _ []int16, bitrate, app string) {
	t.Helper()

	oggPath := filepath.Join(tmpDir, fmt.Sprintf("libopus_%s_%s.ogg", app, bitrate))
	cmd := exec.Command(ffmpegPath,
		"-y",
		"-f", "s16le", "-ar", "48000", "-ac", "1", "-i", pcmPath,
		"-c:a", "libopus",
		"-b:a", bitrate,
		"-application", app,
		"-frame_duration", "20",
		"-vbr", "on",
		oggPath,
	)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("ffmpeg encode: %v\n%s", err, out)
	}

	// Read the Ogg/Opus file and extract raw Opus frames
	oggData, err := os.ReadFile(oggPath)
	if err != nil {
		t.Fatalf("read ogg: %v", err)
	}

	frames := extractOpusFramesFromOgg(t, oggData)
	if len(frames) == 0 {
		t.Fatal("no Opus frames extracted from Ogg container")
	}
	t.Logf("Extracted %d Opus frames from libopus encoder (first frame %d bytes)", len(frames), len(frames[0]))

	// Decode with our opus-go decoder
	dec, err := NewOpusDecoder()
	if err != nil {
		t.Fatalf("NewOpusDecoder: %v", err)
	}
	defer dec.Close()

	var allDecoded []int16
	decodeErrors := 0
	for i, frame := range frames {
		decoded, err := dec.Decode(frame)
		if err != nil {
			decodeErrors++
			if decodeErrors <= 5 {
				t.Logf("frame %d: decode error: %v (size=%d)", i, err, len(frame))
			}
			continue
		}
		if i < 5 {
			t.Logf("frame %d: payload=%d bytes, decoded=%d samples (%.1fms @ 48kHz)",
				i, len(frame), len(decoded), float64(len(decoded))/48.0)
		}
		allDecoded = append(allDecoded, decoded...)
	}

	if decodeErrors > 0 {
		t.Logf("Total decode errors: %d/%d frames", decodeErrors, len(frames))
	}

	if len(allDecoded) == 0 {
		t.Fatal("no decoded samples from libopus-encoded Opus")
	}

	// Skip warmup and check quality
	skip := min(len(allDecoded)/4, 48000*100/1000)
	tail := allDecoded[skip:]
	rms := computeRMS(tail)
	freq := estimateFrequency(tail, 48000)

	t.Logf("libopus->opus-go: %d decoded samples, RMS=%.1f, freq≈%.0f Hz", len(allDecoded), rms, freq)

	if rms < 50 {
		t.Errorf("RMS=%.1f is too low — opus-go cannot decode libopus output", rms)
	}
	if math.Abs(freq-440) > 30 {
		t.Errorf("frequency %.0f Hz deviates from expected 440 Hz (ratio=%.3f)", freq, freq/440.0)
	}
}

// extractOpusFramesFromOgg parses an Ogg container and extracts raw Opus audio frames.
func extractOpusFramesFromOgg(t *testing.T, data []byte) [][]byte {
	t.Helper()
	var frames [][]byte
	pos := 0
	pageNum := 0

	for pos+27 <= len(data) {
		// Check for OggS sync
		if string(data[pos:pos+4]) != "OggS" {
			t.Fatalf("invalid Ogg page at offset %d", pos)
		}

		nSegments := int(data[pos+26])
		if pos+27+nSegments > len(data) {
			break
		}

		segTable := data[pos+27 : pos+27+nSegments]
		dataStart := pos + 27 + nSegments

		// Calculate total page data size
		var totalDataSize int
		for _, s := range segTable {
			totalDataSize += int(s)
		}

		if dataStart+totalDataSize > len(data) {
			break
		}

		// Skip first two pages (OpusHead + OpusTags)
		if pageNum >= 2 {
			// Extract packets from segment table
			pageData := data[dataStart : dataStart+totalDataSize]
			offset := 0
			var packet []byte
			for _, segSize := range segTable {
				packet = append(packet, pageData[offset:offset+int(segSize)]...)
				offset += int(segSize)
				if segSize < 255 {
					// End of packet
					if len(packet) > 0 {
						frameCopy := make([]byte, len(packet))
						copy(frameCopy, packet)
						frames = append(frames, frameCopy)
					}
					packet = nil
				}
			}
			// If last segment was 255, packet continues on next page
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

func TestOpusEncodeDecode_Roundtrip_48kHz(t *testing.T) {
	// Use a longer signal (1 second) so the codec can stabilise past its
	// lookahead period and produce meaningful output.
	sine := generateSineWave(440, 48000, 48000)
	pcmBytes := sound.Int16toBytesLE(sine)

	decoded := encodeDecodeRoundtrip(t, pcmBytes, 48000)
	if len(decoded) == 0 {
		t.Fatal("no decoded samples")
	}

	// Skip initial codec warmup (first 50ms) for frequency estimation.
	skip := 48000 * 50 / 1000 // 2400 samples at 48kHz
	// The decoder may return fewer samples per frame (e.g. 480 instead of 960),
	// so the total decoded length may differ. Adjust skip proportionally.
	decodedSR := 48000 // decoder is initialised at 48kHz
	skipDecoded := decodedSR * 50 / 1000
	if skipDecoded > len(decoded)/2 {
		skipDecoded = len(decoded) / 4
	}
	tail := decoded[skipDecoded:]

	rms := computeRMS(tail)
	t.Logf("48kHz roundtrip: %d decoded samples, RMS=%.1f (skip=%d, analysed=%d)",
		len(decoded), rms, skip, len(tail))

	if rms < 50 {
		t.Errorf("decoded audio RMS=%.1f is too low; signal appears silent", rms)
	}
}

func TestOpusEncodeDecode_Roundtrip_16kHz(t *testing.T) {
	// 1 second of 440Hz at 16kHz. Encoder resamples 16k->48k internally.
	sine16k := generateSineWave(440, 16000, 16000)
	pcmBytes := sound.Int16toBytesLE(sine16k)

	decoded := encodeDecodeRoundtrip(t, pcmBytes, 16000)
	if len(decoded) == 0 {
		t.Fatal("no decoded samples")
	}

	// Resample back to 16kHz
	decoded16k := sound.ResampleInt16(decoded, 48000, 16000)

	// Skip warmup
	skip := min(len(decoded16k)/4, 16000*50/1000)
	tail := decoded16k[skip:]

	rms := computeRMS(tail)
	t.Logf("16kHz roundtrip: %d decoded@48k -> %d resampled@16k, RMS=%.1f",
		len(decoded), len(decoded16k), rms)

	if rms < 50 {
		t.Errorf("decoded audio RMS=%.1f is too low; signal appears silent", rms)
	}
}

func TestOpusEncode_EmptyInput(t *testing.T) {
	enc, err := NewOpusEncoder()
	if err != nil {
		t.Fatalf("NewOpusEncoder: %v", err)
	}
	defer enc.Close()

	frames, err := enc.Encode([]byte{}, 48000)
	if err != nil {
		t.Fatalf("Encode empty: %v", err)
	}
	if frames != nil {
		t.Errorf("expected nil frames for empty input, got %d frames", len(frames))
	}
}

func TestOpusEncode_SubFrameInput_SilentDrop(t *testing.T) {
	// Less than 960 samples at 48kHz = not enough for a single frame.
	// The encoder silently drops these trailing samples.
	enc, err := NewOpusEncoder()
	if err != nil {
		t.Fatalf("NewOpusEncoder: %v", err)
	}
	defer enc.Close()

	sine := generateSineWave(440, 48000, 500) // < 960
	pcmBytes := sound.Int16toBytesLE(sine)

	frames, err := enc.Encode(pcmBytes, 48000)
	if err != nil {
		t.Fatalf("Encode: %v", err)
	}
	if len(frames) != 0 {
		t.Errorf("expected 0 frames for %d samples (< 960), got %d", len(sine), len(frames))
	}
}

func TestOpusEncode_MultiFrame(t *testing.T) {
	enc, err := NewOpusEncoder()
	if err != nil {
		t.Fatalf("NewOpusEncoder: %v", err)
	}
	defer enc.Close()

	// 2880 samples at 48kHz = exactly 3 frames of 960
	sine := generateSineWave(440, 48000, 2880)
	pcmBytes := sound.Int16toBytesLE(sine)

	frames, err := enc.Encode(pcmBytes, 48000)
	if err != nil {
		t.Fatalf("Encode: %v", err)
	}
	if len(frames) != 3 {
		t.Errorf("expected 3 frames for 2880 samples, got %d", len(frames))
	}
}

func TestOpusDecode_FrameSize(t *testing.T) {
	// Document the actual decoded frame size from the pure Go opus-go library.
	enc, err := NewOpusEncoder()
	if err != nil {
		t.Fatalf("NewOpusEncoder: %v", err)
	}
	defer enc.Close()

	dec, err := NewOpusDecoder()
	if err != nil {
		t.Fatalf("NewOpusDecoder: %v", err)
	}
	defer dec.Close()

	sine := generateSineWave(440, 48000, 960)
	pcmBytes := sound.Int16toBytesLE(sine)

	frames, err := enc.Encode(pcmBytes, 48000)
	if err != nil {
		t.Fatalf("Encode: %v", err)
	}
	if len(frames) != 1 {
		t.Fatalf("expected 1 frame, got %d", len(frames))
	}

	decoded, err := dec.Decode(frames[0])
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}

	t.Logf("Encoder input: 960 samples (20ms @ 48kHz)")
	t.Logf("Decoder output: %d samples (%.1fms @ 48kHz)",
		len(decoded), float64(len(decoded))/48.0)

	// The decoder may return a different frame size due to internal
	// bandwidth decisions in VoIP mode. Document the actual value.
	if len(decoded) != 960 && len(decoded) != 480 {
		t.Errorf("unexpected decoded frame size %d (expected 960 or 480)", len(decoded))
	}
}

func TestOpus_FullWebRTCOutputPath(t *testing.T) {
	// Simulates the TTS -> SendAudio path:
	// PCM at 16kHz -> Encode(pcm, 16000) -> Opus frames -> Decode -> 48kHz samples
	// Use 1 second of audio to let codec stabilise.
	sine16k := generateSineWave(440, 16000, 16000)
	pcmBytes := sound.Int16toBytesLE(sine16k)

	decoded := encodeDecodeRoundtrip(t, pcmBytes, 16000)
	if len(decoded) == 0 {
		t.Fatal("no frames produced")
	}

	rms := computeRMS(decoded)
	t.Logf("WebRTC output path: %d decoded samples at 48kHz, RMS=%.1f", len(decoded), rms)

	if rms < 50 {
		t.Errorf("decoded audio RMS=%.1f is too low; expected recognisable signal", rms)
	}
}

func TestOpus_FullWebRTCInputPath(t *testing.T) {
	// Simulates the client -> server path:
	// PCM@48k -> Encode -> Decode -> Resample 48k->24k->16k
	// Verify that the pipeline produces non-silent audio.
	sine48k := generateSineWave(440, 48000, 48000) // 1 second
	pcmBytes := sound.Int16toBytesLE(sine48k)

	decoded48k := encodeDecodeRoundtrip(t, pcmBytes, 48000)
	if len(decoded48k) == 0 {
		t.Fatal("no decoded samples")
	}

	// WebRTC path: 48k -> 24k -> (VAD) -> 16k
	step24k := sound.ResampleInt16(decoded48k, 48000, 24000)
	webrtcPath := sound.ResampleInt16(step24k, 24000, 16000)

	rms := computeRMS(webrtcPath)
	t.Logf("WebRTC input path: %d decoded@48k -> %d@24k -> %d@16k, RMS=%.1f",
		len(decoded48k), len(step24k), len(webrtcPath), rms)

	if rms < 50 {
		t.Errorf("WebRTC input path RMS=%.1f is too low; signal lost in pipeline", rms)
	}
}

// --- Bug documentation tests ---

func TestOpusBug_TrailingSampleLoss(t *testing.T) {
	// Encode 1000 samples at 48kHz -> only 1 frame (960 samples) returned.
	// 40 trailing samples are silently lost.
	enc, err := NewOpusEncoder()
	if err != nil {
		t.Fatalf("NewOpusEncoder: %v", err)
	}
	defer enc.Close()

	sine := generateSineWave(440, 48000, 1000)
	pcmBytes := sound.Int16toBytesLE(sine)

	frames, err := enc.Encode(pcmBytes, 48000)
	if err != nil {
		t.Fatalf("Encode: %v", err)
	}
	if len(frames) != 1 {
		t.Fatalf("expected 1 frame, got %d", len(frames))
	}

	dec, err := NewOpusDecoder()
	if err != nil {
		t.Fatalf("NewOpusDecoder: %v", err)
	}
	defer dec.Close()

	decoded, err := dec.Decode(frames[0])
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}

	// The encoder only encoded 960 of 1000 input samples.
	// Decoded frame size may be 960 or 480 depending on codec mode.
	// Either way, 40 input samples are permanently lost.
	t.Logf("Input: 1000 samples, Encoded: 1 frame, Decoded: %d samples (40 samples lost)", len(decoded))
	if len(decoded) > 960 {
		t.Errorf("decoded more samples (%d) than the encoder consumed (960)", len(decoded))
	}
}

func TestOpusBug_TTSSampleRateMismatch(t *testing.T) {
	// If TTS produces 24kHz audio but the pipeline assumes 16kHz,
	// the Opus encoder resamples from 16kHz to 48kHz (3x) instead of
	// 24kHz to 48kHz (2x). The result is pitched up by 50%.
	//
	// This test uses a longer signal and compares the two paths to
	// demonstrate the frequency distortion.

	// Generate 440Hz at 24kHz (what TTS actually produces)
	sine24k := generateSineWave(440, 24000, 24000) // 1 second
	pcmBytes := sound.Int16toBytesLE(sine24k)

	// BUG path: Pipeline passes sampleRate=16000 (assumed) instead of 24000 (actual)
	decodedBug := encodeDecodeRoundtrip(t, pcmBytes, 16000)
	// CORRECT path: Pipeline should pass sampleRate=24000
	decodedCorrect := encodeDecodeRoundtrip(t, pcmBytes, 24000)

	// Skip warmup for frequency estimation
	skipBug := min(len(decodedBug)/4, 48000*100/1000)
	skipCorrect := min(len(decodedCorrect)/4, 48000*100/1000)

	bugTail := decodedBug[skipBug:]
	correctTail := decodedCorrect[skipCorrect:]

	bugFreq := estimateFrequency(bugTail, 48000)
	correctFreq := estimateFrequency(correctTail, 48000)

	t.Logf("Bug path:     %d decoded samples, freq≈%.0f Hz (expected ~660 Hz = 440*1.5)", len(decodedBug), bugFreq)
	t.Logf("Correct path: %d decoded samples, freq≈%.0f Hz (expected ~440 Hz)", len(decodedCorrect), correctFreq)

	// The bug path produces significantly more decoded samples because
	// the encoder thinks the input is 16kHz and upsamples by 3x instead of 2x.
	// This also means the perceived playback speed and pitch are wrong.
	if len(decodedBug) > 0 && len(decodedCorrect) > 0 {
		ratio := float64(len(decodedBug)) / float64(len(decodedCorrect))
		t.Logf("Sample count ratio (bug/correct): %.2f (expected ~1.5)", ratio)
		if ratio < 1.1 {
			t.Error("expected bug path to produce significantly more samples due to wrong resample ratio")
		}
	}
}

// TestOpus_CrossLibraryCompat encodes a sine wave with opus-go, wraps the
// output in a minimal Ogg/Opus container, and decodes it with ffmpeg. This
// catches issues where the pure-Go encoder produces Opus frames that only
// its own decoder can parse (but not a browser or standard decoder).
// Skipped if ffmpeg is not available.
func TestOpus_CrossLibraryCompat(t *testing.T) {
	ffmpegPath, err := exec.LookPath("ffmpeg")
	if err != nil {
		t.Skip("ffmpeg not found, skipping cross-library compatibility test")
	}

	// Encode 1 second of 440Hz sine at 48kHz with opus-go
	sine := generateSineWave(440, 48000, 48000)
	pcmBytes := sound.Int16toBytesLE(sine)

	enc, err := NewOpusEncoder()
	if err != nil {
		t.Fatalf("NewOpusEncoder: %v", err)
	}
	defer enc.Close()

	frames, err := enc.Encode(pcmBytes, 48000)
	if err != nil {
		t.Fatalf("Encode: %v", err)
	}
	if len(frames) == 0 {
		t.Fatal("no frames produced")
	}
	t.Logf("opus-go produced %d frames (first frame %d bytes)", len(frames), len(frames[0]))

	// Wrap the Opus frames in an Ogg/Opus container so ffmpeg can decode them.
	tmpDir := t.TempDir()
	oggPath := filepath.Join(tmpDir, "opus_go_output.ogg")
	if err := writeOggOpus(oggPath, frames, 48000, 1); err != nil {
		t.Fatalf("writeOggOpus: %v", err)
	}

	// Decode with ffmpeg
	decodedWavPath := filepath.Join(tmpDir, "ffmpeg_decoded.wav")
	cmd := exec.Command(ffmpegPath, "-y", "-i", oggPath, "-ar", "48000", "-ac", "1", "-c:a", "pcm_s16le", decodedWavPath)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("ffmpeg failed to decode opus-go output: %v\n%s", err, out)
	}

	// Read the decoded WAV and check audio quality
	decodedData, err := os.ReadFile(decodedWavPath)
	if err != nil {
		t.Fatalf("read decoded WAV: %v", err)
	}

	// Use our robust ParseWAV to handle ffmpeg's WAV output
	decodedPCM, sr := parseTestWAV(decodedData)
	if sr == 0 {
		t.Fatal("ffmpeg output has no WAV header")
	}
	decodedSamples := sound.BytesToInt16sLE(decodedPCM)

	// Skip codec warmup (first 100ms), check RMS of the rest
	skip := min(len(decodedSamples)/4, sr*100/1000)
	if skip >= len(decodedSamples) {
		skip = 0
	}
	tail := decodedSamples[skip:]
	rms := computeRMS(tail)

	t.Logf("ffmpeg decoded opus-go output: %d samples at %dHz, RMS=%.1f", len(decodedSamples), sr, rms)

	if rms < 50 {
		t.Errorf("ffmpeg decoded RMS=%.1f is too low — opus-go frames are likely incompatible with standard decoders", rms)
	} else {
		t.Logf("PASS: opus-go Opus frames are decodable by ffmpeg (libopus) with good signal quality")
	}
}

// parseTestWAV is a simple WAV parser for test output (ffmpeg always writes standard headers).
func parseTestWAV(data []byte) (pcm []byte, sampleRate int) {
	if len(data) < 44 || string(data[0:4]) != "RIFF" {
		return data, 0
	}
	// Walk chunks to find "data"
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

// writeOggOpus writes Opus frames into a minimal Ogg/Opus container file.
func writeOggOpus(path string, frames [][]byte, sampleRate, channels int) error {
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer f.Close()

	serial := uint32(0x4C6F6341) // "LocA"
	var pageSeq uint32
	const preSkip = 312 // standard Opus pre-skip for 48kHz

	// Page 1: OpusHead (BOS page)
	opusHead := make([]byte, 19)
	copy(opusHead[0:8], "OpusHead")
	opusHead[8] = 1                                                         // version
	opusHead[9] = byte(channels)                                            // channel count
	binary.LittleEndian.PutUint16(opusHead[10:12], uint16(preSkip))         // pre-skip
	binary.LittleEndian.PutUint32(opusHead[12:16], uint32(sampleRate))      // input sample rate
	binary.LittleEndian.PutUint16(opusHead[16:18], 0)                       // output gain
	opusHead[18] = 0                                                        // channel mapping family
	if err := writeOggPage(f, serial, pageSeq, 0, 0x02, [][]byte{opusHead}); err != nil {
		return err
	}
	pageSeq++

	// Page 2: OpusTags
	opusTags := make([]byte, 16)
	copy(opusTags[0:8], "OpusTags")
	binary.LittleEndian.PutUint32(opusTags[8:12], 0)  // vendor string length
	binary.LittleEndian.PutUint32(opusTags[12:16], 0) // comment list length
	if err := writeOggPage(f, serial, pageSeq, 0, 0x00, [][]byte{opusTags}); err != nil {
		return err
	}
	pageSeq++

	// Audio pages: one Opus frame per page for simplicity
	var granulePos uint64
	for i, frame := range frames {
		granulePos += 960 // 20ms at 48kHz
		headerType := byte(0x00)
		if i == len(frames)-1 {
			headerType = 0x04 // EOS
		}
		if err := writeOggPage(f, serial, pageSeq, granulePos, headerType, [][]byte{frame}); err != nil {
			return err
		}
		pageSeq++
	}

	return nil
}

// writeOggPage writes a single Ogg page containing the given packets.
func writeOggPage(w io.Writer, serial, pageSeq uint32, granulePos uint64, headerType byte, packets [][]byte) error {
	// Build segment table
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

	// Build page header (27 bytes + segment table)
	hdr := make([]byte, 27+len(segments))
	copy(hdr[0:4], "OggS")
	hdr[4] = 0 // version
	hdr[5] = headerType
	binary.LittleEndian.PutUint64(hdr[6:14], granulePos)
	binary.LittleEndian.PutUint32(hdr[14:18], serial)
	binary.LittleEndian.PutUint32(hdr[18:22], pageSeq)
	// CRC at [22:26] — filled after computing
	hdr[26] = byte(len(segments))
	copy(hdr[27:], segments)

	// Compute CRC-32 over header + page data
	crc := oggCRC32(hdr, pageData)
	binary.LittleEndian.PutUint32(hdr[22:26], crc)

	if _, err := w.Write(hdr); err != nil {
		return err
	}
	_, err := w.Write(pageData)
	return err
}

// oggCRC32 computes the Ogg CRC-32 checksum (polynomial 0x04C11DB7).
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

// goertzel computes the power at a specific frequency using the Goertzel algorithm.
// Returns power in linear scale (not dB).
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

// computeTHD computes Total Harmonic Distortion for a signal with known fundamental.
// THD = sqrt(sum of harmonic powers) / fundamental power, returned as percentage.
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

// TestWebRTCPipeline_TestToneQuality exercises the full audio pipeline:
//
//	PCM (24kHz) → resample to 48kHz → Opus encode → RTP packetize →
//	WebRTC transport (local loopback) → RTP depacketize → Opus decode → PCM (48kHz)
//
// Two local PeerConnections are connected via SDP exchange (no network).
// The sender uses the same RTP construction as WebRTCTransport.SendAudio.
// Quality metrics are computed on the received/decoded audio and logged.
//
// This test catches regressions in:
//   - Opus encoder output quality
//   - RTP packetization (sequence numbers, timestamps, marker bit)
//   - Sample rate handling in the encode path
//   - Packet delivery through pion's internal transport
func TestWebRTCPipeline_TestToneQuality(t *testing.T) {
	const (
		toneFreq       = 440.0
		toneSampleRate = 24000 // matches sendTestTone
		toneDuration   = 1     // seconds
		toneAmplitude  = 16000
		toneNumSamples = toneSampleRate * toneDuration
	)

	// Generate test tone (same as sendTestTone in realtime.go)
	pcm := make([]byte, toneNumSamples*2)
	for i := 0; i < toneNumSamples; i++ {
		sample := int16(toneAmplitude * math.Sin(2*math.Pi*toneFreq*float64(i)/float64(toneSampleRate)))
		binary.LittleEndian.PutUint16(pcm[i*2:], uint16(sample))
	}

	// Encode to Opus frames (same path as SendAudio)
	enc, err := NewOpusEncoder()
	if err != nil {
		t.Fatalf("NewOpusEncoder: %v", err)
	}
	defer enc.Close()

	opusFrames, err := enc.Encode(pcm, toneSampleRate)
	if err != nil {
		t.Fatalf("Encode: %v", err)
	}
	if len(opusFrames) == 0 {
		t.Fatal("no Opus frames produced")
	}
	t.Logf("Encoded %d Opus frames from %d PCM samples at %dHz", len(opusFrames), toneNumSamples, toneSampleRate)

	// --- Create sender PeerConnection ---
	senderME := &webrtc.MediaEngine{}
	if err := senderME.RegisterDefaultCodecs(); err != nil {
		t.Fatalf("sender RegisterDefaultCodecs: %v", err)
	}
	senderAPI := webrtc.NewAPI(webrtc.WithMediaEngine(senderME))
	senderPC, err := senderAPI.NewPeerConnection(webrtc.Configuration{})
	if err != nil {
		t.Fatalf("sender NewPeerConnection: %v", err)
	}
	defer senderPC.Close()

	audioTrack, err := webrtc.NewTrackLocalStaticRTP(
		webrtc.RTPCodecCapability{
			MimeType:  webrtc.MimeTypeOpus,
			ClockRate: 48000,
			Channels:  2,
		},
		"audio", "test",
	)
	if err != nil {
		t.Fatalf("NewTrackLocalStaticRTP: %v", err)
	}

	rtpSender, err := senderPC.AddTrack(audioTrack)
	if err != nil {
		t.Fatalf("AddTrack: %v", err)
	}
	// Drain RTCP
	go func() {
		buf := make([]byte, 1500)
		for {
			if _, _, err := rtpSender.Read(buf); err != nil {
				return
			}
		}
	}()

	// --- Create receiver PeerConnection ---
	receiverME := &webrtc.MediaEngine{}
	if err := receiverME.RegisterDefaultCodecs(); err != nil {
		t.Fatalf("receiver RegisterDefaultCodecs: %v", err)
	}
	receiverAPI := webrtc.NewAPI(webrtc.WithMediaEngine(receiverME))
	receiverPC, err := receiverAPI.NewPeerConnection(webrtc.Configuration{})
	if err != nil {
		t.Fatalf("receiver NewPeerConnection: %v", err)
	}
	defer receiverPC.Close()

	// Collect received RTP payloads (Opus frames)
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

	// --- Exchange SDP ---
	offer, err := senderPC.CreateOffer(nil)
	if err != nil {
		t.Fatalf("CreateOffer: %v", err)
	}
	if err := senderPC.SetLocalDescription(offer); err != nil {
		t.Fatalf("sender SetLocalDescription: %v", err)
	}
	senderGatherDone := webrtc.GatheringCompletePromise(senderPC)
	select {
	case <-senderGatherDone:
	case <-time.After(5 * time.Second):
		t.Fatal("sender ICE gathering timeout")
	}

	if err := receiverPC.SetRemoteDescription(*senderPC.LocalDescription()); err != nil {
		t.Fatalf("receiver SetRemoteDescription: %v", err)
	}
	answer, err := receiverPC.CreateAnswer(nil)
	if err != nil {
		t.Fatalf("CreateAnswer: %v", err)
	}
	if err := receiverPC.SetLocalDescription(answer); err != nil {
		t.Fatalf("receiver SetLocalDescription: %v", err)
	}
	receiverGatherDone := webrtc.GatheringCompletePromise(receiverPC)
	select {
	case <-receiverGatherDone:
	case <-time.After(5 * time.Second):
		t.Fatal("receiver ICE gathering timeout")
	}

	if err := senderPC.SetRemoteDescription(*receiverPC.LocalDescription()); err != nil {
		t.Fatalf("sender SetRemoteDescription: %v", err)
	}

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
	select {
	case <-connected:
	case <-time.After(5 * time.Second):
		t.Fatal("timeout waiting for WebRTC connection")
	}

	// --- Send test tone via RTP (same logic as SendAudio) ---
	const samplesPerFrame = 960
	seqNum := uint16(rand.UintN(65536))
	timestamp := rand.Uint32()
	marker := true

	ticker := time.NewTicker(20 * time.Millisecond)
	defer ticker.Stop()

	for i, frame := range opusFrames {
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

		if err := audioTrack.WriteRTP(pkt); err != nil {
			t.Fatalf("WriteRTP frame %d: %v", i, err)
		}
		if i < len(opusFrames)-1 {
			<-ticker.C
		}
	}

	// Wait for packets to arrive (give extra time for jitter buffer)
	time.Sleep(500 * time.Millisecond)

	// Close sender to trigger track end on receiver
	senderPC.Close()

	// Wait for track reader to finish (with timeout)
	select {
	case <-trackDone:
	case <-time.After(2 * time.Second):
		// Track reader may not exit cleanly on all platforms
	}

	// --- Decode received Opus frames ---
	receivedMu.Lock()
	pkts := make([]receivedPacket, len(receivedPackets))
	copy(pkts, receivedPackets)
	receivedMu.Unlock()

	if len(pkts) == 0 {
		t.Fatal("no RTP packets received")
	}

	dec, err := NewOpusDecoder()
	if err != nil {
		t.Fatalf("NewOpusDecoder: %v", err)
	}
	defer dec.Close()

	var allDecoded []int16
	decodeErrors := 0
	for _, pkt := range pkts {
		samples, err := dec.Decode(pkt.payload)
		if err != nil {
			decodeErrors++
			continue
		}
		allDecoded = append(allDecoded, samples...)
	}

	if len(allDecoded) == 0 {
		t.Fatal("no decoded samples")
	}

	// --- Analyse RTP packet delivery ---
	frameLoss := len(opusFrames) - len(pkts)
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

	t.Log("── RTP Delivery ──")
	t.Logf("  Frames sent:     %d", len(opusFrames))
	t.Logf("  Packets recv:    %d", len(pkts))
	t.Logf("  Frame loss:      %d", frameLoss)
	t.Logf("  Sequence gaps:   %d", seqGaps)
	t.Logf("  Marker packets:  %d (expect 1)", markerCount)
	t.Logf("  Decode errors:   %d", decodeErrors)

	// --- Audio quality metrics ---
	// Skip codec warmup (first 100ms at 48kHz = 4800 samples)
	skip := 48000 * 100 / 1000
	if skip > len(allDecoded)/2 {
		skip = len(allDecoded) / 4
	}
	tail := allDecoded[skip:]

	rms := computeRMS(tail)
	freq := estimateFrequency(tail, 48000)
	thd := computeTHD(tail, toneFreq, 48000, 10)

	t.Log("── Audio Quality ──")
	t.Logf("  Decoded samples: %d (%.1f ms at 48kHz)", len(allDecoded), float64(len(allDecoded))/48.0)
	t.Logf("  RMS level:       %.1f", rms)
	t.Logf("  Peak frequency:  %.0f Hz (expected %.0f Hz)", freq, toneFreq)
	t.Logf("  THD (h2-h10):    %.1f%%", thd)

	// --- Assertions ---
	if frameLoss > 0 {
		t.Errorf("lost %d frames in localhost transport", frameLoss)
	}
	if seqGaps > 0 {
		t.Errorf("detected %d sequence number gaps", seqGaps)
	}
	if markerCount != 1 {
		t.Errorf("expected exactly 1 marker packet (first packet), got %d", markerCount)
	}
	if rms < 50 {
		t.Errorf("RMS=%.1f is too low; signal appears silent or severely attenuated", rms)
	}
	freqDelta := math.Abs(freq - toneFreq)
	if freqDelta > 20 {
		t.Errorf("peak frequency %.0f Hz deviates from expected %.0f Hz by %.0f Hz", freq, toneFreq, freqDelta)
	}
	if thd > 50 {
		t.Errorf("THD=%.1f%% is too high; signal is severely distorted", thd)
	}

	// Log a summary line for quick scanning
	result := "PASS"
	issues := []string{}
	if frameLoss > 0 {
		issues = append(issues, fmt.Sprintf("%d frames lost", frameLoss))
	}
	if freqDelta > 20 {
		issues = append(issues, fmt.Sprintf("freq off by %.0f Hz", freqDelta))
	}
	if thd > 50 {
		issues = append(issues, fmt.Sprintf("THD %.1f%%", thd))
	}
	if rms < 50 {
		issues = append(issues, "silent")
	}
	if len(issues) > 0 {
		result = "FAIL: " + fmt.Sprintf("%v", issues)
	}
	t.Logf("── Summary: %s ──", result)
}
