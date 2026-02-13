package utils

import (
	"encoding/binary"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"

	laudio "github.com/mudler/LocalAI/pkg/audio"
	"github.com/mudler/LocalAI/pkg/sound"

	"github.com/go-audio/audio"
	"github.com/go-audio/wav"
)

func ffmpegCommand(args []string) (string, error) {
	cmd := exec.Command("ffmpeg", args...) // Constrain this to ffmpeg to permit security scanner to see that the command is safe.
	cmd.Env = []string{}
	out, err := cmd.CombinedOutput()
	return string(out), err
}

// AudioToWav converts audio to wav for transcribe.
// WAV files are converted in pure Go (resample + downmix to 16 kHz mono s16le).
// Non-WAV files fall back to ffmpeg.
func AudioToWav(src, dst string) error {
	if strings.HasSuffix(src, ".wav") {
		return convertWav(src, dst)
	}
	return convertWithFFmpeg(src, dst)
}

// convertWav reads a WAV file and writes a 16 kHz mono 16-bit PCM WAV to dst.
// If the source already matches those parameters the file is simply renamed.
func convertWav(src, dst string) error {
	f, err := os.Open(src)
	if err != nil {
		return fmt.Errorf("open: %w", err)
	}
	defer f.Close()

	dec := wav.NewDecoder(f)
	if !dec.IsValidFile() {
		return fmt.Errorf("invalid wav file: %s", src)
	}

	if dec.BitDepth == 16 && dec.NumChans == 1 && dec.SampleRate == 16000 {
		f.Close()
		return os.Rename(src, dst)
	}

	buf, err := dec.FullPCMBuffer()
	if err != nil {
		return fmt.Errorf("decode wav: %w", err)
	}

	samples := toInt16(buf)

	if dec.NumChans > 1 {
		samples = downmixToMono(samples, int(dec.NumChans))
	}

	if dec.SampleRate != 16000 {
		samples = sound.ResampleInt16(samples, int(dec.SampleRate), 16000)
	}

	return writeWav16k(dst, samples)
}

// toInt16 converts a go-audio IntBuffer (arbitrary bit depth) to int16 samples.
func toInt16(buf *audio.IntBuffer) []int16 {
	depth := buf.SourceBitDepth
	out := make([]int16, len(buf.Data))
	switch {
	case depth == 16:
		for i, v := range buf.Data {
			out[i] = int16(v)
		}
	case depth > 16:
		shift := uint(depth - 16)
		for i, v := range buf.Data {
			out[i] = int16(v >> shift)
		}
	default:
		shift := uint(16 - depth)
		for i, v := range buf.Data {
			out[i] = int16(v << shift)
		}
	}
	return out
}

func downmixToMono(samples []int16, channels int) []int16 {
	n := len(samples) / channels
	mono := make([]int16, n)
	for i := 0; i < n; i++ {
		var sum int32
		for ch := 0; ch < channels; ch++ {
			sum += int32(samples[i*channels+ch])
		}
		mono[i] = int16(sum / int32(channels))
	}
	return mono
}

func writeWav16k(dst string, samples []int16) error {
	out, err := os.Create(dst)
	if err != nil {
		return fmt.Errorf("create: %w", err)
	}
	defer out.Close()

	pcmBytes := len(samples) * 2
	hdr := laudio.NewWAVHeader(uint32(pcmBytes), 16000)
	if err := hdr.Write(out); err != nil {
		return fmt.Errorf("write wav header: %w", err)
	}

	if err := binary.Write(out, binary.LittleEndian, samples); err != nil {
		return fmt.Errorf("write wav data: %w", err)
	}

	return nil
}

func convertWithFFmpeg(src, dst string) error {
	commandArgs := []string{"-i", src, "-format", "s16le", "-ar", "16000", "-ac", "1", "-acodec", "pcm_s16le", dst}
	out, err := ffmpegCommand(commandArgs)
	if err != nil {
		return fmt.Errorf("error: %w out: %s", err, out)
	}
	return nil
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
	hdr := laudio.NewWAVHeader(uint32(len(pcm)), 16000)
	if err := hdr.Write(w); err != nil {
		return fmt.Errorf("write wav header: %w", err)
	}
	_, err = w.Write(pcm)
	return err
}
