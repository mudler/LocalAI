package main

import (
	"bytes"
	"context"
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"
	"testing"

	pb "github.com/mudler/LocalAI/pkg/grpc/proto"
	"google.golang.org/grpc/metadata"
)

var (
	wantPNG   = mustHex("89504e470d0a1a0a0000000d49484452000000010000000108060000001f15c4890000000d4944415408d763f8cfc0f01f00050001ff89993d1d0000000049454e44ae426082")
	wantWAV   = mustHex("524946462800000057415645666d74201000000001000100401f0000803e000002001000646174610400000000000000")
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

func TestFixtureOutputRPCsWriteExactBytes(t *testing.T) {
	t.Parallel()

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
			name: "tts", file: "speech.wav", want: wantWAV,
			call: func(dst string) (*pb.Result, error) {
				return backend.TTS(context.Background(), &pb.TTSRequest{Dst: dst})
			},
		},
		{
			name: "sound", file: "sound.wav", want: wantWAV,
			call: func(dst string) (*pb.Result, error) {
				return backend.SoundGeneration(context.Background(), &pb.SoundGenerationRequest{Dst: dst})
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			dst := filepath.Join(t.TempDir(), "nested", tc.file)
			result, err := tc.call(dst)
			if err != nil {
				t.Fatalf("RPC returned error: %v", err)
			}
			if result == nil || !result.Success {
				t.Fatalf("RPC did not report success: %#v", result)
			}
			got, err := os.ReadFile(dst)
			if err != nil {
				t.Fatalf("read output: %v", err)
			}
			if !bytes.Equal(got, tc.want) {
				t.Fatalf("output bytes differ\n got: %x\nwant: %x", got, tc.want)
			}
		})
	}
}

func TestTTSStreamEmitsExactWAVFixture(t *testing.T) {
	backend := &MockBackend{}
	stream := &ttsFixtureStream{testServerStream: testServerStream{ctx: context.Background()}}
	if err := backend.TTSStream(&pb.TTSRequest{}, stream); err != nil {
		t.Fatalf("TTSStream returned error: %v", err)
	}
	var got []byte
	for _, reply := range stream.replies {
		got = append(got, reply.Audio...)
	}
	if !bytes.Equal(got, wantWAV) {
		t.Fatalf("streamed WAV differs\n got: %x\nwant: %x", got, wantWAV)
	}
}

func TestFileBearingRPCsReportStagedInputDigests(t *testing.T) {
	backend := &MockBackend{}
	dir := t.TempDir()
	input := filepath.Join(dir, "fixture.bin")
	if err := os.WriteFile(input, []byte("staged-fixture"), 0600); err != nil {
		t.Fatal(err)
	}
	const digest = "sha256:cf9e28cc728a07a8c3362a74f7fdcbc3695f593d5c8cef5c9b2ad18ff73a5024"

	image, err := backend.GenerateImage(context.Background(), &pb.GenerateImageRequest{Src: input, RefImages: []string{input}, Dst: filepath.Join(dir, "image.png")})
	assertContainsDigest(t, image.GetMessage(), err, digest)
	video, err := backend.GenerateVideo(context.Background(), &pb.GenerateVideoRequest{StartImage: input, EndImage: input, Audio: input, Dst: filepath.Join(dir, "video.mp4")})
	assertContainsDigest(t, video.GetMessage(), err, digest)
	asset, err := backend.Generate3D(context.Background(), &pb.Generate3DRequest{Src: input, Dst: filepath.Join(dir, "asset.glb")})
	assertContainsDigest(t, asset.GetMessage(), err, digest)
	tts, err := backend.TTS(context.Background(), &pb.TTSRequest{
		Model: input,
		Voice: input,
		Dst:   filepath.Join(dir, "tts.wav"),
		Params: map[string]string{
			"multi_reference_cond": `[{"audio":"` + input + `","text":"one"},{"audio":"` + input + `","text":"two"}]`,
		},
	})
	assertContainsDigest(t, tts.GetMessage(), err, digest)
	if !strings.Contains(tts.GetMessage(), "reference[0]="+digest) || !strings.Contains(tts.GetMessage(), "reference[1]="+digest) {
		t.Fatalf("TTS did not report every staged reference: %q", tts.GetMessage())
	}
	sound, err := backend.SoundGeneration(context.Background(), &pb.SoundGenerationRequest{Model: input, Src: &input, Dst: filepath.Join(dir, "sound.wav")})
	assertContainsDigest(t, sound.GetMessage(), err, digest)
	detected, err := backend.SoundDetection(context.Background(), &pb.SoundDetectionRequest{Src: input})
	if err != nil {
		t.Fatalf("SoundDetection returned error: %v", err)
	}
	if len(detected.GetDetections()) == 0 || !strings.Contains(detected.GetDetections()[0].GetLabel(), digest) {
		t.Fatalf("SoundDetection did not report staged digest: %#v", detected)
	}
	transcript, err := backend.AudioTranscription(context.Background(), &pb.TranscriptRequest{Dst: input})
	if err != nil {
		t.Fatalf("AudioTranscription returned error: %v", err)
	}
	if !strings.Contains(transcript.GetText(), digest) {
		t.Fatalf("AudioTranscription did not report staged digest: %q", transcript.GetText())
	}
}

