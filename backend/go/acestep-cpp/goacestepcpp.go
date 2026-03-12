package main

import (
	"fmt"
	"os"
	"strings"

	"github.com/mudler/LocalAI/pkg/grpc/base"
	pb "github.com/mudler/LocalAI/pkg/grpc/proto"
)

var (
	CppLoadModel    func(lmModelPath, textEncoderPath, ditModelPath, vaeModelPath string) int
	CppGenerateMusic func(caption, lyrics string, bpm int, keyscale, timesignature string, duration, temperature float32, instrumental bool, seed int, dst string, threads int) int
)

type AceStepCpp struct {
	base.SingleThread
}

func (a *AceStepCpp) Load(opts *pb.ModelOptions) error {
	// ModelFile is the LM model path
	lmModel := opts.ModelFile

	var textEncoderModel, ditModel, vaeModel string

	for _, oo := range opts.Options {
		parts := strings.SplitN(oo, ":", 2)
		if len(parts) != 2 {
			fmt.Fprintf(os.Stderr, "Unrecognized option: %v\n", oo)
			continue
		}
		switch parts[0] {
		case "text_encoder_model":
			textEncoderModel = parts[1]
		case "dit_model":
			ditModel = parts[1]
		case "vae_model":
			vaeModel = parts[1]
		default:
			fmt.Fprintf(os.Stderr, "Unrecognized option: %v\n", oo)
		}
	}

	if textEncoderModel == "" {
		return fmt.Errorf("text_encoder_model option is required")
	}
	if ditModel == "" {
		return fmt.Errorf("dit_model option is required")
	}
	if vaeModel == "" {
		return fmt.Errorf("vae_model option is required")
	}

	if ret := CppLoadModel(lmModel, textEncoderModel, ditModel, vaeModel); ret != 0 {
		return fmt.Errorf("failed to load acestep models (error code: %d)", ret)
	}

	return nil
}

func (a *AceStepCpp) SoundGeneration(req *pb.SoundGenerationRequest) error {
	caption := req.GetCaption()
	if caption == "" {
		caption = req.GetText()
	}
	lyrics := req.GetLyrics()
	bpm := int(req.GetBpm())
	keyscale := req.GetKeyscale()
	timesignature := req.GetTimesignature()
	duration := req.GetDuration()
	temperature := req.GetTemperature()
	instrumental := req.GetInstrumental()
	seed := 42
	threads := 4

	if ret := CppGenerateMusic(caption, lyrics, bpm, keyscale, timesignature, duration, temperature, instrumental, seed, req.GetDst(), threads); ret != 0 {
		return fmt.Errorf("failed to generate music (error code: %d)", ret)
	}

	return nil
}
