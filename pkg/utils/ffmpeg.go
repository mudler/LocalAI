package utils

import (
	"encoding/binary"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"

	laudio "github.com/mudler/LocalAI/pkg/audio"

	"github.com/go-audio/wav"
)

func ffmpegCommand(args []string) (string, error) {
	cmd := exec.Command("ffmpeg", args...) // Constrain this to ffmpeg to permit security scanner to see that the command is safe.
	cmd.Env = []string{}
	out, err := cmd.CombinedOutput()
	return string(out), err
}

// AudioToWav converts audio to wav for transcribe (16 kHz mono s16le).
// WAV files already in the target format are passed through directly;
// everything else is converted via ffmpeg.
//
// The pass-through uses a hardlink (with a Copy fallback for cross-fs
// src/dst) rather than Rename — callers may invoke this twice against
// the same fixture (e.g. once for AudioTranscription and once for
// AudioTranscriptionStream) and expect the original file to remain.
func AudioToWav(src, dst string) error {
	if strings.HasSuffix(src, ".wav") && isTargetWav(src) {
		return passthroughWAV(src, dst)
	}
	return convertWithFFmpeg(src, dst)
}

// AudioToWavPreservingShape converts audio to 16-bit PCM WAV while KEEPING the
// source sample rate and channel count. A WAV that is already 16-bit PCM is
// passed through byte for byte, whatever its rate or channel count.
//
// It is the counterpart to AudioToWav, which folds everything to 16 kHz mono.
// That fold is right for speech backends and destructive for the rest: source
// separation models refuse any rate but their checkpoint's own, and separate a
// centred vocal from a wide mix using the stereo image, so a 16 kHz mono
// downmix removes both the format they accept and the cue they work from.
// Callers pick between the two from the BACKEND'S declared need
// (config.AudioTransformRequiresMono16kInput), never from the file.
//
// Non-WAV uploads are still transcoded, since backends read WAV. Only the rate
// and the channel layout survive that.
func AudioToWavPreservingShape(src, dst string) error {
	if strings.HasSuffix(src, ".wav") && isPCM16Wav(src) {
		return passthroughWAV(src, dst)
	}
	return convertWithFFmpegPreservingShape(src, dst)
}

func passthroughWAV(src, dst string) error {
	if err := os.Link(src, dst); err == nil {
		return nil
	}
	// Fallback: copy. Hardlink fails across filesystems (e.g. src on a
	// read-only mount, dst in /tmp) or when the destination already
	// exists — both are fine; just copy bytes.
	in, err := os.Open(src)
	if err != nil {
		return fmt.Errorf("open src: %w", err)
	}
	defer in.Close()
	out, err := os.Create(dst)
	if err != nil {
		return fmt.Errorf("create dst: %w", err)
	}
	defer out.Close()
	if _, err := io.Copy(out, in); err != nil {
		return fmt.Errorf("copy: %w", err)
	}
	return nil
}

// isTargetWav returns true when src is a valid WAV already in the
// target format (16 kHz, mono, 16-bit PCM).
func isTargetWav(src string) bool {
	f, err := os.Open(src)
	if err != nil {
		return false
	}
	defer f.Close()

	dec := wav.NewDecoder(f)
	if !dec.IsValidFile() {
		return false
	}
	return dec.BitDepth == 16 && dec.NumChans == 1 && dec.SampleRate == 16000
}

func convertWithFFmpeg(src, dst string) error {
	commandArgs := []string{"-i", src, "-format", "s16le", "-ar", "16000", "-ac", "1", "-acodec", "pcm_s16le", dst}
	out, err := ffmpegCommand(commandArgs)
	if err != nil {
		return fmt.Errorf("error: %w out: %s", err, out)
	}
	return nil
}

// isPCM16Wav returns true when src is a valid WAV carrying PLAIN 16-bit PCM, at
// ANY sample rate and ANY channel count. Weaker than isTargetWav on rate and
// channels; exactly as strict on the encoding, and deliberately so.
//
// The format tag is checked and that is not a formality. go-audio's
// IsValidFile() never inspects it, so a BitDepth == 16 test on its own also
// accepts WAVE_FORMAT_EXTENSIBLE (0xFFFE), which is what many DAWs and Windows
// tools write. Consumers are not as relaxed: audio.cpp's WAV reader accepts
// 16-bit only when the tag is 1 and otherwise refuses with "unsupported WAV
// encoding". Passing such a file through unchanged would trade a transcode for
// a failed request, and music files, which are the input this passthrough
// exists for, are exactly where extensible turns up.
func isPCM16Wav(src string) bool {
	f, err := os.Open(src)
	if err != nil {
		return false
	}
	defer func() { _ = f.Close() }()

	dec := wav.NewDecoder(f)
	if !dec.IsValidFile() {
		return false
	}
	return dec.BitDepth == 16 && dec.WavAudioFormat == 1
}

// convertWithFFmpegPreservingShape transcodes to 16-bit PCM WAV with no -ar and
// no -ac, so ffmpeg keeps the source rate and channel count. Dropping those two
// flags is the whole difference from convertWithFFmpeg.
func convertWithFFmpegPreservingShape(src, dst string) error {
	commandArgs := []string{"-y", "-i", src, "-acodec", "pcm_s16le", dst}
	out, err := ffmpegCommand(commandArgs)
	if err != nil {
		return fmt.Errorf("error: %w out: %s", err, out)
	}
	return nil
}

