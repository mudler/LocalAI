package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	pb "github.com/mudler/LocalAI/pkg/grpc/proto"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"google.golang.org/grpc/metadata"
)

func TestMockBackendFixtures(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Mock backend fixture contract suite")
}

func failFixture(format string, args ...any) {
	GinkgoHelper()
	Fail(fmt.Sprintf(format, args...))
}

var (
	wantPNG   = mustHex("89504e470d0a1a0a0000000d49484452000000010000000108060000001f15c4890000000d4944415408d763f8cfc0f01f00050001ff89993d1d0000000049454e44ae426082")
	wantVideo = []byte("\x00\x00\x00\x18ftypisomMOCK-VIDEO")
	wantGLB   = mustHex("676c5446020000000c000000")
)

func mustHex(value string) []byte {
	b, err := hex.DecodeString(value)
	if err != nil {
		panic(err)
	}
	return b
}

var _ = Describe("Mock backend fixture contracts", func() {
	It("FixtureOutputRPCsWriteExactBytes", func() {

		backend := &MockBackend{}
		tests := []struct {
			name string
			file string
			want []byte
			call func(string) (*pb.Result, error)
		}{
			{
				name: "image", file: "image.png", want: wantPNG,
				call: func(dst string) (*pb.Result, error) {
					return backend.GenerateImage(context.Background(), &pb.GenerateImageRequest{Dst: dst})
				},
			},
			{
				name: "video", file: "video.mp4", want: wantVideo,
				call: func(dst string) (*pb.Result, error) {
					return backend.GenerateVideo(context.Background(), &pb.GenerateVideoRequest{Dst: dst})
				},
			},
			{
				name: "3d", file: "asset.glb", want: wantGLB,
				call: func(dst string) (*pb.Result, error) {
					return backend.Generate3D(context.Background(), &pb.Generate3DRequest{Dst: dst})
				},
			},
			{
				name: "3d-animation", file: "animation.glb", want: wantGLB,
				call: func(dst string) (*pb.Result, error) {
					return backend.Animate3D(context.Background(), &pb.Animate3DRequest{Dst: dst})
				},
			},
		}

		for _, tc := range tests {
			By(tc.name)
			dst := filepath.Join(GinkgoT().TempDir(), "nested", tc.file)
			result, err := tc.call(dst)
			if err != nil {
				failFixture("RPC returned error: %v", err)
			}
			if result == nil || !result.Success {
				failFixture("RPC did not report success: %#v", result)
			}
			got, err := os.ReadFile(dst)
			if err != nil {
				failFixture("read output: %v", err)
			}
			if !bytes.Equal(got, tc.want) {
				failFixture("output bytes differ\n got: %x\nwant: %x", got, tc.want)
			}
		}
	})

	It("FixtureArtifactsCarryStagedInputDigest", func() {
		backend := &MockBackend{}
		dir := GinkgoT().TempDir()
		input := filepath.Join(dir, "input.bin")
		if err := os.WriteFile(input, []byte("frontend-origin"), 0600); err != nil {
			failFixture("%v", err)
		}
		const digest = "sha256:60aea919cd84c509d660e2b8dabd65996fdebdfa25b3be85cf32501c24e22a75"

		tests := []struct {
			name   string
			base   []byte
			marker string
			call   func(string) (*pb.Result, error)
		}{
			{"image", wantPNG, "src", func(dst string) (*pb.Result, error) {
				return backend.GenerateImage(context.Background(), &pb.GenerateImageRequest{Src: input, Dst: dst})
			}},
			{"video", wantVideo, "start_image", func(dst string) (*pb.Result, error) {
				return backend.GenerateVideo(context.Background(), &pb.GenerateVideoRequest{StartImage: input, Dst: dst})
			}},
			{"3d", wantGLB, "src", func(dst string) (*pb.Result, error) {
				return backend.Generate3D(context.Background(), &pb.Generate3DRequest{Src: input, Dst: dst})
			}},
			{"3d-animation", wantGLB, "mesh", func(dst string) (*pb.Result, error) {
				return backend.Animate3D(context.Background(), &pb.Animate3DRequest{Inputs: map[string]*pb.AnimationInput{
					"mesh": {Type: "mesh", Data: input},
				}, Dst: dst})
			}},
			{"upscale", wantPNG, "src", func(dst string) (*pb.Result, error) {
				return backend.UpscaleImage(context.Background(), &pb.UpscaleImageRequest{Src: input, Dst: dst, Scale: 2})
			}},
		}
		for _, tc := range tests {
			By(tc.name)
			dst := filepath.Join(dir, tc.name+".out")
			result, err := tc.call(dst)
			if err != nil || result == nil || !result.Success {
				failFixture("RPC failed: result=%#v err=%v", result, err)
			}
			got, err := os.ReadFile(dst)
			if err != nil {
				failFixture("%v", err)
			}
			want := fixtureArtifact(tc.base, tc.marker+"="+digest)
			if !bytes.Equal(got, want) {
				failFixture("artifact mismatch: got %q want %q", got, want)
			}
			corrupt := append([]byte(nil), want...)
			corrupt[len(corrupt)-1] ^= 1
			if bytes.Equal(got, corrupt) {
				failFixture("exact comparison accepted a corrupt fixture")
			}
		}
	})

	It("FixtureImageCarriesNegativePromptDigest", func() {
		backend := &MockBackend{}
		dst := filepath.Join(GinkgoT().TempDir(), "image.png")
		result, err := backend.GenerateImage(context.Background(), &pb.GenerateImageRequest{
			NegativePrompt: "blurry",
			Dst:            dst,
		})
		Expect(err).ToNot(HaveOccurred())
		Expect(result.GetSuccess()).To(BeTrue(), result.GetMessage())

		got, err := os.ReadFile(dst)
		Expect(err).ToNot(HaveOccurred())
		Expect(got).To(Equal(fixtureArtifact(wantPNG,
			fmt.Sprintf("negative_prompt=inline-sha256:%x", sha256.Sum256([]byte("blurry"))))))
	})

	It("ExtendedConformanceRPCFixtures", func() {
		backend := &MockBackend{}
		ctx := context.Background()
		depth, err := backend.Depth(ctx, &pb.DepthRequest{})
		if err != nil || depth.GetWidth() != 2 || depth.GetHeight() != 1 || !depth.GetIsMetric() {
			failFixture("unexpected Depth fixture: %#v err=%v", depth, err)
		}
		face, err := backend.FaceAnalyze(ctx, &pb.FaceAnalyzeRequest{})
		if err != nil || len(face.GetFaces()) != 1 || face.GetFaces()[0].GetDominantGender() != "Woman" {
			failFixture("unexpected FaceAnalyze fixture: %#v err=%v", face, err)
		}
		voice, err := backend.VoiceAnalyze(ctx, &pb.VoiceAnalyzeRequest{})
		if err != nil || len(voice.GetSegments()) != 1 || voice.GetSegments()[0].GetDominantEmotion() != "neutral" {
			failFixture("unexpected VoiceAnalyze fixture: %#v err=%v", voice, err)
		}
		classified, err := backend.TokenClassify(ctx, &pb.TokenClassifyRequest{Text: "Alice visited Rome"})
		if err != nil || len(classified.GetEntities()) != 1 || classified.GetEntities()[0].GetText() != "Alice" {
			failFixture("unexpected TokenClassify fixture: %#v err=%v", classified, err)
		}
	})

	It("UnaryAudioFixturesHonorConfiguredSampleRate", func() {
		GinkgoT().Setenv("MOCK_TTS_SAMPLE_RATE", "22050")
		backend := &MockBackend{}
		for _, tc := range []struct {
			name string
			call func(string) (*pb.Result, error)
		}{
			{"TTS", func(dst string) (*pb.Result, error) {
				return backend.TTS(context.Background(), &pb.TTSRequest{Dst: dst})
			}},
			{"SoundGeneration", func(dst string) (*pb.Result, error) {
				return backend.SoundGeneration(context.Background(), &pb.SoundGenerationRequest{Dst: dst})
			}},
		} {
			By(tc.name)
			dst := filepath.Join(GinkgoT().TempDir(), "nested", "audio.wav")
			result, err := tc.call(dst)
			if err != nil || result == nil || !result.Success {
				failFixture("RPC failed: result=%#v err=%v", result, err)
			}
			wav, err := os.ReadFile(dst)
			if err != nil {
				failFixture("%v", err)
			}
			if len(wav) != 44+22050 || string(wav[:4]) != "RIFF" || string(wav[8:12]) != "WAVE" {
				failFixture("invalid deterministic WAV: len=%d header=%q", len(wav), wav[:min(len(wav), 12)])
			}
			if got := binary.LittleEndian.Uint32(wav[24:28]); got != 22050 {
				failFixture("sample rate = %d, want 22050", got)
			}
			if got := binary.LittleEndian.Uint32(wav[40:44]); got != 22050 {
				failFixture("PCM byte count = %d, want 22050", got)
			}
			if bytes.Equal(wav[44:], make([]byte, len(wav)-44)) {
				failFixture("PCM payload is silent")
			}
		}
	})

	It("TTSStreamUsesProductionConsumerFraming", func() {
		GinkgoT().Setenv("MOCK_TTS_SAMPLE_RATE", "24000")
		backend := &MockBackend{}
		stream := &ttsFixtureStream{testServerStream: testServerStream{ctx: context.Background()}}
		if err := backend.TTSStream(&pb.TTSRequest{}, stream); err != nil {
			failFixture("TTSStream returned error: %v", err)
		}
		if len(stream.replies) < 2 {
			failFixture("got %d replies, want metadata plus PCM", len(stream.replies))
		}
		var info map[string]any
		if err := json.Unmarshal(stream.replies[0].Message, &info); err != nil {
			failFixture("production consumer cannot decode first Message: %v", err)
		}
		if got := info["sample_rate"]; got != float64(24000) {
			failFixture("sample_rate metadata = %#v, want 24000", got)
		}
		if len(stream.replies[0].Audio) != 0 {
			failFixture("metadata reply unexpectedly contains audio")
		}
		var pcm []byte
		for i, reply := range stream.replies[1:] {
			if len(reply.Message) != 0 {
				failFixture("PCM reply %d unexpectedly contains metadata", i)
			}
			pcm = append(pcm, reply.Audio...)
		}
		if len(pcm) != 24000 {
			failFixture("raw PCM length = %d, want 24000", len(pcm))
		}
		if bytes.HasPrefix(pcm, []byte("RIFF")) {
			failFixture("TTSStream sent a WAV container where the production consumer expects raw PCM")
		}
		if bytes.Equal(pcm, make([]byte, len(pcm))) {
			failFixture("streamed PCM is silent")
		}
	})

	It("FileBearingRPCsReportStagedInputDigests", func() {
		backend := &MockBackend{}
		dir := GinkgoT().TempDir()
		input := filepath.Join(dir, "fixture.bin")
		if err := os.WriteFile(input, []byte("staged-fixture"), 0600); err != nil {
			failFixture("%v", err)
		}
		const digest = "sha256:cf9e28cc728a07a8c3362a74f7fdcbc3695f593d5c8cef5c9b2ad18ff73a5024"

		image, err := backend.GenerateImage(context.Background(), &pb.GenerateImageRequest{Src: input, RefImages: []string{input}, Dst: filepath.Join(dir, "image.png")})
		assertContainsDigest(image.GetMessage(), err, digest)
		video, err := backend.GenerateVideo(context.Background(), &pb.GenerateVideoRequest{StartImage: input, EndImage: input, Audio: input, Dst: filepath.Join(dir, "video.mp4")})
		assertContainsDigest(video.GetMessage(), err, digest)
		asset, err := backend.Generate3D(context.Background(), &pb.Generate3DRequest{Src: input, Dst: filepath.Join(dir, "asset.glb")})
		assertContainsDigest(asset.GetMessage(), err, digest)
		animation, err := backend.Animate3D(context.Background(), &pb.Animate3DRequest{Inputs: map[string]*pb.AnimationInput{
			"mesh": {Type: "mesh", Data: input},
		}, Dst: filepath.Join(dir, "animation.glb")})
		assertContainsDigest(animation.GetMessage(), err, digest)
		tts, err := backend.TTS(context.Background(), &pb.TTSRequest{
			Model: input,
			Voice: input,
			Dst:   filepath.Join(dir, "tts.wav"),
			Params: map[string]string{
				"multi_reference_cond": `[{"audio":"` + input + `","text":"one"},{"audio":"` + input + `","text":"two"}]`,
			},
		})
		assertContainsDigest(tts.GetMessage(), err, digest)
		if !strings.Contains(tts.GetMessage(), "reference[0]="+digest) || !strings.Contains(tts.GetMessage(), "reference[1]="+digest) {
			failFixture("TTS did not report every staged reference: %q", tts.GetMessage())
		}
		sound, err := backend.SoundGeneration(context.Background(), &pb.SoundGenerationRequest{Model: input, Src: &input, Dst: filepath.Join(dir, "sound.wav")})
		assertContainsDigest(sound.GetMessage(), err, digest)
		detected, err := backend.SoundDetection(context.Background(), &pb.SoundDetectionRequest{Src: input})
		if err != nil {
			failFixture("SoundDetection returned error: %v", err)
		}
		if len(detected.GetDetections()) == 0 || !strings.Contains(detected.GetDetections()[0].GetLabel(), digest) {
			failFixture("SoundDetection did not report staged digest: %#v", detected)
		}
		transcript, err := backend.AudioTranscription(context.Background(), &pb.TranscriptRequest{Dst: input})
		if err != nil {
			failFixture("AudioTranscription returned error: %v", err)
		}
		if !strings.Contains(transcript.GetText(), digest) {
			failFixture("AudioTranscription did not report staged digest: %q", transcript.GetText())
		}
	})

	It("AudioTransformCopiesExactStagedInput", func() {
		dir := GinkgoT().TempDir()
		input := filepath.Join(dir, "input.wav")
		reference := filepath.Join(dir, "reference.wav")
		output := filepath.Join(dir, "nested", "output.wav")
		if err := writeMinimalWAV(input); err != nil {
			failFixture("%v", err)
		}
		if err := os.WriteFile(reference, []byte("distinct-reference"), 0o600); err != nil {
			failFixture("%v", err)
		}
		want, err := os.ReadFile(input)
		if err != nil {
			failFixture("%v", err)
		}
		result, err := (&MockBackend{}).AudioTransform(context.Background(), &pb.AudioTransformRequest{
			AudioPath:     input,
			ReferencePath: reference,
			Dst:           output,
		})
		if err != nil {
			failFixture("AudioTransform returned error: %v", err)
		}
		got, err := os.ReadFile(output)
		if err != nil {
			failFixture("%v", err)
		}
		referenceDigest := fmt.Sprintf("sha256:%x", sha256.Sum256([]byte("distinct-reference")))
		if !bytes.Equal(got, fixtureArtifact(want, "reference="+referenceDigest)) {
			failFixture("AudioTransform output does not prove the staged reference content")
		}
		if result.GetDst() != output || result.GetSampleRate() != 16000 || result.GetSamples() != 8000 {
			failFixture("unexpected AudioTransform metadata: %#v", result)
		}
	})

	It("FixturePathsMustUseExpectedWorkerStagingRoot", func() {
		root := GinkgoT().TempDir()
		inside := filepath.Join(root, "cache", "input.wav")
		outside := filepath.Join(GinkgoT().TempDir(), "input.wav")
		if err := writeFixture(inside, []byte("inside")); err != nil {
			failFixture("%v", err)
		}
		if err := writeFixture(outside, []byte("outside")); err != nil {
			failFixture("%v", err)
		}
		GinkgoT().Setenv("LOCALAI_MOCK_EXPECT_STAGING_ROOT", root)
		if _, err := fixtureInputMarker(namedFixtureInput{name: "inside", value: inside}); err != nil {
			failFixture("worker-staged input rejected: %v", err)
		}
		if _, err := fixtureInputMarker(namedFixtureInput{name: "outside", value: outside}); err == nil {
			failFixture("frontend-readable path outside the worker root was accepted")
		}
	})

	It("AudioTransformStreamEchoesExactAudioChannel", func() {
		want := []byte{1, 0, 3, 0}
		stream := &audioTransformFixtureStream{
			testServerStream: testServerStream{ctx: context.Background()},
			requests: []*pb.AudioTransformFrameRequest{
				{Payload: &pb.AudioTransformFrameRequest_Config{Config: &pb.AudioTransformStreamConfig{SampleFormat: pb.AudioTransformStreamConfig_S16_LE}}},
				{Payload: &pb.AudioTransformFrameRequest_Frame{Frame: &pb.AudioTransformFrame{AudioPcm: want, ReferencePcm: []byte{2, 0, 4, 0}}}},
			},
		}
		if err := (&MockBackend{}).AudioTransformStream(stream); err != nil {
			failFixture("AudioTransformStream returned error: %v", err)
		}
		if len(stream.responses) != 1 || !bytes.Equal(stream.responses[0].GetPcm(), want) || stream.responses[0].GetFrameIndex() != 0 {
			failFixture("unexpected AudioTransformStream response: %#v", stream.responses)
		}
	})
})

