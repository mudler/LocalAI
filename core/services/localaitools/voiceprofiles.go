package localaitools

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"

	"github.com/mudler/LocalAI/core/services/voiceprofile"
)

// ErrConsentNeeded means a create call came without the confirmation of a
// person. The model cannot give it: the host asks the person in a chat they
// watch and then calls the tool with WithVoiceConsent.
var ErrConsentNeeded = errors.New("voice_profile: action \"create\" needs a person to confirm that the speaker agreed to the cloning of their voice; the chat asks them. It cannot be done from here")

// ErrNotWAV means the clip of a profile is not a WAV file.
// ErrClipChanged means the clip is not the one the person confirmed.
var ErrClipChanged = errors.New("voice_profile: the clip changed after the person confirmed it (its SHA-256 or size differs); nothing was created. Propose the profile again")

// VoiceClipCheck is the result of a check-only create call: what the person is
// asked to confirm. SHA256 is the lowercase hex digest of the clip.
type VoiceClipCheck struct {
	Checked    bool   `json:"checked"`
	SHA256     string `json:"sha256,omitempty"`
	Bytes      int64  `json:"bytes,omitempty"`
	DurationMS int64  `json:"duration_ms,omitempty"`
}

var ErrNotWAV = errors.New("voice_profile: the clip must be a WAV file (16-bit PCM, 1 to 120 seconds); this file is not WAV. Convert it to WAV first, for example with a command that runs ffmpeg")

func voiceProfileAction(ctx context.Context, client LocalAIClient, in VoiceProfileToolInput) (any, error) {
	switch in.Action {
	case ActionCreate:
		if err := need(ToolVoiceProfile, in.Action,
			[2]string{"name", in.Name}, [2]string{"audio", in.Audio}, [2]string{"transcript", in.Transcript}); err != nil {
			return nil, err
		}
		p, err := client.CreateVoiceProfile(ctx, CreateVoiceProfileRequest{
			Name: in.Name, Language: in.Language, Transcript: in.Transcript, Audio: in.Audio,
		})
		if err != nil {
			return nil, err
		}
		if p == nil || p.ID == "" {
			// A check-only call (see WithVoiceCheck) validated and created nothing.
			return VoiceClipCheck{Checked: true, SHA256: p.Audio.SHA256, Bytes: p.Audio.SizeBytes, DurationMS: p.Audio.DurationMilliseconds}, nil
		}
		return VoiceProfileCreated{ID: p.ID, Name: p.Name, Voice: p.Voice, Note: VoiceProfileVisibilityNote}, nil
	case ActionList:
		ps, err := client.ListVoiceProfiles(ctx)
		if err != nil {
			return nil, err
		}
		out := make([]VoiceProfileSummary, 0, len(ps))
		for _, p := range ps {
			out = append(out, VoiceProfileSummary{ID: p.ID, Name: p.Name, Language: p.Language, Voice: p.Voice,
				Shared: p.OwnerID == "", DurationMS: p.Audio.DurationMilliseconds})
		}
		return VoiceProfileList{Profiles: out, Note: VoiceProfileVisibilityNote}, nil
	case ActionDelete:
		if err := need(ToolVoiceProfile, in.Action, [2]string{"id", in.ID}); err != nil {
			return nil, err
		}
		if err := client.DeleteVoiceProfile(ctx, strings.TrimSpace(in.ID)); err != nil {
			return nil, err
		}
		return map[string]any{"deleted": true, "id": in.ID}, nil
	}
	if in.Action == "" {
		return nil, errors.New("voice_profile: action is required (create, list or delete)")
	}
	return nil, fmt.Errorf("voice_profile: unknown action %q (use create, list or delete)", in.Action)
}

// isWAV reports whether data starts like a RIFF/WAVE file.
func isWAV(data []byte) bool {
	return len(data) >= 12 && bytes.Equal(data[0:4], []byte("RIFF")) && bytes.Equal(data[8:12], []byte("WAVE"))
}

// ListVoiceProfiles lists the profiles the acting user may use (GET /api/voice-profiles).
func (i *Inference) ListVoiceProfiles(ctx context.Context) ([]VoiceProfile, error) {
	spec := clientSpecAction(ToolVoiceProfile, ActionList)
	data, err := i.call(ctx, spec, http.MethodGet, spec.Route, "", nil)
	if err != nil {
		return nil, err
	}
	var out struct {
		Data []VoiceProfile `json:"data"`
	}
	if err := json.Unmarshal(data, &out); err != nil {
		return nil, fmt.Errorf("read the voice profiles: %w", err)
	}
	return out.Data, nil
}

