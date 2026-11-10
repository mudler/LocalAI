package mcp

import (
	"context"

	localaitools "github.com/mudler/LocalAI/core/services/localaitools"
)

// The client methods of stubClient: the holder tests do not run inference.

func (stubClient) GenerateImage(context.Context, localaitools.GenerateImageRequest) (*localaitools.ImageResult, error) {
	return &localaitools.ImageResult{}, nil
}

func (stubClient) EditImage(context.Context, localaitools.EditImageRequest) (*localaitools.ImageResult, error) {
	return &localaitools.ImageResult{}, nil
}

func (stubClient) Speak(context.Context, localaitools.SpeakRequest) (*localaitools.AudioResult, error) {
	return &localaitools.AudioResult{}, nil
}

func (stubClient) TranscribeAudio(context.Context, localaitools.TranscribeAudioRequest) (*localaitools.TranscriptResult, error) {
	return &localaitools.TranscriptResult{}, nil
}

func (stubClient) DescribeImage(context.Context, localaitools.DescribeImageRequest) (*localaitools.DescribeImageResult, error) {
	return &localaitools.DescribeImageResult{}, nil
}

func (stubClient) Embed(context.Context, localaitools.EmbedRequest) (*localaitools.EmbedResult, error) {
	return &localaitools.EmbedResult{}, nil
}

func (stubClient) Detect(context.Context, localaitools.DetectRequest) (*localaitools.DetectResult, error) {
	return &localaitools.DetectResult{}, nil
}

func (stubClient) Rerank(context.Context, localaitools.RerankRequest) (*localaitools.RerankResult, error) {
	return &localaitools.RerankResult{}, nil
}

func (stubClient) GenerateVideo(context.Context, localaitools.GenerateVideoRequest) (*localaitools.VideoResult, error) {
	return &localaitools.VideoResult{}, nil
}

func (stubClient) GenerateSound(context.Context, localaitools.GenerateSoundRequest) (*localaitools.AudioResult, error) {
	return &localaitools.AudioResult{}, nil
}

func (stubClient) UpscaleImage(context.Context, localaitools.UpscaleImageRequest) (*localaitools.ImageResult, error) {
	return &localaitools.ImageResult{}, nil
}

func (stubClient) TransformAudio(context.Context, localaitools.TransformAudioRequest) (*localaitools.TransformAudioResult, error) {
	return &localaitools.TransformAudioResult{}, nil
}

func (stubClient) VerifyVoice(context.Context, localaitools.VoiceVerifyRequest) (*localaitools.VerifyResult, error) {
	return &localaitools.VerifyResult{}, nil
}

func (stubClient) IdentifyVoice(context.Context, localaitools.IdentifyVoiceRequest) (*localaitools.IdentifyResult, error) {
	return &localaitools.IdentifyResult{}, nil
}

func (stubClient) VerifyFace(context.Context, localaitools.VerifyFaceRequest) (*localaitools.VerifyResult, error) {
	return &localaitools.VerifyResult{}, nil
}

func (stubClient) IdentifyFace(context.Context, localaitools.IdentifyFaceRequest) (*localaitools.IdentifyResult, error) {
	return &localaitools.IdentifyResult{}, nil
}
