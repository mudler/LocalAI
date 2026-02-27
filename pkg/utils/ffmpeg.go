package utils

import (
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
func AudioToWav(src, dst string) error {
	if strings.HasSuffix(src, ".wav") && isTargetWav(src) {
		return os.Rename(src, dst)
	}
	return convertWithFFmpeg(src, dst)
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
	return dst, nil
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
