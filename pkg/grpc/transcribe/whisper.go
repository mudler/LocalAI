package transcribe

// This is a wrapper to statisfy the GRPC service interface
// It is meant to be used by the main executable that is the server for the specific backend type (falcon, gpt3, etc)
import (
	"github.com/ggerganov/whisper.cpp/bindings/go/pkg/whisper"
	"github.com/go-skynet/LocalAI/pkg/grpc/base"
	pb "github.com/go-skynet/LocalAI/pkg/grpc/proto"
	whisperutil "github.com/go-skynet/LocalAI/pkg/grpc/whisper"
	"github.com/go-skynet/LocalAI/pkg/grpc/whisper/api"
)

type Whisper struct {
	base.Base
	whisper whisper.Model
}

func (sd *Whisper) Load(opts *pb.ModelOptions) error {
	// Note: the Model here is a path to a directory containing the model files
	w, err := whisper.New(opts.Model)
	sd.whisper = w
	return err
}

func (sd *Whisper) AudioTranscription(opts *pb.TranscriptRequest) (api.Result, error) {
	return whisperutil.Transcript(sd.whisper, opts.Dst, opts.Language, uint(opts.Threads))
}
