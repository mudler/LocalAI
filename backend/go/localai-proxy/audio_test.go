package main

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"google.golang.org/grpc/codes"

	pb "github.com/mudler/LocalAI/pkg/grpc/proto"
)

// writeInput writes content to a fresh temp file and returns its path, so a
// spec can check the upstream received exactly those bytes.
func writeInput(name, content string) string {
	path := filepath.Join(GinkgoT().TempDir(), name)
	Expect(os.WriteFile(path, []byte(content), 0o600)).To(Succeed())
	return path
}

// drainBytes collects every chunk sent on ch until it is closed.
func drainBytes(ch chan []byte) <-chan [][]byte {
	done := make(chan [][]byte, 1)
	go func() {
		var got [][]byte
		for c := range ch {
			got = append(got, c)
		}
		done <- got
	}()
	return done
}

func drainTranscript(ch chan *pb.TranscriptStreamResponse) <-chan []*pb.TranscriptStreamResponse {
	done := make(chan []*pb.TranscriptStreamResponse, 1)
	go func() {
		var got []*pb.TranscriptStreamResponse
		for c := range ch {
			got = append(got, c)
		}
		done <- got
	}()
	return done
}

// cutMidStream answers 200, flushes body, then drops the connection without
// finishing the chunked encoding, as a crashed upstream would.
func cutMidStream(contentType, body string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", contentType)
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(body))
		w.(http.Flusher).Flush()
		conn, _, err := w.(http.Hijacker).Hijack()
		if err == nil {
			_ = conn.Close()
		}
	}
}

