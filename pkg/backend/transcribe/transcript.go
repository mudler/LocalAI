package transcribe

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"

	"github.com/ggerganov/whisper.cpp/bindings/go/pkg/whisper"
	"github.com/go-audio/wav"
	"github.com/go-skynet/LocalAI/api/schema"
)

func sh(c string) (string, error) {
	cmd := exec.Command("/bin/sh", "-c", c)
	cmd.Env = os.Environ()
	o, err := cmd.CombinedOutput()
	return string(o), err
}

// AudioToWav converts audio to wav for transcribe. It bashes out to ffmpeg
// TODO: use https://github.com/mccoyst/ogg?
func audioToWav(src, dst string) error {
	out, err := sh(fmt.Sprintf("ffmpeg -i %s -format s16le -ar 16000 -ac 1 -acodec pcm_s16le %s", src, dst))
	if err != nil {
		return fmt.Errorf("error: %w out: %s", err, out)
	}

	return nil
}

func Transcript(model whisper.Model, audiopath, language string, threads uint) (schema.Result, error) {
	res := schema.Result{}

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