func assertContainsDigest(t *testing.T, message string, err error, digest string) {
	t.Helper()
	if err != nil {
		t.Fatalf("RPC returned error: %v", err)
	}
	if !strings.Contains(message, digest) {
		t.Fatalf("result %q does not contain %q", message, digest)
	}
}

func TestInlineInputsNeverReachFilesystemAPIs(t *testing.T) {
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
		t.Run(tc.name, func(t *testing.T) {
			err := tc.call()
			if err != nil {
				if strings.Contains(strings.ToLower(err.Error()), "file name too long") {
					t.Fatalf("inline input reached a filesystem API: %v", err)
				}
				t.Fatalf("RPC rejected compatible inline input: %v", err)
			}
		})
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

func TestExportAndQuantizationCreateNestedFixtures(t *testing.T) {
	backend := &MockBackend{}
	exportDir := filepath.Join(t.TempDir(), "export")
	result, err := backend.ExportModel(context.Background(), &pb.ExportModelRequest{OutputPath: exportDir})
	if err != nil || result == nil || !result.Success {
		t.Fatalf("ExportModel failed: result=%#v err=%v", result, err)
	}
	assertFileBytes(t, filepath.Join(exportDir, "nested", "weights.bin"), []byte("MOCK-EXPORTED-WEIGHTS\n"))
	assertFileBytes(t, filepath.Join(exportDir, "nested", "config.json"), []byte("{\"mock\":true}\n"))

	quantDir := filepath.Join(t.TempDir(), "quant")
	job, err := backend.StartQuantization(context.Background(), &pb.QuantizationRequest{JobId: "job-42", OutputDir: quantDir, QuantizationType: "q4_k_m"})
	if err != nil || job == nil || !job.Success {
		t.Fatalf("StartQuantization failed: result=%#v err=%v", job, err)
	}
	stream := &quantFixtureStream{testServerStream: testServerStream{ctx: context.Background()}}
	if err := backend.QuantizationProgress(&pb.QuantizationProgressRequest{JobId: "job-42"}, stream); err != nil {
		t.Fatalf("QuantizationProgress failed: %v", err)
	}
	if len(stream.updates) != 1 {
		t.Fatalf("got %d progress updates, want 1", len(stream.updates))
	}
	update := stream.updates[0]
	if update.Status != "completed" || update.ProgressPercent != 100 || update.OutputFile == "" {
		t.Fatalf("unexpected completed update: %#v", update)
	}
	assertFileBytes(t, update.OutputFile, []byte("MOCK-GGUF:q4_k_m\n"))
}

func TestAudioTranscriptionStreamReportsFixtureDigest(t *testing.T) {
	input := filepath.Join(t.TempDir(), "audio.wav")
	if err := os.WriteFile(input, []byte("streamed-audio"), 0600); err != nil {
		t.Fatal(err)
	}
	stream := &transcriptFixtureStream{testServerStream: testServerStream{ctx: context.Background()}}
	if err := (&MockBackend{}).AudioTranscriptionStream(&pb.TranscriptRequest{Dst: input}, stream); err != nil {
		t.Fatalf("AudioTranscriptionStream failed: %v", err)
	}
	const digest = "sha256:6fec91c5f1f08f64e9fad3c72072abf551821bcbd325ea1ff73b357e6a9c72af"
	if len(stream.responses) < 2 || !strings.Contains(stream.responses[len(stream.responses)-1].GetFinalResult().GetText(), digest) {
		t.Fatalf("stream did not finish with fixture digest: %#v", stream.responses)
	}
}

func assertFileBytes(t *testing.T, path string, want []byte) {
	t.Helper()
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	if !bytes.Equal(got, want) {
		t.Fatalf("%s differs: got %q want %q", path, got, want)
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

type quantFixtureStream struct {
	testServerStream
	updates []*pb.QuantizationProgressUpdate
}

func (s *quantFixtureStream) Send(update *pb.QuantizationProgressUpdate) error {
	s.updates = append(s.updates, update)
	return nil
}