// AudioResample resamples an audio file to the given sample rate using ffmpeg.
// If sampleRate <= 0, it is a no-op and returns src unchanged.
func AudioResample(src string, sampleRate int) (string, error) {
	if sampleRate <= 0 {
		return src, nil
	}
	dst := strings.Replace(src, ".wav", fmt.Sprintf("_%dhz.wav", sampleRate), 1)
	commandArgs := []string{"-y", "-i", src, "-ar", fmt.Sprintf("%d", sampleRate), dst}
	out, err := ffmpegCommand(commandArgs)
	if err != nil {
		return "", fmt.Errorf("error resampling audio: %w out: %s", err, out)
	}
	// A successful exit is not enough. For a rate its resampler collapses to
	// nothing (-ar 1 on a one second clip is the reproducer) ffmpeg writes a
	// bare 78-byte header, no audio at all, and exits 0 - and every caller here
	// hands that straight back to the client as the audio it asked for. A 200
	// carrying no audio is the silent wrong answer; callers already handle an
	// error from this function.
	info, statErr := os.Stat(dst)
	if statErr != nil {
		return "", fmt.Errorf("error resampling audio: no output at %s: %w out: %s", dst, statErr, out)
	}
	if info.Size() == 0 {
		return "", fmt.Errorf("error resampling audio: ffmpeg produced an empty file at %d Hz out: %s", sampleRate, out)
	}
	if wavAudioBytes(dst) == 0 {
		return "", fmt.Errorf("error resampling audio: ffmpeg produced a WAV with no audio at %d Hz out: %s", sampleRate, out)
	}
	return dst, nil
}

// wavAudioBytes reports how many bytes of audio a RIFF/WAVE file actually
// carries on disk. Returns -1 for anything it cannot walk as RIFF/WAVE, so a
// caller can tell "no audio" apart from "not a file I can judge".
//
// The declared data-chunk size is deliberately clamped to the bytes that are
// really present, and that clamp is the entire point. When ffmpeg's resampler
// collapses a signal to nothing it still writes a header CLAIMING 70 data
// bytes and then writes none of them, so the file is 78 bytes of header and
// go-audio's decoder reports a 35 second duration for it: both the declared
// size and the parsed duration say "audio", and only the file length says the
// truth. A plain size check does not work either, since the file is not empty.
func wavAudioBytes(path string) int64 {
	f, err := os.Open(path)
	if err != nil {
		return -1
	}
	defer func() { _ = f.Close() }()

	info, err := f.Stat()
	if err != nil {
		return -1
	}
	var riffHeader [12]byte
	if _, err := io.ReadFull(f, riffHeader[:]); err != nil {
		return -1
	}
	if string(riffHeader[0:4]) != "RIFF" || string(riffHeader[8:12]) != "WAVE" {
		return -1
	}

	offset := int64(len(riffHeader))
	var chunkHeader [8]byte
	for {
		if _, err := f.ReadAt(chunkHeader[:], offset); err != nil {
			// Ran off the end without meeting a data chunk: no audio.
			return 0
		}
		declared := int64(binary.LittleEndian.Uint32(chunkHeader[4:8]))
		payload := offset + int64(len(chunkHeader))
		if string(chunkHeader[0:4]) == "data" {
			available := info.Size() - payload
			if available < 0 {
				available = 0
			}
			if declared < available {
				return declared
			}
			return available
		}
		offset = payload + declared
		// RIFF chunks are word aligned; an odd-sized one is followed by a pad
		// byte that is not part of the next chunk's header.
		if declared%2 == 1 {
			offset++
		}
	}
}

// AudioConvert converts generated wav file from tts to other output formats.
// TODO: handle pcm to have 100% parity of supported format from OpenAI
func AudioConvert(src string, format string) (string, error) {
	extension := ""
	// compute file extension from format, default to wav
	switch format {
	case "opus":
		extension = ".ogg"
	case "mp3", "aac", "flac":
		extension = fmt.Sprintf(".%s", format)
	default:
		extension = ".wav"
	}

	// if .wav, do nothing
	if extension == ".wav" {
		return src, nil
	}

	// naive conversion based on default values and target extension of file
	dst := strings.Replace(src, ".wav", extension, -1)
	commandArgs := []string{"-y", "-i", src, "-vn", dst}
	out, err := ffmpegCommand(commandArgs)
	if err != nil {
		return "", fmt.Errorf("error: %w out: %s", err, out)
	}
	return dst, nil
}

// WriteWav16kFromReader reads all PCM data from r and writes a 16 kHz mono
// 16-bit WAV to w. Useful when the PCM length is not known in advance.
func WriteWav16kFromReader(w io.Writer, r io.Reader) error {
	pcm, err := io.ReadAll(r)
	if err != nil {
		return fmt.Errorf("read pcm: %w", err)
	}
	hdr := laudio.NewWAVHeader(uint32(len(pcm)))
	if err := hdr.Write(w); err != nil {
		return fmt.Errorf("write wav header: %w", err)
	}
	_, err = w.Write(pcm)
	return err
}