func assertContainsDigest(message string, err error, digest string) {
	GinkgoHelper()
	if err != nil {
		failFixture("RPC returned error: %v", err)
	}
	if !strings.Contains(message, digest) {
		failFixture("result %q does not contain %q", message, digest)
	}
}

var _ = Describe("Mock backend fixture contracts", func() {
	It("InlineInputsNeverReachFilesystemAPIs", func() {
		backend := &MockBackend{}
		longBase64 := strings.Repeat("QUJD", 20000)
		longDataURI := "data:audio/wav;base64," + longBase64
		longURL := "https://example.invalid/" + longBase64
		longAbsoluteOpaque := "/" + strings.Repeat("A", 3000)

		tests := []struct {
			name string
			call func() error
		}{
			{"GenerateImage", func() error {
				result, err := backend.GenerateImage(context.Background(), &pb.GenerateImageRequest{Src: longDataURI, RefImages: []string{longBase64, longURL}})
				return successfulResult(result, err)
			}},
			{"GenerateVideo", func() error {
				result, err := backend.GenerateVideo(context.Background(), &pb.GenerateVideoRequest{StartImage: longDataURI, EndImage: longBase64, Audio: longURL})
				return successfulResult(result, err)
			}},
			{"Generate3D", func() error {
				result, err := backend.Generate3D(context.Background(), &pb.Generate3DRequest{Src: longDataURI})
				return successfulResult(result, err)
			}},
			{"TTS", func() error {
				result, err := backend.TTS(context.Background(), &pb.TTSRequest{
					Model: longBase64, Voice: longDataURI,
					Params: map[string]string{"multi_reference_cond": `[{"audio":"` + longDataURI + `","text":"inline"}]`},
				})
				return successfulResult(result, err)
			}},
			{"TTSStream", func() error {
				return backend.TTSStream(&pb.TTSRequest{
					Model: longBase64, Voice: longDataURI,
					Params: map[string]string{"multi_reference_cond": `[{"audio":"` + longDataURI + `","text":"inline"}]`},
				}, &ttsFixtureStream{testServerStream: testServerStream{ctx: context.Background()}})
			}},
			{"SoundGeneration", func() error {
				result, err := backend.SoundGeneration(context.Background(), &pb.SoundGenerationRequest{Model: longBase64, Src: &longDataURI})
				return successfulResult(result, err)
			}},
			{"SoundDetection", func() error {
				_, err := backend.SoundDetection(context.Background(), &pb.SoundDetectionRequest{Src: longDataURI})
				return err
			}},
			{"SoundDetection long absolute opaque", func() error {
				_, err := backend.SoundDetection(context.Background(), &pb.SoundDetectionRequest{Src: longAbsoluteOpaque})
				return err
			}},
			{"AudioTranscription", func() error {
				_, err := backend.AudioTranscription(context.Background(), &pb.TranscriptRequest{Dst: longDataURI})
				return err
			}},
			{"AudioTranscriptionStream", func() error {
				return backend.AudioTranscriptionStream(&pb.TranscriptRequest{Dst: longDataURI}, &transcriptFixtureStream{testServerStream: testServerStream{ctx: context.Background()}})
			}},
			{"ExportModel", func() error {
				result, err := backend.ExportModel(context.Background(), &pb.ExportModelRequest{CheckpointPath: longDataURI, Model: longURL})
				return successfulResult(result, err)
			}},
			{"StartQuantization", func() error {
				result, err := backend.StartQuantization(context.Background(), &pb.QuantizationRequest{JobId: "inline", Model: longDataURI})
				if err != nil {
					return err
				}
				if result == nil || !result.Success {
					return &contractError{"quantization did not succeed"}
				}
				return nil
			}},
		}

		for _, tc := range tests {
			By(tc.name)
			err := tc.call()
			if err != nil {
				if strings.Contains(strings.ToLower(err.Error()), "file name too long") {
					failFixture("inline input reached a filesystem API: %v", err)
				}
				failFixture("RPC rejected compatible inline input: %v", err)
			}
		}
	})

	It("UnsafePathLikeInputsStayInlineAcrossFixtureRPCs", func() {
		dir := GinkgoT().TempDir()
		secret := filepath.Join(dir, "secret.bin")
		if err := os.WriteFile(secret, []byte("must-not-be-read"), 0600); err != nil {
			failFixture("%v", err)
		}
		inputs := map[string]string{
			"slash-prefixed base64": "/9j/" + strings.Repeat("QUJD", 32),
			"dot-dot traversal":     filepath.Join(dir, "nested") + string(filepath.Separator) + ".." + string(filepath.Separator) + filepath.Base(secret),
		}
		for inputName, input := range inputs {
			By(inputName)
			for _, rpc := range fixtureInputRPCCalls(input) {
				By(rpc.name)
				marker, err := rpc.call()
				if err != nil {
					failFixture("RPC rejected compatible inline input: %v", err)
				}
				if !strings.Contains(marker, "inline-sha256:") {
					failFixture("unsafe input was not kept inline: %q", marker)
				}
			}
		}
	})

	It("SafeLocalFixturePathRejectsSymlinks", func() {
		dir := GinkgoT().TempDir()
		realDir := filepath.Join(dir, "real")
		if err := os.Mkdir(realDir, 0750); err != nil {
			failFixture("%v", err)
		}
		realFile := filepath.Join(realDir, "input.bin")
		if err := os.WriteFile(realFile, []byte("fixture"), 0600); err != nil {
			failFixture("%v", err)
		}
		fileLink := filepath.Join(dir, "file-link")
		if err := os.Symlink(realFile, fileLink); err != nil {
			failFixture("%v", err)
		}
		dirLink := filepath.Join(dir, "dir-link")
		if err := os.Symlink(realDir, dirLink); err != nil {
			failFixture("%v", err)
		}
		for _, path := range []string{fileLink, filepath.Join(dirLink, "input.bin")} {
			if got, ok := safeLocalFixturePath(path); ok {
				failFixture("accepted symlink path %q as %q", path, got)
			}
			result, err := (&MockBackend{}).GenerateImage(context.Background(), &pb.GenerateImageRequest{Src: path})
			if err != nil || result == nil || !result.Success {
				failFixture("representative RPC rejected symlink as inline data: result=%#v err=%v", result, err)
			}
			if !strings.Contains(result.Message, "inline-sha256:") {
				failFixture("representative RPC followed symlink: %q", result.Message)
			}
		}
	})
})

