package localaitools

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/google/jsonschema-go/jsonschema"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// registerClientTools wires the client tools: inference that any user may run
// within their own permissions. They are a separate group from the manage
// tools (see ToolGroupOf) and are not gated by Options.DisableMutating as a
// group, which is about changing the instance. The read-only flavour of the server
// still drops the ones that write files or make content (clientToolWrites).
func registerClientTools(s *mcp.Server, client LocalAIClient, opts Options) {
	// Which tools a flavour of the server keeps: the read-only one drops every
	// tool that writes a file, makes new content or changes the voice
	// library; a server without a files folder drops the ones that need files.
	keep := func(tool string) bool {
		if opts.DisableMutating && clientToolWrites[tool] {
			return false
		}
		return !(opts.DisableClientFiles && clientToolFiles[tool])
	}
	if keep(ToolGenerateImage) {
		addClientTool(s, ToolGenerateImage,
			"Make images from a prompt, or with image and mask repaint the masked area. Saves files.",
			func(ctx context.Context, in GenerateImageToolInput) (*ImageResult, error) {
				return generateImage(ctx, client, in)
			})
	}
	if keep(ToolSpeak) {
		addClientTool(s, ToolSpeak,
			"Turn text into speech. voice can be a profile from voice_profile. Saves an audio file.",
			client.Speak)
	}
	if keep(ToolTranscribeAudio) {
		addClientTool(s, ToolTranscribeAudio,
			"Turn a recording into text.",
			client.TranscribeAudio)
	}
	if keep(ToolDescribeImage) {
		addClientTool(s, ToolDescribeImage,
			"Describe an image or answer a question about it, including text in it.",
			client.DescribeImage)
	}
	if keep(ToolEmbed) {
		addClientTool(s, ToolEmbed,
			"Turn texts into embedding vectors. Saves them as a JSON file.",
			client.Embed)
	}
	if keep(ToolDetect) {
		addClientTool(s, ToolDetect,
			"Find objects in an image: boxes, classes, confidence.",
			client.Detect)
	}
	if keep(ToolRerank) {
		addClientTool(s, ToolRerank,
			"Rank documents by how well they answer a query.",
			client.Rerank)
	}
	if keep(ToolGenerateVideo) {
		addClientTool(s, ToolGenerateVideo,
			"Generate a video from a prompt, optionally from an image. Can take minutes. Saves a file.",
			client.GenerateVideo)
	}
	if keep(ToolGenerateSound) {
		addClientTool(s, ToolGenerateSound,
			"Generate a sound effect or music from a description. Saves an audio file.",
			client.GenerateSound)
	}
	if keep(ToolVoice) {
		addActionTool(s, ToolVoice,
			"Speaker recognition: verify two recordings, or identify a speaker.",
			func(ctx context.Context, in VoiceToolInput) (any, error) { return voiceAction(ctx, client, in) },
			ActionVerify, ActionIdentify)
	}
	if keep(ToolFace) {
		addActionTool(s, ToolFace,
			"Face recognition: verify two images, or identify a face.",
			func(ctx context.Context, in FaceToolInput) (any, error) { return faceAction(ctx, client, in) },
			ActionVerify, ActionIdentify)
	}
	// voice_profile keeps only the actions the flavour allows: create needs a
	// file and writes data, delete changes data, list is always safe.
	actions := []string{ActionList}
	if !opts.DisableMutating {
		if !opts.DisableClientFiles {
			actions = append([]string{ActionCreate}, actions...)
		}
		actions = append(actions, ActionDelete)
	}
	addActionTool(s, ToolVoiceProfile,
		"Voices to clone with speak. create saves a WAV clip; the person must confirm the speaker agreed. Profiles made by an administrator are visible to all users.",
		func(ctx context.Context, in VoiceProfileToolInput) (any, error) {
			if !slices.Contains(actions, in.Action) {
				return nil, fmt.Errorf("voice_profile: action %q is not available on this server (use %s)", in.Action, strings.Join(actions, ", "))
			}
			return voiceProfileAction(ctx, client, in)
		},
		actions...)
}

// clientToolWrites lists the client tools the read-only server leaves out: they
// write a file or spend compute to make new content.
var clientToolWrites = map[string]bool{
	ToolGenerateImage: true, ToolSpeak: true, ToolEmbed: true, ToolGenerateVideo: true, ToolGenerateSound: true,
}

// clientToolFiles lists the client tools that read or write files, so they need
// a files folder.
var clientToolFiles = map[string]bool{
	ToolGenerateImage: true, ToolSpeak: true, ToolTranscribeAudio: true, ToolDescribeImage: true, ToolEmbed: true,
	ToolDetect: true, ToolGenerateVideo: true, ToolGenerateSound: true, ToolVoice: true, ToolFace: true,
}

