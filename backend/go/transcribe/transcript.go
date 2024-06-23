package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"

	"github.com/ggerganov/whisper.cpp/bindings/go/pkg/whisper"
	"github.com/go-audio/wav"
	"github.com/mudler/LocalAI/core/schema"
)

func ffmpegCommand(args []string) (string, error) {
	cmd := exec.Command("ffmpeg", args...) // Constrain this to ffmpeg to permit security scanner to see that the command is safe.
	cmd.Env = os.Environ()
	out, err := cmd.CombinedOutput()
	return string(out), err
}

// AudioToWav converts audio to wav for transcribe.
// TODO: use https://github.com/mccoyst/ogg?
func audioToWav(src, dst string) error {
	commandArgs := []string{"-i", src, "-format", "s16le", "-ar", "16000", "-ac", "1", "-acodec", "pcm_s16le", dst}
	out, err := ffmpegCommand(commandArgs)
	if err != nil {
		return fmt.Errorf("error: %w out: %s", err, out)
	}
	return nil
}

func Transcript(model whisper.Model, audiopath, language string, threads uint) (schema.TranscriptionResult, error) {
	res := schema.TranscriptionResult{}

	dir, err := os.MkdirTemp("", "whisper")
	if err != nil {
		return res, err
	}
	defer os.RemoveAll(dir)

	convertedPath := filepath.Join(dir, "converted.wav")

	if err := audioToWav(audiopath, convertedPath); err != nil {
		return res, err
	}

	// Open samples
	fh, err := os.Open(convertedPath)
	if err != nil {
		return res, err
	}
	defer fh.Close()

	// Read samples
	d := wav.NewDecoder(fh)
	buf, err := d.FullPCMBuffer()
	if err != nil {
		return res, err
	}

	data := buf.AsFloat32Buffer().Data

	// Process samples
	context, err := model.NewContext()
	if err != nil {
		return res, err

	}

	context.SetThreads(threads)

	if language != "" {
		context.SetLanguage(language)
	} else {
		context.SetLanguage("auto")
	}

	if err := context.Process(data, nil, nil); err != nil {
		return res, err
	}

	for {
		s, err := context.NextSegment()
		if err != nil {
			break
		}

		var tokens []int
		for _, t := range s.Tokens {
			tokens = append(tokens, t.Id)
		}

		segment := schema.Segment{Id: s.Num, Text: s.Text, Start: s.Start, End: s.End, Tokens: tokens}
		res.Segments = append(res.Segments, segment)

		res.Text += s.Text
	}

	return res, nil
}