var _ = Describe("audio methods", func() {
	var up *fakeUpstream

	BeforeEach(func() {
		up = newFakeUpstream()
		DeferCleanup(up.Close)
	})

	Describe("TTS", func() {
		It("posts to /tts and writes the upstream audio to Dst", func() {
			p := loadProxy(up, nil)
			up.script("/tts", scriptedResponse{Status: http.StatusOK, ContentType: "audio/wav", Body: "RIFF-audio-bytes"})
			dst := filepath.Join(GinkgoT().TempDir(), "out.wav")
			lang := "it"
			instr := "cheerful"

			Expect(p.TTS(&pb.TTSRequest{
				Text: "ciao", Model: "local-path", Dst: dst, Voice: "v1", Language: &lang,
				Instructions: &instr, Params: map[string]string{"speed": "1.2"},
			})).To(Succeed())

			got, err := os.ReadFile(dst)
			Expect(err).NotTo(HaveOccurred())
			Expect(string(got)).To(Equal("RIFF-audio-bytes"))
			req := up.last()
			Expect(req.Path).To(Equal("/tts"))
			Expect(req.JSON).To(Equal(map[string]any{
				"model": "remote-model", "input": "ciao", "voice": "v1", "language": "it",
				"instructions": "cheerful", "params": map[string]any{"speed": "1.2"},
			}))
		})

		It("maps an upstream failure and leaves no partial file", func() {
			p := loadProxy(up, nil)
			up.script("/tts", scriptedResponse{Status: http.StatusInternalServerError, Body: "boom"})
			dst := filepath.Join(GinkgoT().TempDir(), "out.wav")
			err := p.TTS(&pb.TTSRequest{Text: "x", Dst: dst})
			Expect(codeOf(err)).To(Equal(codes.Unavailable))
			Expect(dst).NotTo(BeAnExistingFile())
		})
	})

	Describe("TTSStream", func() {
		It("forwards the chunked WAV body in order and closes the channel", func() {
			header := "RIFF" + string(make([]byte, 40))
			up = newFakeUpstreamWithHandler(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "audio/wav")
				w.WriteHeader(http.StatusOK)
				for _, c := range []string{header, "pcm-1", "pcm-2"} {
					_, _ = w.Write([]byte(c))
					w.(http.Flusher).Flush()
					time.Sleep(10 * time.Millisecond)
				}
			})
			DeferCleanup(up.Close)
			p := loadProxy(up, nil)

			out := make(chan []byte)
			done := drainBytes(out)
			Expect(p.TTSStream(&pb.TTSRequest{Text: "hi"}, out)).To(Succeed())

			var chunks [][]byte
			Eventually(done).Should(Receive(&chunks))
			Expect(chunks).NotTo(BeEmpty())
			Expect(string(chunks[0][:4])).To(Equal("RIFF"))
			var all []byte
			for _, c := range chunks {
				all = append(all, c...)
			}
			Expect(string(all)).To(Equal(header + "pcm-1pcm-2"))
		})

		It("asks the upstream to stream", func() {
			p := loadProxy(up, nil)
			up.script("/tts", scriptedResponse{Status: http.StatusOK, ContentType: "audio/wav", Body: "RIFF"})
			out := make(chan []byte)
			done := drainBytes(out)
			Expect(p.TTSStream(&pb.TTSRequest{Text: "hi", Voice: "v"}, out)).To(Succeed())
			Eventually(done).Should(Receive())
			Expect(up.last().JSON).To(HaveKeyWithValue("stream", true))
			Expect(up.last().JSON).To(HaveKeyWithValue("input", "hi"))
		})

		It("reports a mid-stream disconnect as Unavailable and still closes the channel", func() {
			cut := newFakeUpstreamWithHandler(cutMidStream("audio/wav", "RIFFpartial"))
			DeferCleanup(cut.Close)
			p := loadProxy(cut, nil)
			out := make(chan []byte)
			done := drainBytes(out)
			err := p.TTSStream(&pb.TTSRequest{Text: "hi"}, out)
			Expect(codeOf(err)).To(Equal(codes.Unavailable))
			Eventually(done).Should(Receive())
		})

		It("closes the channel when the upstream refuses the request", func() {
			p := loadProxy(up, nil)
			up.script("/tts", scriptedResponse{Status: http.StatusBadRequest, Body: "bad"})
			out := make(chan []byte)
			done := drainBytes(out)
			Expect(codeOf(p.TTSStream(&pb.TTSRequest{Text: "hi"}, out))).To(Equal(codes.InvalidArgument))
			Eventually(done).Should(Receive())
		})
	})

	Describe("SoundGeneration", func() {
		It("posts the ElevenLabs body to /v1/sound-generation and writes Dst", func() {
			p := loadProxy(up, nil)
			up.script("/v1/sound-generation", scriptedResponse{Status: http.StatusOK, ContentType: "audio/wav", Body: "RIFF-sfx"})
			dst := filepath.Join(GinkgoT().TempDir(), "sfx.wav")
			dur, temp, bpm := float32(4.5), float32(0.3), int32(120)
			sample, instrumental := true, false

			Expect(p.SoundGeneration(&pb.SoundGenerationRequest{
				Text: "rain", Dst: dst, Duration: &dur, Temperature: &temp, Sample: &sample,
				Bpm: &bpm, Caption: ptr("cap"), Lyrics: ptr("la"), Keyscale: ptr("C major"),
				Language: ptr("en"), Timesignature: ptr("4/4"), Instrumental: &instrumental,
			})).To(Succeed())

			got, err := os.ReadFile(dst)
			Expect(err).NotTo(HaveOccurred())
			Expect(string(got)).To(Equal("RIFF-sfx"))
			Expect(up.last().JSON).To(Equal(map[string]any{
				"model_id": "remote-model", "text": "rain", "duration_seconds": 4.5,
				"prompt_influence": 0.3, "do_sample": true, "bpm": float64(120),
				"caption": "cap", "lyrics": "la", "keyscale": "C major", "language": "en",
				"timesignature": "4/4", "instrumental": false,
			}))
		})

		It("refuses audio-conditioned generation it cannot upload", func() {
			p := loadProxy(up, nil)
			err := p.SoundGeneration(&pb.SoundGenerationRequest{Text: "x", Src: ptr("/tmp/in.wav")})
			Expect(codeOf(err)).To(Equal(codes.Unimplemented))
			Expect(up.recorded()).To(BeEmpty())
		})
	})

	Describe("AudioTranscription", func() {
		It("uploads Dst with the request fields and maps the seconds-based result", func() {
			p := loadProxy(up, nil)
			up.replyJSON("/v1/audio/transcriptions", map[string]any{
				"text": "hello world", "language": "en", "duration": 2.5,
				"segments": []any{map[string]any{
					"id": 0, "start": 0.5, "end": 1.25, "text": "hello world", "tokens": []int{1, 2},
					"speaker": "SPEAKER_00",
					"words":   []any{map[string]any{"start": 0.5, "end": 0.75, "text": "hello"}},
				}},
			})
			in := writeInput("speech.wav", "RIFF-speech")

			res, err := p.AudioTranscription(context.Background(), &pb.TranscriptRequest{
				Dst: in, Language: "en", Translate: true, Diarize: true, Prompt: "names: Ada",
				Temperature: 0.2, TimestampGranularities: []string{"word"},
			})
			Expect(err).NotTo(HaveOccurred())

			req := up.last()
			Expect(req.Path).To(Equal("/v1/audio/transcriptions"))
			Expect(req.Files).To(Equal(map[string]string{"file": "RIFF-speech"}))
			Expect(req.Fields).To(Equal(map[string]string{
				"model": "remote-model", "language": "en", "translate": "true", "diarize": "true",
				"prompt": "names: Ada", "temperature": "0.2", "timestamp_granularities[]": "word",
				"response_format": "verbose_json",
			}))

			Expect(res.Text).To(Equal("hello world"))
			Expect(res.Language).To(Equal("en"))
			Expect(res.Duration).To(BeNumerically("==", 2.5))
			Expect(res.Segments).To(HaveLen(1))
			seg := res.Segments[0]
			// pb carries nanoseconds (core reads them as time.Duration).
			Expect(seg.Start).To(Equal(int64(500 * time.Millisecond)))
			Expect(seg.End).To(Equal(int64(1250 * time.Millisecond)))
			Expect(seg.Text).To(Equal("hello world"))
			Expect(seg.Tokens).To(Equal([]int32{1, 2}))
			Expect(seg.Speaker).To(Equal("SPEAKER_00"))
			Expect(seg.Words).To(HaveLen(1))
			Expect(seg.Words[0].Start).To(Equal(int64(500 * time.Millisecond)))
			Expect(seg.Words[0].Text).To(Equal("hello"))
		})

		It("sends diarize=false explicitly because the upstream defaults it on", func() {
			p := loadProxy(up, nil)
			up.replyJSON("/v1/audio/transcriptions", map[string]any{"text": "x"})
			_, err := p.AudioTranscription(context.Background(), &pb.TranscriptRequest{Dst: writeInput("a.wav", "A")})
			Expect(err).NotTo(HaveOccurred())
			f := up.last().Fields
			Expect(f).To(HaveKeyWithValue("diarize", "false"))
			// Unset language and translate are left to the upstream model config.
			Expect(f).NotTo(HaveKey("language"))
			Expect(f).NotTo(HaveKey("translate"))
		})

		It("maps a 4xx to InvalidArgument", func() {
			p := loadProxy(up, nil)
			up.script("/v1/audio/transcriptions", scriptedResponse{Status: http.StatusBadRequest, Body: "bad audio"})
			_, err := p.AudioTranscription(context.Background(), &pb.TranscriptRequest{Dst: writeInput("a.wav", "A")})
			Expect(codeOf(err)).To(Equal(codes.InvalidArgument))
		})
	})

	Describe("AudioTranscriptionStream", func() {
		It("emits deltas, then the final result, and closes the channel", func() {
			p := loadProxy(up, nil)
			up.script("/v1/audio/transcriptions", scriptedResponse{SSE: []string{
				sseJSON(map[string]any{"type": "transcript.text.delta", "delta": "hel"}),
				sseJSON(map[string]any{"type": "transcript.text.delta", "delta": "lo"}),
				sseJSON(map[string]any{
					"type": "transcript.text.done", "text": "hello", "language": "en", "duration": 1.5,
					"segments": []any{map[string]any{"id": 0, "start": 0.0, "end": 1.5, "text": "hello"}},
				}),
				"[DONE]",
			}})
			out := make(chan *pb.TranscriptStreamResponse)
			done := drainTranscript(out)

			Expect(p.AudioTranscriptionStream(context.Background(),
				&pb.TranscriptRequest{Dst: writeInput("a.wav", "RIFF-a"), Stream: true}, out)).To(Succeed())

			var got []*pb.TranscriptStreamResponse
			Eventually(done).Should(Receive(&got))
			Expect(got).To(HaveLen(3))
			Expect(got[0].Delta).To(Equal("hel"))
			Expect(got[1].Delta).To(Equal("lo"))
			final := got[2].FinalResult
			Expect(final).NotTo(BeNil())
			Expect(final.Text).To(Equal("hello"))
			Expect(final.Language).To(Equal("en"))
			Expect(final.Duration).To(BeNumerically("==", 1.5))
			Expect(final.Segments).To(HaveLen(1))
			Expect(final.Segments[0].End).To(Equal(int64(1500 * time.Millisecond)))

			req := up.last()
			Expect(req.Fields).To(HaveKeyWithValue("stream", "true"))
			Expect(req.Files).To(HaveKeyWithValue("file", "RIFF-a"))
		})

		It("returns an upstream error event as Unavailable", func() {
			p := loadProxy(up, nil)
			up.script("/v1/audio/transcriptions", scriptedResponse{SSE: []string{
				sseJSON(map[string]any{"type": "error", "error": map[string]any{"message": "decoder died"}}),
				"[DONE]",
			}})
			out := make(chan *pb.TranscriptStreamResponse)
			done := drainTranscript(out)
			err := p.AudioTranscriptionStream(context.Background(), &pb.TranscriptRequest{Dst: writeInput("a.wav", "A")}, out)
			Expect(codeOf(err)).To(Equal(codes.Unavailable))
			Expect(err).To(MatchError(ContainSubstring("decoder died")))
			Eventually(done).Should(Receive())
		})

		It("reports an upstream disconnect before the final result as Unavailable", func() {
			frame := "data: " + sseJSON(map[string]any{"type": "transcript.text.delta", "delta": "hel"}) + "\n\n"
			cut := newFakeUpstreamWithHandler(cutMidStream("text/event-stream", frame))
			DeferCleanup(cut.Close)
			p := loadProxy(cut, nil)
			out := make(chan *pb.TranscriptStreamResponse)
			done := drainTranscript(out)
			err := p.AudioTranscriptionStream(context.Background(), &pb.TranscriptRequest{Dst: writeInput("a.wav", "A")}, out)
			Expect(codeOf(err)).To(Equal(codes.Unavailable))
			var got []*pb.TranscriptStreamResponse
			Eventually(done).Should(Receive(&got))
			Expect(got).To(HaveLen(1))
		})

		It("closes the channel when the local file is missing", func() {
			p := loadProxy(up, nil)
			out := make(chan *pb.TranscriptStreamResponse)
			done := drainTranscript(out)
			err := p.AudioTranscriptionStream(context.Background(), &pb.TranscriptRequest{Dst: "/nonexistent.wav"}, out)
			Expect(codeOf(err)).To(Equal(codes.InvalidArgument))
			Eventually(done).Should(Receive())
			Expect(up.recorded()).To(BeEmpty())
		})
	})

	Describe("Diarize", func() {
		It("uploads Dst with the tuning fields and maps the segments", func() {
			p := loadProxy(up, nil)
			up.replyJSON("/v1/audio/diarization", map[string]any{
				"task": "diarize", "duration": 3.0, "language": "en", "num_speakers": 2,
				"segments": []any{
					map[string]any{"id": 0, "speaker": "SPEAKER_00", "label": "spk_a", "start": 0.0, "end": 1.5, "text": "hi"},
					map[string]any{"id": 1, "speaker": "SPEAKER_01", "start": 1.5, "end": 3.0},
				},
			})
			res, err := p.Diarize(&pb.DiarizeRequest{
				Dst: writeInput("talk.wav", "RIFF-talk"), Language: "en", NumSpeakers: 2, MinSpeakers: 1,
				MaxSpeakers: 3, ClusteringThreshold: 0.5, MinDurationOn: 0.1, MinDurationOff: 0.2, IncludeText: true,
			})
			Expect(err).NotTo(HaveOccurred())

			req := up.last()
			Expect(req.Path).To(Equal("/v1/audio/diarization"))
			Expect(req.Files).To(Equal(map[string]string{"file": "RIFF-talk"}))
			Expect(req.Fields).To(Equal(map[string]string{
				"model": "remote-model", "language": "en", "num_speakers": "2", "min_speakers": "1",
				"max_speakers": "3", "clustering_threshold": "0.5", "min_duration_on": "0.1",
				"min_duration_off": "0.2", "include_text": "true", "response_format": "verbose_json",
			}))

			Expect(res.NumSpeakers).To(Equal(int32(2)))
			Expect(res.Duration).To(BeNumerically("==", 3.0))
			Expect(res.Language).To(Equal("en"))
			Expect(res.Segments).To(HaveLen(2))
			// The raw upstream label survives, so core's own normalisation
			// yields the same speakers a direct call would.
			Expect(res.Segments[0].Speaker).To(Equal("spk_a"))
			Expect(res.Segments[0].Text).To(Equal("hi"))
			Expect(res.Segments[1].Speaker).To(Equal("SPEAKER_01"))
			Expect(res.Segments[1].Start).To(BeNumerically("==", 1.5))
			Expect(res.Segments[1].Id).To(Equal(int32(1)))
		})
	})

	Describe("VAD", func() {
		It("posts the samples to /v1/vad and maps the segments", func() {
			p := loadProxy(up, nil)
			up.replyJSON("/v1/vad", map[string]any{"segments": []any{map[string]any{"start": 0.25, "end": 1.5}}})
			res, err := p.VAD(&pb.VADRequest{Audio: []float32{0.5, -0.25}})
			Expect(err).NotTo(HaveOccurred())
			Expect(up.last().JSON).To(Equal(map[string]any{"model": "remote-model", "audio": []any{0.5, -0.25}}))
			Expect(res.Segments).To(HaveLen(1))
			Expect(res.Segments[0].Start).To(BeNumerically("==", 0.25))
			Expect(res.Segments[0].End).To(BeNumerically("==", 1.5))
		})
	})

	Describe("SoundDetection", func() {
		It("uploads Src to /v1/audio/classification and maps the detections", func() {
			p := loadProxy(up, nil)
			up.replyJSON("/v1/audio/classification", map[string]any{"model": "remote-model", "detections": []any{
				map[string]any{"index": 74, "label": "Dog", "score": 0.9},
				map[string]any{"index": 0, "label": "Speech", "score": 0.4},
			}})
			res, err := p.SoundDetection(context.Background(), &pb.SoundDetectionRequest{
				Src: writeInput("bark.wav", "RIFF-bark"), TopK: 5, Threshold: 0.25,
			})
			Expect(err).NotTo(HaveOccurred())
			req := up.last()
			Expect(req.Files).To(Equal(map[string]string{"file": "RIFF-bark"}))
			Expect(req.Fields).To(Equal(map[string]string{"model": "remote-model", "top_k": "5", "threshold": "0.25"}))
			Expect(res.Detections).To(HaveLen(2))
			Expect(res.Detections[0].Label).To(Equal("Dog"))
			Expect(res.Detections[0].Index).To(Equal(int32(74)))
			Expect(res.Detections[0].Score).To(BeNumerically("~", 0.9, 1e-6))
		})
	})

	Describe("AudioTransform", func() {
		It("uploads audio and reference with params and writes the result to Dst", func() {
			p := loadProxy(up, nil)
			up.script("/audio/transformations", scriptedResponse{Status: http.StatusOK, ContentType: "audio/wav", Body: "RIFF-clean"})
			dst := filepath.Join(GinkgoT().TempDir(), "transform.wav")

			res, err := p.AudioTransform(&pb.AudioTransformRequest{
				AudioPath: writeInput("mic.wav", "RIFF-mic"), ReferencePath: writeInput("ref.wav", "RIFF-ref"),
				Dst: dst, Params: map[string]string{"noise_gate": "true"},
			})
			Expect(err).NotTo(HaveOccurred())
			got, err := os.ReadFile(dst)
			Expect(err).NotTo(HaveOccurred())
			Expect(string(got)).To(Equal("RIFF-clean"))
			Expect(res.Dst).To(Equal(dst))
			Expect(res.ReferenceProvided).To(BeTrue())
			Expect(res.Stems).To(BeEmpty())

			req := up.last()
			Expect(req.Path).To(Equal("/audio/transformations"))
			Expect(req.Files).To(Equal(map[string]string{"audio": "RIFF-mic", "reference": "RIFF-ref"}))
			Expect(req.Fields).To(Equal(map[string]string{"model": "remote-model", "params[noise_gate]": "true"}))
		})

		It("fetches the stems the upstream names and writes them beside Dst", func() {
			p := loadProxy(up, nil)
			stems := `[{"name":"vocals","url":"/generated-audio/sep%20vocals.wav"},{"name":"evil","url":"/etc/passwd"}]`
			up.script("/audio/transformations", scriptedResponse{
				Status: http.StatusOK, ContentType: "audio/wav", Body: "RIFF-drums",
				Header: map[string]string{"X-Audio-Stems": stems},
			})
			up.script("/generated-audio/sep vocals.wav", scriptedResponse{Status: http.StatusOK, ContentType: "audio/wav", Body: "RIFF-vocals"})
			dir := GinkgoT().TempDir()
			dst := filepath.Join(dir, "transform.wav")

			res, err := p.AudioTransform(&pb.AudioTransformRequest{AudioPath: writeInput("song.wav", "RIFF-song"), Dst: dst})
			Expect(err).NotTo(HaveOccurred())
			Expect(res.ReferenceProvided).To(BeFalse())
			Expect(res.Stems).To(HaveLen(1))
			Expect(res.Stems[0].Name).To(Equal("vocals"))
			// Core keeps only stems that are direct children of Dst's directory.
			Expect(filepath.Dir(res.Stems[0].Dst)).To(Equal(dir))
			got, err := os.ReadFile(res.Stems[0].Dst)
			Expect(err).NotTo(HaveOccurred())
			Expect(string(got)).To(Equal("RIFF-vocals"))

			var paths []string
			for _, r := range up.recorded() {
				paths = append(paths, r.Method+" "+r.Path)
			}
			Expect(paths).To(Equal([]string{"POST /audio/transformations", "GET /generated-audio/sep vocals.wav"}))
		})
	})

	It("names the upload file after the local file", func() {
		// The upstream saves the upload under its base name and some handlers
		// pick the decoder by extension, so the name must reach it intact.
		var name string
		named := newFakeUpstreamWithHandler(func(w http.ResponseWriter, r *http.Request) {
			_, fh, err := r.FormFile("file")
			if err == nil {
				name = fh.Filename
			}
			w.Header().Set("Content-Type", "application/json")
			_, _ = fmt.Fprint(w, `{"detections":[]}`)
		})
		DeferCleanup(named.Close)
		p := loadProxy(named, nil)
		_, err := p.SoundDetection(context.Background(), &pb.SoundDetectionRequest{Src: writeInput("clip.mp3", "ID3")})
		Expect(err).NotTo(HaveOccurred())
		Expect(name).To(Equal("clip.mp3"))
	})
})

func ptr[T any](v T) *T { return &v }