// generateImage serves generate_image: the inpainting route when the call
// names an image, the generations route otherwise.
func generateImage(ctx context.Context, client LocalAIClient, in GenerateImageToolInput) (*ImageResult, error) {
	if in.Image == "" {
		if in.Mask != "" {
			return nil, errors.New("generate_image: mask needs image; give both to edit an image, or neither to make a new one")
		}
		return client.GenerateImage(ctx, GenerateImageRequest{
			Prompt: in.Prompt, Model: in.Model, Size: in.Size,
			NegativePrompt: in.NegativePrompt, Steps: in.Steps, N: in.N,
		})
	}
	if in.Mask == "" {
		return nil, errors.New("generate_image: mask is required when image is set")
	}
	return client.EditImage(ctx, EditImageRequest{
		Prompt: in.Prompt, Image: in.Image, Mask: in.Mask, Model: in.Model, Steps: in.Steps,
	})
}

// need names the first empty required field of an action, as an error that
// says the tool, the action and the field.
func need(tool, action string, fields ...[2]string) error {
	for _, f := range fields {
		if strings.TrimSpace(f[1]) == "" {
			return fmt.Errorf("%s: action %q needs %s", tool, action, f[0])
		}
	}
	return nil
}

func voiceAction(ctx context.Context, client LocalAIClient, in VoiceToolInput) (any, error) {
	switch in.Action {
	case ActionVerify:
		if err := need(ToolVoice, in.Action, [2]string{"audio", in.Audio}, [2]string{"audio2", in.Audio2}); err != nil {
			return nil, err
		}
		return client.VerifyVoice(ctx, VoiceVerifyRequest{Audio1: in.Audio, Audio2: in.Audio2, Model: in.Model, Threshold: in.Threshold})
	case ActionIdentify:
		if err := need(ToolVoice, in.Action, [2]string{"audio", in.Audio}); err != nil {
			return nil, err
		}
		return client.IdentifyVoice(ctx, IdentifyVoiceRequest{Audio: in.Audio, Model: in.Model, TopK: in.TopK, Threshold: in.Threshold})
	}
	return nil, badAction(ToolVoice, in.Action)
}

func faceAction(ctx context.Context, client LocalAIClient, in FaceToolInput) (any, error) {
	switch in.Action {
	case ActionVerify:
		if err := need(ToolFace, in.Action, [2]string{"image", in.Image}, [2]string{"image2", in.Image2}); err != nil {
			return nil, err
		}
		return client.VerifyFace(ctx, VerifyFaceRequest{Image1: in.Image, Image2: in.Image2, Model: in.Model, Threshold: in.Threshold})
	case ActionIdentify:
		if err := need(ToolFace, in.Action, [2]string{"image", in.Image}); err != nil {
			return nil, err
		}
		return client.IdentifyFace(ctx, IdentifyFaceRequest{Image: in.Image, Model: in.Model, TopK: in.TopK, Threshold: in.Threshold})
	}
	return nil, badAction(ToolFace, in.Action)
}

func badAction(tool, action string) error {
	if action == "" {
		return fmt.Errorf("%s: action is required (verify or identify)", tool)
	}
	return fmt.Errorf("%s: unknown action %q (use verify or identify)", tool, action)
}

// addClientTool registers a typed tool whose handler calls one client method
// and returns the result as JSON.
func addClientTool[In, Out any](s *mcp.Server, name, description string, call func(context.Context, In) (*Out, error)) {
	mcp.AddTool(s, &mcp.Tool{Name: name, Description: description},
		func(ctx context.Context, _ *mcp.CallToolRequest, args In) (*mcp.CallToolResult, any, error) {
			out, err := call(ctx, args)
			if err != nil {
				return errorResult(err), nil, nil
			}
			return jsonResult(out), nil, nil
		})
}

// addActionTool registers a tool with an action parameter. The schema stays
// one flat object (many MCP clients and model APIs reject a top-level
// oneOf/anyOf), with the action as an enum; the handler checks the fields
// each action needs.
func addActionTool[In any](s *mcp.Server, name, description string, call func(context.Context, In) (any, error), actions ...string) {
	schema, err := jsonschema.For[In](nil)
	if err != nil {
		panic(fmt.Sprintf("localaitools: schema of %s: %v", name, err))
	}
	if a := schema.Properties["action"]; a != nil {
		for _, name := range actions {
			a.Enum = append(a.Enum, name)
		}
	}
	mcp.AddTool(s, &mcp.Tool{Name: name, Description: description, InputSchema: schema},
		func(ctx context.Context, _ *mcp.CallToolRequest, args In) (*mcp.CallToolResult, any, error) {
			out, err := call(ctx, args)
			if err != nil {
				return errorResult(err), nil, nil
			}
			return jsonResult(out), nil, nil
		})
}
