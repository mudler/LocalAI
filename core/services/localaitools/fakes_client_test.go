package localaitools

import "context"

// The client methods of fakeClient only record the call and return an empty
// result: the dispatch tests care that the right method ran with the right
// arguments, the inference code has its own tests.

func (f *fakeClient) GenerateImage(_ context.Context, req GenerateImageRequest) (*ImageResult, error) {
	f.record("GenerateImage", req)
	return &ImageResult{}, nil
}

func (f *fakeClient) EditImage(_ context.Context, req EditImageRequest) (*ImageResult, error) {
	f.record("EditImage", req)
	return &ImageResult{}, nil
}

func (f *fakeClient) Speak(_ context.Context, req SpeakRequest) (*AudioResult, error) {
	f.record("Speak", req)
	return &AudioResult{}, nil
}

func (f *fakeClient) TranscribeAudio(_ context.Context, req TranscribeAudioRequest) (*TranscriptResult, error) {
	f.record("TranscribeAudio", req)
	return &TranscriptResult{}, nil
}

func (f *fakeClient) DescribeImage(_ context.Context, req DescribeImageRequest) (*DescribeImageResult, error) {
	f.record("DescribeImage", req)
	return &DescribeImageResult{}, nil
}

func (f *fakeClient) Embed(_ context.Context, req EmbedRequest) (*EmbedResult, error) {
	f.record("Embed", req)
	return &EmbedResult{}, nil
}

func (f *fakeClient) Detect(_ context.Context, req DetectRequest) (*DetectResult, error) {
	f.record("Detect", req)
	return &DetectResult{}, nil
}

func (f *fakeClient) Rerank(_ context.Context, req RerankRequest) (*RerankResult, error) {
	f.record("Rerank", req)
	return &RerankResult{}, nil
}

func (f *fakeClient) GenerateVideo(_ context.Context, req GenerateVideoRequest) (*VideoResult, error) {
	f.record("GenerateVideo", req)
	return &VideoResult{}, nil
}

func (f *fakeClient) GenerateSound(_ context.Context, req GenerateSoundRequest) (*AudioResult, error) {
	f.record("GenerateSound", req)
	return &AudioResult{}, nil
}

func (f *fakeClient) VerifyVoice(_ context.Context, req VoiceVerifyRequest) (*VerifyResult, error) {
	f.record("VerifyVoice", req)
	return &VerifyResult{}, nil
}

func (f *fakeClient) IdentifyVoice(_ context.Context, req IdentifyVoiceRequest) (*IdentifyResult, error) {
	f.record("IdentifyVoice", req)
	return &IdentifyResult{}, nil
}

func (f *fakeClient) VerifyFace(_ context.Context, req VerifyFaceRequest) (*VerifyResult, error) {
	f.record("VerifyFace", req)
	return &VerifyResult{}, nil
}

func (f *fakeClient) IdentifyFace(_ context.Context, req IdentifyFaceRequest) (*IdentifyResult, error) {
	f.record("IdentifyFace", req)
	return &IdentifyResult{}, nil
}
