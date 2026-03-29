package trace

import (
	"bytes"
	"encoding/base64"
	"math"
	"os"

	"github.com/mudler/LocalAI/pkg/audio"
	"github.com/mudler/LocalAI/pkg/sound"
	"github.com/mudler/xlog"
)

// MaxSnippetSeconds is the maximum number of seconds of audio captured per trace.
const MaxSnippetSeconds = 30

// AudioSnippet captures the first MaxSnippetSeconds of a WAV file and computes
// quality metrics. The result is a map suitable for merging into a BackendTrace
// Data field.
func AudioSnippet(wavPath string) map[string]any {
	raw, err := os.ReadFile(wavPath)
	if err != nil {
		xlog.Warn("audio snippet: read failed", "path", wavPath, "error", err)
		return nil
	}
	// Only process WAV files (RIFF header)
	if len(raw) <= audio.WAVHeaderSize || string(raw[:4]) != "RIFF" {
		xlog.Debug("audio snippet: not a WAV file or too small", "path", wavPath, "bytes", len(raw))
		return nil
	}

	pcm, sampleRate := audio.ParseWAV(raw)
	if sampleRate == 0 {
		sampleRate = 16000
	}

	return AudioSnippetFromPCM(pcm, sampleRate, len(pcm))
}

// AudioSnippetFromPCM builds an audio snippet from raw PCM bytes (int16 LE mono).
// totalPCMBytes is the full audio size before truncation (used to compute total duration).
func AudioSnippetFromPCM(pcm []byte, sampleRate int, totalPCMBytes int) map[string]any {
	if len(pcm) == 0 || len(pcm)%2 != 0 {
		return nil
	}

	samples := sound.BytesToInt16sLE(pcm)
	totalSamples := totalPCMBytes / 2
	durationS := float64(totalSamples) / float64(sampleRate)

	// Truncate to first MaxSnippetSeconds
	maxSamples := MaxSnippetSeconds * sampleRate
	if len(samples) > maxSamples {
		samples = samples[:maxSamples]
	}

	snippetDuration := float64(len(samples)) / float64(sampleRate)

	rms := sound.CalculateRMS16(samples)
	rmsDBFS := -math.Inf(1)
	if rms > 0 {
		rmsDBFS = 20 * math.Log10(rms/32768.0)
	}

	var peak int16
	var dcSum int64
	for _, s := range samples {
		if s < 0 && -s > peak {
			peak = -s
		} else if s > peak {
			peak = s
		}
		dcSum += int64(s)
	}
	peakDBFS := -math.Inf(1)
	if peak > 0 {
		peakDBFS = 20 * math.Log10(float64(peak)/32768.0)
	}
	dcOffset := float64(dcSum) / float64(len(samples)) / 32768.0

	// Encode the snippet as WAV
	snippetPCM := sound.Int16toBytesLE(samples)
	hdr := audio.NewWAVHeaderWithRate(uint32(len(snippetPCM)), uint32(sampleRate))
	var buf bytes.Buffer
	buf.Grow(audio.WAVHeaderSize + len(snippetPCM))
	if err := hdr.Write(&buf); err != nil {
		xlog.Warn("audio snippet: write header failed", "error", err)
		return nil
	}
	buf.Write(snippetPCM)

	return map[string]any{
		"audio_wav_base64":  base64.StdEncoding.EncodeToString(buf.Bytes()),
		"audio_duration_s":  math.Round(durationS*100) / 100,
		"audio_snippet_s":   math.Round(snippetDuration*100) / 100,
		"audio_sample_rate": sampleRate,
		"audio_samples":     totalSamples,
		"audio_rms_dbfs":    math.Round(rmsDBFS*10) / 10,
		"audio_peak_dbfs":   math.Round(peakDBFS*10) / 10,
		"audio_dc_offset":   math.Round(dcOffset*10000) / 10000,
	}
}
