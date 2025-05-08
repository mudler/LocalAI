package main

// This is a wrapper to statisfy the GRPC service interface
// It is meant to be used by the main executable that is the server for the specific backend type (falcon, gpt3, etc)
import (
	"os"
	"path/filepath"

	"github.com/ggerganov/whisper.cpp/bindings/go/pkg/whisper"
	"github.com/go-audio/wav"
	"github.com/mudler/LocalAI/pkg/grpc/base"
	pb "github.com/mudler/LocalAI/pkg/grpc/proto"
	"github.com/mudler/LocalAI/pkg/utils"
)

type Whisper struct {
	base.SingleThread
	whisper whisper.Model
}

func (sd *Whisper) Load(opts *pb.ModelOptions) error {
	// Note: the Model here is a path to a directory containing the model files
	w, err := whisper.New(opts.ModelFile)
	sd.whisper = w
	return err
}

func (sd *Whisper) AudioTranscription(opts *pb.TranscriptRequest) (pb.TranscriptResult, error) {

	dir, err := os.MkdirTemp("", "whisper")
	if err != nil {
		return pb.TranscriptResult{}, err
	}
	defer os.RemoveAll(dir)

	convertedPath := filepath.Join(dir, "converted.wav")

	if err := utils.AudioToWav(opts.Dst, convertedPath); err != nil {
		return pb.TranscriptResult{}, err
	}

	// Open samples
	fh, err := os.Open(convertedPath)
	if err != nil {
		return pb.TranscriptResult{}, err
	}
	defer fh.Close()

	// Read samples
	d := wav.NewDecoder(fh)
	buf, err := d.FullPCMBuffer()
	if err != nil {
		return pb.TranscriptResult{}, err
	}

	data := buf.AsFloat32Buffer().Data

	// Process samples
	context, err := sd.whisper.NewContext()
	if err != nil {
		return pb.TranscriptResult{}, err

	}

	context.SetThreads(uint(opts.Threads))

	if opts.Language != "" {
		context.SetLanguage(opts.Language)
	} else {
		context.SetLanguage("auto")
	}

	if opts.Translate {
		context.SetTranslate(true)
	}

	if err := context.Process(data, nil, nil, nil); err != nil {
		return pb.TranscriptResult{}, err
	}

	segments := []*pb.TranscriptSegment{}
	text := ""
	for {
		s, err := context.NextSegment()
		if err != nil {
			break
		}

		var tokens []int32
		for _, t := range s.Tokens {
			tokens = append(tokens, int32(t.Id))
		}

		segment := &pb.TranscriptSegment{Id: int32(s.Num), Text: s.Text, Start: int64(s.Start), End: int64(s.End), Tokens: tokens}
		segments = append(segments, segment)

		text += s.Text
	}

	return pb.TranscriptResult{
		Segments: segments,
		Text:     text,
	}, nil

}