type fixtureInputRPCCall struct {
	name string
	call func() (string, error)
}

func fixtureInputRPCCalls(input string) []fixtureInputRPCCall {
	backend := &MockBackend{}
	return []fixtureInputRPCCall{
		{"GenerateImage", func() (string, error) {
			result, err := backend.GenerateImage(context.Background(), &pb.GenerateImageRequest{Src: input, RefImages: []string{input}})
			return result.GetMessage(), err
		}},
		{"GenerateVideo", func() (string, error) {
			result, err := backend.GenerateVideo(context.Background(), &pb.GenerateVideoRequest{StartImage: input, EndImage: input, Audio: input})
			return result.GetMessage(), err
		}},
		{"Generate3D", func() (string, error) {
			result, err := backend.Generate3D(context.Background(), &pb.Generate3DRequest{Src: input})
			return result.GetMessage(), err
		}},
		{"Animate3D", func() (string, error) {
			result, err := backend.Animate3D(context.Background(), &pb.Animate3DRequest{Inputs: map[string]*pb.AnimationInput{
				"mesh": {Type: "mesh", Data: input},
			}})
			return result.GetMessage(), err
		}},
		{"TTS", func() (string, error) {
			result, err := backend.TTS(context.Background(), &pb.TTSRequest{Model: input, Voice: input, Params: map[string]string{"multi_reference_cond": `[{"audio":"` + input + `"}]`}})
			return result.GetMessage(), err
		}},
		{"TTSStream", func() (string, error) {
			stream := &ttsFixtureStream{testServerStream: testServerStream{ctx: context.Background()}}
			err := backend.TTSStream(&pb.TTSRequest{Model: input, Voice: input, Params: map[string]string{"multi_reference_cond": `[{"audio":"` + input + `"}]`}}, stream)
			messages := make([]string, 0, len(stream.replies))
			for _, reply := range stream.replies {
				messages = append(messages, string(reply.Message))
			}
			return strings.Join(messages, "; "), err
		}},
		{"SoundGeneration", func() (string, error) {
			result, err := backend.SoundGeneration(context.Background(), &pb.SoundGenerationRequest{Model: input, Src: &input})
			return result.GetMessage(), err
		}},
		{"SoundDetection", func() (string, error) {
			result, err := backend.SoundDetection(context.Background(), &pb.SoundDetectionRequest{Src: input})
			if result == nil || len(result.Detections) == 0 {
				return "", err
			}
			return result.Detections[0].Label, err
		}},
		{"AudioTranscription", func() (string, error) {
			result, err := backend.AudioTranscription(context.Background(), &pb.TranscriptRequest{Dst: input})
			return result.GetText(), err
		}},
		{"AudioTranscriptionStream", func() (string, error) {
			stream := &transcriptFixtureStream{testServerStream: testServerStream{ctx: context.Background()}}
			err := backend.AudioTranscriptionStream(&pb.TranscriptRequest{Dst: input}, stream)
			if len(stream.responses) == 0 {
				return "", err
			}
			return stream.responses[len(stream.responses)-1].GetFinalResult().GetText(), err
		}},
		{"ExportModel", func() (string, error) {
			result, err := backend.ExportModel(context.Background(), &pb.ExportModelRequest{CheckpointPath: input, Model: input})
			return result.GetMessage(), err
		}},
		{"StartQuantization", func() (string, error) {
			result, err := backend.StartQuantization(context.Background(), &pb.QuantizationRequest{JobId: "unsafe-input", Model: input})
			return result.GetMessage(), err
		}},
	}
}

