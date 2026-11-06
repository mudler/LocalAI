package localaitools_test

import (
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"strings"
	"sync"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	localaitools "github.com/mudler/LocalAI/core/services/localaitools"
)

// wavBytes is a small valid RIFF/WAVE header with silence.
func wavBytes() []byte {
	data := make([]byte, 16000*2)
	b := []byte("RIFF")
	b = binary.LittleEndian.AppendUint32(b, uint32(36+len(data)))
	b = append(b, []byte("WAVEfmt ")...)
	b = binary.LittleEndian.AppendUint32(b, 16)
	b = binary.LittleEndian.AppendUint16(b, 1)
	b = binary.LittleEndian.AppendUint16(b, 1)
	b = binary.LittleEndian.AppendUint32(b, 16000)
	b = binary.LittleEndian.AppendUint32(b, 32000)
	b = binary.LittleEndian.AppendUint16(b, 2)
	b = binary.LittleEndian.AppendUint16(b, 16)
	b = append(b, []byte("data")...)
	b = binary.LittleEndian.AppendUint32(b, uint32(len(data)))
	return append(b, data...)
}

var _ = Describe("Inference voice profiles", func() {
	var (
		fs       *memStore
		inf      *localaitools.Inference
		mu       sync.Mutex
		requests []*http.Request
		fields   map[string]string
		answer   func(*http.Request) (int, string)
	)

	BeforeEach(func() {
		fs = newMemStore()
		fs.files["/files/voice.wav"] = localaitools.File{Name: "voice.wav", MIME: "audio/wav", Data: wavBytes()}
		fs.files["/files/voice.mp3"] = localaitools.File{Name: "voice.mp3", MIME: "audio/mpeg", Data: []byte("ID3\x03\x00\x00\x00\x00\x00\x00 not a wav")}
		requests, fields = nil, map[string]string{}
		answer = func(r *http.Request) (int, string) {
			switch r.Method {
			case http.MethodPost:
				return http.StatusCreated, `{"id":"11111111-1111-1111-1111-111111111111","name":"Narrator","voice":"localai://voice-profiles/11111111-1111-1111-1111-111111111111"}`
			case http.MethodGet:
				return http.StatusOK, `{"data":[{"id":"a","name":"Mine","owner_id":"u1","voice":"localai://voice-profiles/a"},{"id":"b","name":"Shared","voice":"localai://voice-profiles/b"}]}`
			}
			return http.StatusNoContent, ""
		}
		inf = &localaitools.Inference{
			Files: fs,
			Do: func(r *http.Request) (*http.Response, error) {
				mu.Lock()
				defer mu.Unlock()
				requests = append(requests, r)
				if mt, params, err := mime.ParseMediaType(r.Header.Get("Content-Type")); err == nil && mt == "multipart/form-data" {
					mr := multipart.NewReader(r.Body, params["boundary"])
					for {
						part, err := mr.NextPart()
						if err != nil {
							break
						}
						b, _ := io.ReadAll(part)
						if part.FileName() != "" {
							fields["file:"+part.FormName()] = part.FileName()
						} else {
							fields[part.FormName()] = string(b)
						}
					}
				}
				code, body := answer(r)
				return &http.Response{StatusCode: code, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(body)), Request: r}, nil
			},
		}
	})

	create := func(ctx context.Context, audio string) (*localaitools.VoiceProfile, error) {
		return inf.CreateVoiceProfile(ctx, localaitools.CreateVoiceProfileRequest{Name: "Narrator", Transcript: "Words.", Language: "en", Audio: audio})
	}

	It("creates nothing without the consent of a person, and sends no request", func() {
		_, err := create(context.Background(), "/files/voice.wav")
		Expect(err).To(MatchError(localaitools.ErrConsentNeeded))
		Expect(err.Error()).To(ContainSubstring("needs a person to confirm"))
		Expect(requests).To(BeEmpty())
	})

	It("states the consent to the API only after a person gave it, and returns the voice to pass to speak", func() {
		p, err := create(localaitools.WithVoiceConsent(context.Background()), "/files/voice.wav")
		Expect(err).NotTo(HaveOccurred())
		Expect(p.ID).To(Equal("11111111-1111-1111-1111-111111111111"))
		Expect(p.Voice).To(Equal("localai://voice-profiles/11111111-1111-1111-1111-111111111111"))
		Expect(requests).To(HaveLen(1))
		Expect(requests[0].Method).To(Equal(http.MethodPost))
		Expect(requests[0].URL.Path).To(Equal("/api/voice-profiles"))
		Expect(fields).To(HaveKeyWithValue("consent_confirmed", "true"))
		Expect(fields).To(HaveKeyWithValue("transcript", "Words."))
		Expect(fields).To(HaveKeyWithValue("language", "en"))
		Expect(fields).To(HaveKey("file:audio"))
	})

	It("answers with a clear error when the clip is not a WAV file, before it asks the API", func() {
		_, err := create(localaitools.WithVoiceConsent(context.Background()), "/files/voice.mp3")
		Expect(err).To(MatchError(localaitools.ErrNotWAV))
		Expect(err.Error()).To(ContainSubstring("must be a WAV file"))
		Expect(requests).To(BeEmpty())
	})

	It("validates the clip in check mode and creates nothing", func() {
		p, err := create(localaitools.WithVoiceCheck(context.Background()), "/files/voice.wav")
		Expect(err).NotTo(HaveOccurred())
		Expect(p.ID).To(BeEmpty())
		_, err = create(localaitools.WithVoiceCheck(context.Background()), "/files/voice.mp3")
		Expect(err).To(MatchError(localaitools.ErrNotWAV))
		Expect(requests).To(BeEmpty())
	})

	It("reports the SHA-256, size and duration of the clip in check mode", func() {
		p, err := create(localaitools.WithVoiceCheck(context.Background()), "/files/voice.wav")
		Expect(err).NotTo(HaveOccurred())
		sum := sha256.Sum256(wavBytes())
		Expect(p.Audio.SHA256).To(Equal(hex.EncodeToString(sum[:])))
		Expect(p.Audio.SizeBytes).To(Equal(int64(len(wavBytes()))))
		Expect(p.Audio.DurationMilliseconds).To(BeNumerically(">", 0))
	})

	It("creates the profile only when the clip is the one the person confirmed", func() {
		sum := sha256.Sum256(wavBytes())
		pin := hex.EncodeToString(sum[:])
		ctx := localaitools.WithVoiceClipPin(localaitools.WithVoiceConsent(context.Background()), pin, int64(len(wavBytes())))
		_, err := create(ctx, "/files/voice.wav")
		Expect(err).NotTo(HaveOccurred())
		Expect(requests).To(HaveLen(1))
	})

	It("refuses a clip that was swapped after the person confirmed, and sends no request", func() {
		sum := sha256.Sum256(wavBytes())
		pin := hex.EncodeToString(sum[:])
		other := wavBytes()
		other[len(other)-1] ^= 0xff // same size, other bytes
		fs.files["/files/voice.wav"] = localaitools.File{Name: "voice.wav", MIME: "audio/wav", Data: other}
		ctx := localaitools.WithVoiceClipPin(localaitools.WithVoiceConsent(context.Background()), pin, int64(len(wavBytes())))
		_, err := create(ctx, "/files/voice.wav")
		Expect(err).To(MatchError(localaitools.ErrClipChanged))
		Expect(requests).To(BeEmpty())
	})

	It("lists through the profile route and deletes by id", func() {
		ps, err := inf.ListVoiceProfiles(context.Background())
		Expect(err).NotTo(HaveOccurred())
		Expect(ps).To(HaveLen(2))
		Expect(inf.DeleteVoiceProfile(context.Background(), "a")).To(Succeed())
		Expect(requests[1].Method).To(Equal(http.MethodDelete))
		Expect(requests[1].URL.Path).To(Equal("/api/voice-profiles/a"))
	})

	It("words the refusals of the API for the model", func() {
		answer = func(*http.Request) (int, string) {
			return http.StatusNotFound, `{"error":{"message":"voice profile not found"}}`
		}
		err := inf.DeleteVoiceProfile(context.Background(), "x")
		Expect(err).To(MatchError(ContainSubstring("no such profile")))
		answer = func(*http.Request) (int, string) { return http.StatusForbidden, `{}` }
		err = inf.DeleteVoiceProfile(context.Background(), "x")
		Expect(err).To(MatchError(ContainSubstring("administrator")))
		answer = func(*http.Request) (int, string) {
			return http.StatusConflict, `{"error":{"message":"voice profile limit reached: at most 20 profiles per user"}}`
		}
		_, err = create(localaitools.WithVoiceConsent(context.Background()), "/files/voice.wav")
		Expect(err).To(MatchError(ContainSubstring("at most 20 profiles")))
	})
})
