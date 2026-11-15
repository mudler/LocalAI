package studio

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"unicode/utf8"

	mcpendpoint "github.com/mudler/LocalAI/core/http/endpoints/mcp"
	"github.com/mudler/LocalAI/core/services/localaitools"
	"github.com/mudler/LocalAI/core/services/studio/builtins"
	"github.com/mudler/LocalAI/core/services/studio/proposals"
	"github.com/mudler/LocalAI/core/services/studio/session"
	"github.com/mudler/LocalAI/pkg/addons/contract"
)

// Saving a voice profile with the voice_profile tool.
//
// Cloning a voice without the consent of the speaker is abuse, so the model
// cannot create a profile by itself. The create action of the tool becomes a
// change proposal of the kind voice_profile: the person sees a card with the
// name, the transcript and the file, and confirms that the speaker agreed. Only
// that confirmation runs the tool again, with the consent of a person in its
// context (localaitools.WithVoiceConsent), and the tool then states the consent
// to the API. A run that nobody watches, a sub-agent and a messaging chat have
// nobody who can confirm in Studio, so the action is refused there.

// voiceProfileContent is the content of a voice_profile proposal: the
// arguments of the create call.
type voiceProfileContent struct {
	Name       string `json:"name"`
	Audio      string `json:"audio"`
	Transcript string `json:"transcript"`
	Language   string `json:"language,omitempty"`
	// Description is a free-text label the person did not have to read to
	// consent: it names nothing that is cloned.
	Description string `json:"description,omitempty"`
	// ExtraClips are more clips of the same voice. The card lists them.
	ExtraClips []localaitools.VoiceClip `json:"extra_clips,omitempty"`
	// AudioSHA256, AudioBytes and AudioMS pin the clips the person was shown at
	// proposal time: for several clips the pin is localaitools.ClipsPin of
	// their digests, and the size and duration are the totals. The
	// confirmation refuses clips that differ.
	AudioSHA256 string `json:"audio_sha256,omitempty"`
	AudioBytes  int64  `json:"audio_bytes,omitempty"`
	AudioMS     int64  `json:"audio_ms,omitempty"`
}

// voiceProfileTarget is the target of a voice_profile proposal; it names no
// file, as the profile goes to the voice library.
const voiceProfileTarget = "voice-profiles"

// maxVoiceProfileContent bounds the content: a name, a description, up to ten
// paths and transcripts of at most 4000 characters each, and the pin of the
// clips.
const maxVoiceProfileContent = 64 << 10

func (c *Changes) voiceProfileKind() proposals.Kind {
	s := c.s
	return proposals.Kind{
		Name:      contract.ChangeVoiceProfile,
		Sensitive: true,
		Internal:  true,
		MaxBytes:  maxVoiceProfileContent,
		Path: func(a proposals.Aim) (string, error) {
			if a.Scope != contract.ScopeUser {
				return "", fmt.Errorf("%w: a voice profile belongs to the user: use scope user", proposals.ErrBadTarget)
			}
			return voiceProfileTarget, nil
		},
		Check: func(in proposals.CheckInput) error {
			_, err := parseVoiceProfileContent(in.Content)
			return err
		},
		Dest: &proposals.Destination{
			Prepare: func(_ context.Context, _ proposals.ApplyInput) error {
				if s.d.ClientTools == nil || s.d.ClientTools.ClientExecutor() == nil {
					return errors.New("the LocalAI client tools are not available on this server")
				}
				return nil
			},
			Apply: func(ctx context.Context, in proposals.ApplyInput) error {
				return s.applyVoiceProfile(ctx, in)
			},
			Undo: func(context.Context, proposals.ApplyInput) error {
				return errors.New("delete the voice profile with voice_profile or in the Voice library")
			},
		},
	}
}

func parseVoiceProfileContent(content string) (voiceProfileContent, error) {
	var in voiceProfileContent
	if err := json.Unmarshal([]byte(content), &in); err != nil {
		return in, errors.New("the content of a voice profile is JSON with name, audio and transcript")
	}
	switch {
	case strings.TrimSpace(in.Name) == "" || strings.TrimSpace(in.Audio) == "" || strings.TrimSpace(in.Transcript) == "":
		return in, errors.New("a voice profile needs name, audio and transcript")
	case len(in.ExtraClips) > 0 && hasBlankClip(in.ExtraClips):
		return in, errors.New("every extra clip needs an audio file and a transcript")
	case !utf8.ValidString(content):
		return in, errors.New("the content must be text")
	}
	return in, nil
}