func successfulResult(result *pb.Result, err error) error {
	if err != nil {
		return err
	}
	if result == nil || !result.Success {
		return &contractError{"RPC did not report success"}
	}
	return nil
}

type contractError struct{ message string }

func (e *contractError) Error() string { return e.message }

var _ = Describe("Mock backend fixture contracts", func() {
	It("ExportAndQuantizationCreateNestedFixtures", func() {
		backend := &MockBackend{}
		exportDir := filepath.Join(GinkgoT().TempDir(), "export")
		result, err := backend.ExportModel(context.Background(), &pb.ExportModelRequest{OutputPath: exportDir})
		if err != nil || result == nil || !result.Success {
			failFixture("ExportModel failed: result=%#v err=%v", result, err)
		}
		assertFileBytes(filepath.Join(exportDir, "nested", "weights.bin"), []byte("MOCK-EXPORTED-WEIGHTS\n"))
		assertFileBytes(filepath.Join(exportDir, "nested", "config.json"), []byte("{\"mock\":true}\n"))

		quantDir := filepath.Join(GinkgoT().TempDir(), "quant")
		job, err := backend.StartQuantization(context.Background(), &pb.QuantizationRequest{JobId: "job-42", OutputDir: quantDir, QuantizationType: "q4_k_m"})
		if err != nil || job == nil || !job.Success {
			failFixture("StartQuantization failed: result=%#v err=%v", job, err)
		}
		stream := &quantFixtureStream{testServerStream: testServerStream{ctx: context.Background()}}
		if err := backend.QuantizationProgress(&pb.QuantizationProgressRequest{JobId: "job-42"}, stream); err != nil {
			failFixture("QuantizationProgress failed: %v", err)
		}
		if len(stream.updates) != 1 {
			failFixture("got %d progress updates, want 1", len(stream.updates))
		}
		update := stream.updates[0]
		if update.Status != "completed" || update.ProgressPercent != 100 || update.OutputFile == "" {
			failFixture("unexpected completed update: %#v", update)
		}
		assertFileBytes(update.OutputFile, []byte("MOCK-GGUF:q4_k_m\n"))
	})

	It("AudioTranscriptionStreamReportsFixtureDigest", func() {
		input := filepath.Join(GinkgoT().TempDir(), "audio.wav")
		if err := os.WriteFile(input, []byte("streamed-audio"), 0600); err != nil {
			failFixture("%v", err)
		}
		stream := &transcriptFixtureStream{testServerStream: testServerStream{ctx: context.Background()}}
		if err := (&MockBackend{}).AudioTranscriptionStream(&pb.TranscriptRequest{Dst: input}, stream); err != nil {
			failFixture("AudioTranscriptionStream failed: %v", err)
		}
		const digest = "sha256:6fec91c5f1f08f64e9fad3c72072abf551821bcbd325ea1ff73b357e6a9c72af"
		if len(stream.responses) < 2 || !strings.Contains(stream.responses[len(stream.responses)-1].GetFinalResult().GetText(), digest) {
			failFixture("stream did not finish with fixture digest: %#v", stream.responses)
		}
	})
})

