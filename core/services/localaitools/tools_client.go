package localaitools

import (
	"context"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// registerClientTools wires the client tools: inference that any user may run
// within their own permissions. They are a separate group from the manage
// tools (see ToolGroupOf) and are never gated by Options.DisableMutating,
// which is about changing the instance.
func registerClientTools(s *mcp.Server, client LocalAIClient, _ Options) {
	addClientTool(s, ToolGenerateImage,
		"Generate one or more images from a text prompt. The images are saved as files; the result gives a file reference, the size and the dimensions of each.",
		client.GenerateImage)
	addClientTool(s, ToolEditImage,
		"Repaint part of an image: the white area of the mask is painted again following the prompt. Takes file references for the image and the mask and saves the result as a file.",
		client.EditImage)
	addClientTool(s, ToolSpeak,
		"Turn text into speech and save it as an audio file. To speak in a cloned voice, pass a saved voice profile URI from list_voice_profiles as voice.",
		client.Speak)
	addClientTool(s, ToolTranscribeAudio,
		"Turn a recording into text. Takes a file reference to the audio.",
		client.TranscribeAudio)
	addClientTool(s, ToolDescribeImage,
		"Describe an image, or answer a question about it, with a model that can look at images. Reads text in the image too. Takes a file reference.",
		client.DescribeImage)
	addClientTool(s, ToolEmbed,
		"Turn texts into embedding vectors. The vectors are saved as a JSON file; the result gives its reference, the dimensions and the first values.",
		client.Embed)
	addClientTool(s, ToolDetect,
		"Find objects in an image and return their boxes, classes and confidence. Takes a file reference.",
		client.Detect)
	addClientTool(s, ToolRerank,
		"Rank documents by how well they answer a query. Returns the documents' positions and scores, best first.",
		client.Rerank)
	addClientTool(s, ToolGenerateVideo,
		"Generate a video from a text prompt, optionally starting from an image. The video is saved as a file. Generation can take minutes.",
		client.GenerateVideo)
	addClientTool(s, ToolGenerateSound,
		"Generate a sound effect or a piece of music from a description. The audio is saved as a file.",
		client.GenerateSound)
	addClientTool(s, ToolVerifyVoice,
		"Tell whether two recordings are of the same speaker. Takes file references.",
		client.VerifyVoice)
	addClientTool(s, ToolIdentifyVoice,
		"Find which registered speakers a recording is closest to. Takes a file reference.",
		client.IdentifyVoice)
	addClientTool(s, ToolVerifyFace,
		"Tell whether two images show the same person. Takes file references.",
		client.VerifyFace)
	addClientTool(s, ToolIdentifyFace,
		"Find which registered faces the face in an image is closest to. Takes a file reference.",
		client.IdentifyFace)
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