// applyVoiceProfile creates the profile after the person confirmed. It runs
// the tool as the owner of the proposal, with their consent in the context.
func (s *Service) applyVoiceProfile(ctx context.Context, in proposals.ApplyInput) error {
	if s.d.ClientTools == nil {
		return errors.New("the LocalAI client tools are not available on this server")
	}
	c, err := parseVoiceProfileContent(in.Proposal.Content)
	if err != nil {
		return err
	}
	owner, err := s.ownerOf(ctx, in.Owner)
	if err != nil {
		return fmt.Errorf("nothing was created: %w", err)
	}
	if c.AudioSHA256 == "" || c.AudioBytes <= 0 {
		return errors.New("nothing was created: the proposal does not pin its recording; propose the profile again")
	}
	// Read the clip through the fence of the chat that made the proposal, as
	// the proposal did: an executor without a file store would fall back to
	// the whole home of the owner. A chat that is gone, or whose agent folder
	// is missing, means nothing is read.
	x, err := s.ChatAccess(ctx, in.Owner, in.Proposal.Source.Chat)
	if err != nil {
		return fmt.Errorf("nothing was created: the chat that proposed the profile is not available (%v); propose the profile again", err)
	}
	exec := mcpendpoint.WithFiles(mcpendpoint.AsUser(s.d.ClientTools.ClientExecutor(), owner), s.clientFilesOf(x))
	if exec == nil {
		return errors.New("the LocalAI client tools are not available on this server")
	}
	args, _ := json.Marshal(localaitools.VoiceProfileToolInput{Action: localaitools.ActionCreate, Name: c.Name, Audio: c.Audio,
		Transcript: c.Transcript, Language: c.Language, Description: c.Description, ExtraClips: c.ExtraClips})
	out, err := exec.ExecuteTool(localaitools.WithVoiceClipPin(localaitools.WithVoiceConsent(ctx), c.AudioSHA256, c.AudioBytes), localaitools.ToolVoiceProfile, string(args))
	if err != nil {
		return err
	}
	var made localaitools.VoiceProfileCreated
	if json.Unmarshal([]byte(out), &made) != nil || made.ID == "" {
		return errors.New(strings.TrimSpace(out))
	}
	s.log.Info("studio: voice profile created after a confirmation", "owner", in.Owner, "proposal", in.Proposal.ID, "profile", made.ID)
	return nil
}

var (
	errMessagingVoiceDelete = errors.New("voice_profile delete is not available in a messaging chat: the person writing may not be the owner; delete the profile in Studio")
	errMessagingIdentify    = errors.New("identify reads the registry of known people and is not available in a messaging chat: the person writing may not be the owner")
)

// guardMessagingRegistry makes the identify action of voice and face fail in a
// messaging chat. Identify searches the registry of people of the whole
// server, and whoever writes in a messaging chat may not be the owner. verify
// compares two inputs the person gave and stays available.
func guardMessagingRegistry(tools []session.Tool, chat builtins.Chat) []session.Tool {
	if chat.Origin != session.OriginConnector {
		return tools
	}
	for i, t := range tools {
		if t.Name != localaitools.ToolVoice && t.Name != localaitools.ToolFace {
			continue
		}
		run := t.Run
		tools[i].Run = func(ctx context.Context, args json.RawMessage) (string, error) {
			var in struct {
				Action string `json:"action"`
			}
			if json.Unmarshal(args, &in) == nil && in.Action == localaitools.ActionIdentify {
				return "", errMessagingIdentify
			}
			return run(ctx, args)
		}
	}
	return tools
}