// CreateVoiceProfile saves a voice profile from a WAV clip (POST
// /api/voice-profiles). The caller must hold the consent of a person
// (WithVoiceConsent): the request states the consent to the API, so it must
// never be sent on the word of a model. With WithVoiceCheck the input and the
// clip are validated and nothing is sent; the result then has no ID.
func (i *Inference) CreateVoiceProfile(ctx context.Context, req CreateVoiceProfileRequest) (*VoiceProfile, error) {
	spec := clientSpecAction(ToolVoiceProfile, ActionCreate)
	g := voiceGrantFrom(ctx)
	if !g.consent && !g.checkOnly {
		return nil, ErrConsentNeeded
	}
	for _, f := range [][2]string{{"name", req.Name}, {"transcript", req.Transcript}} {
		if strings.TrimSpace(f[1]) == "" {
			return nil, fmt.Errorf("voice_profile: action \"create\" needs %s", f[0])
		}
	}
	clip, err := i.read(ctx, req.Audio, int64(voiceprofile.MaxAudioBytes), "audio")
	if err != nil {
		return nil, err
	}
	if !isWAV(clip.Data) {
		return nil, ErrNotWAV
	}
	sum := sha256.Sum256(clip.Data)
	digest := hex.EncodeToString(sum[:])
	if g.pinSHA256 != "" && (digest != g.pinSHA256 || int64(len(clip.Data)) != g.pinBytes) {
		return nil, ErrClipChanged
	}
	if g.checkOnly && !g.consent {
		meta, err := voiceprofile.InspectWAV(clip.Data)
		if err != nil {
			return nil, fmt.Errorf("voice_profile: the clip is not usable: %w", err)
		}
		meta.SHA256 = digest
		return &VoiceProfile{Audio: meta}, nil
	}
	m := newMultipart()
	m.field("name", req.Name)
	m.field("description", req.Description)
	m.field("language", req.Language)
	m.field("transcript", req.Transcript)
	m.field("consent_confirmed", "true")
	clip.MIME = "audio/wav"
	if err := m.file("audio", clip, "reference.wav"); err != nil {
		return nil, err
	}
	body, contentType, err := m.finish()
	if err != nil {
		return nil, err
	}
	data, err := i.call(ctx, spec, http.MethodPost, spec.Route, contentType, body)
	if err != nil {
		return nil, err
	}
	var p VoiceProfile
	if err := json.Unmarshal(data, &p); err != nil {
		return nil, fmt.Errorf("read the new voice profile: %w", err)
	}
	return &p, nil
}

// DeleteVoiceProfile deletes a profile (DELETE /api/voice-profiles/:id). The
// API answers 404 for a profile of another user.
func (i *Inference) DeleteVoiceProfile(ctx context.Context, id string) error {
	spec := clientSpecAction(ToolVoiceProfile, ActionDelete)
	_, err := i.call(ctx, spec, http.MethodDelete, "/api/voice-profiles/"+url.PathEscape(id), "", nil)
	return err
}

// call sends a request with any method to a route and returns the answer body.
func (i *Inference) call(ctx context.Context, spec ClientToolSpec, method, route, contentType string, body []byte) ([]byte, error) {
	if i == nil || i.Do == nil {
		return nil, errors.New("voice profiles are not available on this server")
	}
	req, err := http.NewRequestWithContext(ctx, method, PlaceholderHost+route, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	data, _, err := i.roundTrip(req, spec, "")
	var se *StatusError
	if errors.As(err, &se) {
		return nil, voiceProfileStatus(se)
	}
	return data, err
}

// voiceProfileStatus words an API refusal for the model. These routes have no
// model, so the generic text about a model would mislead.
func voiceProfileStatus(e *StatusError) error {
	switch e.Status {
	case http.StatusForbidden:
		return errors.New("voice_profile: the server refused it (403): this account may not use speech, or the profile is shared by the whole instance and only an administrator can delete it")
	case http.StatusNotFound:
		return errors.New("voice_profile: no such profile (404). list shows the profiles of this account")
	case http.StatusConflict:
		return fmt.Errorf("voice_profile: %s. Delete a profile you no longer need first", e.Detail)
	case http.StatusBadRequest, http.StatusRequestEntityTooLarge:
		return fmt.Errorf("voice_profile: the server rejected the profile: %s", e.Detail)
	}
	return e
}