func assertFileBytes(path string, want []byte) {
	GinkgoHelper()
	got, err := os.ReadFile(path)
	if err != nil {
		failFixture("read %s: %v", path, err)
	}
	if !bytes.Equal(got, want) {
		failFixture("%s differs: got %q want %q", path, got, want)
	}
}

type testServerStream struct{ ctx context.Context }

func (s testServerStream) SetHeader(metadata.MD) error  { return nil }
func (s testServerStream) SendHeader(metadata.MD) error { return nil }
func (s testServerStream) SetTrailer(metadata.MD)       {}
func (s testServerStream) Context() context.Context     { return s.ctx }
func (s testServerStream) SendMsg(any) error            { return nil }
func (s testServerStream) RecvMsg(any) error            { return nil }

type ttsFixtureStream struct {
	testServerStream
	replies []*pb.Reply
}

func (s *ttsFixtureStream) Send(reply *pb.Reply) error {
	s.replies = append(s.replies, reply)
	return nil
}

type transcriptFixtureStream struct {
	testServerStream
	responses []*pb.TranscriptStreamResponse
}

func (s *transcriptFixtureStream) Send(response *pb.TranscriptStreamResponse) error {
	s.responses = append(s.responses, response)
	return nil
}

type audioTransformFixtureStream struct {
	testServerStream
	requests  []*pb.AudioTransformFrameRequest
	responses []*pb.AudioTransformFrameResponse
}

func (s *audioTransformFixtureStream) Recv() (*pb.AudioTransformFrameRequest, error) {
	if len(s.requests) == 0 {
		return nil, io.EOF
	}
	request := s.requests[0]
	s.requests = s.requests[1:]
	return request, nil
}

func (s *audioTransformFixtureStream) Send(response *pb.AudioTransformFrameResponse) error {
	s.responses = append(s.responses, response)
	return nil
}

type quantFixtureStream struct {
	testServerStream
	updates []*pb.QuantizationProgressUpdate
}

func (s *quantFixtureStream) Send(update *pb.QuantizationProgressUpdate) error {
	s.updates = append(s.updates, update)
	return nil
}