// guardVoiceProfile replaces the Run of the voice_profile tool so that its
// create action goes through a confirmation. The other actions run as they are,
// within the rights of the user. exec runs the tool as the owner of the chat.
func (s *Service) guardVoiceProfile(tools []session.Tool, chat builtins.Chat, exec mcpendpoint.ToolExecutor) []session.Tool {
	for i, t := range tools {
		if t.Name != localaitools.ToolVoiceProfile {
			continue
		}
		run := t.Run
		tools[i].Run = func(ctx context.Context, args json.RawMessage) (string, error) {
			var in localaitools.VoiceProfileToolInput
			if json.Unmarshal(args, &in) == nil && in.Action == localaitools.ActionDelete && chat.Origin == session.OriginConnector {
				// The person who writes in a messaging chat may not be the
				// owner of the voice library, and nobody can confirm there.
				return "", errMessagingVoiceDelete
			}
			if json.Unmarshal(args, &in) != nil || in.Action != localaitools.ActionCreate {
				return run(ctx, args)
			}
			return s.proposeVoiceProfile(ctx, chat, exec, in, args)
		}
	}
	return tools
}

// voiceProfileSummary names every clip, so the line of a proposal does not
// hide the extra ones.
func voiceProfileSummary(in localaitools.VoiceProfileToolInput) string {
	if len(in.ExtraClips) == 0 {
		return fmt.Sprintf("Clone a voice as %q from %s", in.Name, in.Audio)
	}
	files := []string{in.Audio}
	for _, c := range in.ExtraClips {
		files = append(files, c.Audio)
	}
	return fmt.Sprintf("Clone a voice as %q from %d clips: %s", in.Name, len(files), strings.Join(files, ", "))
}

// proposeVoiceProfile turns a create call into a proposal. Nothing is created.
func (s *Service) proposeVoiceProfile(ctx context.Context, chat builtins.Chat, exec mcpendpoint.ToolExecutor, in localaitools.VoiceProfileToolInput, args json.RawMessage) (string, error) {
	switch {
	case chat.SubAgent:
		return "Not created: a sub-agent has nobody to confirm that the speaker agreed. Tell the parent task; do not try again.", nil
	case unattended(chat.Origin):
		return "Not created: this run started by itself and nobody can confirm that the speaker agreed to the cloning of their voice. Do not try again; say that a person must do it in a chat they watch.", nil
	case chat.Origin != "":
		return "Not created: the confirmation that the speaker agreed is given in Studio, not in a messaging chat. Tell the person to ask for it in Studio; do not try again here.", nil
	}
	// Reject a bad request, such as a clip that is not WAV, before a person is asked.
	checked, err := exec.ExecuteTool(localaitools.WithVoiceCheck(ctx), localaitools.ToolVoiceProfile, string(args))
	if err != nil {
		return "", err
	}
	var ok localaitools.VoiceClipCheck
	if json.Unmarshal([]byte(checked), &ok) != nil || !ok.Checked || ok.SHA256 == "" || ok.Bytes <= 0 {
		return "", errors.New(strings.TrimSpace(checked))
	}
	content, _ := json.Marshal(voiceProfileContent{Name: strings.TrimSpace(in.Name), Audio: strings.TrimSpace(in.Audio),
		Transcript: strings.TrimSpace(in.Transcript), Language: strings.TrimSpace(in.Language),
		Description: strings.TrimSpace(in.Description), ExtraClips: in.ExtraClips,
		AudioSHA256: ok.SHA256, AudioBytes: ok.Bytes, AudioMS: ok.DurationMS})
	p, err := s.changes.Propose(ctx, ProposeInput{User: chat.Actor.Principal, Agent: chat.AgentID,
		Source: contract.ChangeSource{Chat: chat.Actor.ChatID}, Kind: string(contract.ChangeVoiceProfile), Scope: string(contract.ScopeUser),
		Content: string(content), Summary: voiceProfileSummary(in)})
	if err != nil {
		return "", changeErr(err)
	}
	session.SetToolCard(ctx, session.ToolCard{Kind: ChangeProposalCardKind, ID: p.ID})
	return fmt.Sprintf("Proposal %s stored. Nothing is created yet: the person must confirm in Studio that the speaker agreed to the cloning of their voice, and it expires in 24 hours. "+
		"Do not say the voice exists. After they confirm, call voice_profile with action list to get the voice to give to speak.", p.ID), nil
}

func hasBlankClip(clips []localaitools.VoiceClip) bool {
	for _, c := range clips {
		if strings.TrimSpace(c.Audio) == "" || strings.TrimSpace(c.Transcript) == "" {
			return true
		}
	}
	return false
}
