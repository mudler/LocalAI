package localaitools

// DTOs of the client tools: the inference operations that any user may run
// within their own permissions. File arguments are references that the
// FileStore resolves (a path in the user's files, or a data URL); a file
// result is a reference too. No tool carries file bytes.

// OutputFile describes a file a tool wrote. Ref is what the next tool takes
// as a file argument.
type OutputFile struct {
	Ref    string `json:"ref"              jsonschema:"File reference. Pass it to another tool."`
	MIME   string `json:"mime,omitempty"`
	Size   int    `json:"size_bytes"`
	Width  int    `json:"width,omitempty"`
	Height int    `json:"height,omitempty"`
}

// GenerateImageRequest is the input for generate_image.
type GenerateImageRequest struct {
	Prompt         string `json:"prompt"                    jsonschema:"What the image shows."`
	Model          string `json:"model,omitempty"           jsonschema:"Model; default first installed."`
	Size           string `json:"size,omitempty"            jsonschema:"WIDTHxHEIGHT, for example 512x512."`
	NegativePrompt string `json:"negative_prompt,omitempty" jsonschema:"What to leave out."`
	Steps          int    `json:"steps,omitempty"`
	N              int    `json:"n,omitempty"               jsonschema:"1 to 4. Default 1."`
}

// ImageResult is the result of generate_image.
type ImageResult struct {
	Model  string       `json:"model"`
	Images []OutputFile `json:"images"`
}

// EditImageRequest is the input for generate_image when it gets an image.
type EditImageRequest struct {
	Prompt string `json:"prompt"          jsonschema:"What to paint."`
	Image  string `json:"image"           jsonschema:"Image file."`
	Mask   string `json:"mask"            jsonschema:"Mask file. White is repainted."`
	Model  string `json:"model,omitempty" jsonschema:"Model; default first installed."`
	Steps  int    `json:"steps,omitempty"`
}

// SpeakRequest is the input for speak.
type SpeakRequest struct {
	Text         string   `json:"text"`
	Model        string   `json:"model,omitempty"        jsonschema:"Model; default first installed."`
	Voice        string   `json:"voice,omitempty"        jsonschema:"Speaker id, or the voice of a voice_profile."`
	Language     string   `json:"language,omitempty"`
	Format       string   `json:"format,omitempty"       jsonschema:"wav, mp3, opus or flac."`
	Speed        *float32 `json:"speed,omitempty"        jsonschema:"0.25 to 4.0."`
	Instructions string   `json:"instructions,omitempty" jsonschema:"Voice style."`
}

// AudioResult is the result of speak and generate_sound.
type AudioResult struct {
	Model string     `json:"model"`
	Audio OutputFile `json:"audio"`
}

// TranscribeAudioRequest is the input for transcribe_audio.
type TranscribeAudioRequest struct {
	Audio    string `json:"audio"              jsonschema:"Audio file."`
	Model    string `json:"model,omitempty"    jsonschema:"Model; default first installed."`
	Language string `json:"language,omitempty"`
	Prompt   string `json:"prompt,omitempty"   jsonschema:"Expected words."`
}

// TranscriptResult is the result of transcribe_audio.
type TranscriptResult struct {
	Model    string  `json:"model"`
	Text     string  `json:"text"`
	Language string  `json:"language,omitempty"`
	Duration float64 `json:"duration_seconds,omitempty"`
}

// DescribeImageRequest is the input for describe_image.
type DescribeImageRequest struct {
	Image    string `json:"image"              jsonschema:"Image file."`
	Question string `json:"question,omitempty" jsonschema:"Question; default full description."`
	Model    string `json:"model,omitempty"    jsonschema:"Vision model. Empty picks one."`
}

// DescribeImageResult is the result of describe_image.
type DescribeImageResult struct {
	Model       string `json:"model"`
	Description string `json:"description"`
}

// EmbedRequest is the input for embed.
type EmbedRequest struct {
	Input []string `json:"input"           jsonschema:"The texts to embed."`
	Model string   `json:"model,omitempty" jsonschema:"Model; default first installed."`
}

// EmbedResult is the result of embed. The vectors go to a JSON file, because
// they are too long for a tool result.
type EmbedResult struct {
	Model      string      `json:"model"`
	Count      int         `json:"count"`
	Dimensions int         `json:"dimensions"`
	Vectors    OutputFile  `json:"vectors"           jsonschema:"JSON file, one vector per text."`
	Preview    [][]float32 `json:"preview,omitempty" jsonschema:"First values of each vector."`
}

// DetectRequest is the input for detect.
type DetectRequest struct {
	Image     string  `json:"image"               jsonschema:"Image file."`
	Model     string  `json:"model,omitempty"     jsonschema:"Model; default first installed."`
	Prompt    string  `json:"prompt,omitempty"    jsonschema:"What to find, if the model takes a prompt."`
	Threshold float32 `json:"threshold,omitempty" jsonschema:"Minimum confidence, 0 to 1."`
}

// Detection is one object found by detect.
type Detection struct {
	X          float32 `json:"x"`
	Y          float32 `json:"y"`
	Width      float32 `json:"width"`
	Height     float32 `json:"height"`
	ClassName  string  `json:"class_name"`
	Confidence float32 `json:"confidence,omitempty"`
	HasMask    bool    `json:"has_mask,omitempty"`
}

// DetectResult is the result of detect.
type DetectResult struct {
	Model      string      `json:"model"`
	Detections []Detection `json:"detections"`
}

// RerankRequest is the input for rerank.
type RerankRequest struct {
	Query     string   `json:"query"`
	Documents []string `json:"documents"`
	TopN      int      `json:"top_n,omitempty"  jsonschema:"Return only the best N."`
	Model     string   `json:"model,omitempty"  jsonschema:"Model; default first installed."`
}

// RerankItem is one ranked document.
type RerankItem struct {
	Index int     `json:"index" jsonschema:"Position in the input."`
	Score float64 `json:"score"`
	Text  string  `json:"text,omitempty"`
}

// RerankResult is the result of rerank, best first.
type RerankResult struct {
	Model   string       `json:"model"`
	Results []RerankItem `json:"results"`
}

// GenerateVideoRequest is the input for generate_video.
type GenerateVideoRequest struct {
	Prompt         string `json:"prompt"`
	Model          string `json:"model,omitempty"           jsonschema:"Model; default first installed."`
	Size           string `json:"size,omitempty"            jsonschema:"WIDTHxHEIGHT."`
	Seconds        string `json:"seconds,omitempty"         jsonschema:"Length in seconds."`
	FPS            int    `json:"fps,omitempty"`
	NegativePrompt string `json:"negative_prompt,omitempty" jsonschema:"What to leave out."`
	StartImage     string `json:"start_image,omitempty"     jsonschema:"Image file to start from."`
}

// VideoResult is the result of generate_video.
type VideoResult struct {
	Model string     `json:"model"`
	Video OutputFile `json:"video"`
}

// GenerateSoundRequest is the input for generate_sound.
type GenerateSoundRequest struct {
	Text         string   `json:"text"                       jsonschema:"Describes the sound or the music."`
	Model        string   `json:"model,omitempty"            jsonschema:"Model; default first installed."`
	Duration     *float32 `json:"duration_seconds,omitempty" jsonschema:"Length in seconds."`
	Lyrics       string   `json:"lyrics,omitempty"           jsonschema:"Lyrics, for song models."`
	Instrumental *bool    `json:"instrumental,omitempty"     jsonschema:"Music with no voice."`
}

// VoiceVerifyRequest is the input for voice with action verify.
type VoiceVerifyRequest struct {
	Audio1    string  `json:"audio1"              jsonschema:"First recording."`
	Audio2    string  `json:"audio2"              jsonschema:"Second recording."`
	Model     string  `json:"model,omitempty"     jsonschema:"Model; default first installed."`
	Threshold float32 `json:"threshold,omitempty" jsonschema:"Match distance."`
}

// VerifyResult is the result of voice and face with action verify.
type VerifyResult struct {
	Model      string  `json:"model"`
	Verified   bool    `json:"verified"`
	Distance   float32 `json:"distance"`
	Threshold  float32 `json:"threshold"`
	Confidence float32 `json:"confidence"`
}

// IdentifyVoiceRequest is the input for voice with action identify.
type IdentifyVoiceRequest struct {
	Audio     string  `json:"audio"               jsonschema:"Audio file."`
	Model     string  `json:"model,omitempty"     jsonschema:"Model; default first installed."`
	TopK      int     `json:"top_k,omitempty"     jsonschema:"How many matches."`
	Threshold float32 `json:"threshold,omitempty" jsonschema:"Match distance."`
}

// Match is one registered person that voice or face with action identify found.
type Match struct {
	ID         string            `json:"id"`
	Name       string            `json:"name"`
	Labels     map[string]string `json:"labels,omitempty"`
	Distance   float32           `json:"distance"`
	Confidence float32           `json:"confidence"`
	Match      bool              `json:"match"`
}

// IdentifyResult is the result of voice and face with action identify.
type IdentifyResult struct {
	Model   string  `json:"model"`
	Matches []Match `json:"matches"`
}

// VerifyFaceRequest is the input for face with action verify.
type VerifyFaceRequest struct {
	Image1    string  `json:"image1"              jsonschema:"First image."`
	Image2    string  `json:"image2"              jsonschema:"Second image."`
	Model     string  `json:"model,omitempty"     jsonschema:"Model; default first installed."`
	Threshold float32 `json:"threshold,omitempty" jsonschema:"Match distance."`
}

// IdentifyFaceRequest is the input for face with action identify.
type IdentifyFaceRequest struct {
	Image     string  `json:"image"               jsonschema:"Image file."`
	Model     string  `json:"model,omitempty"     jsonschema:"Model; default first installed."`
	TopK      int     `json:"top_k,omitempty"     jsonschema:"How many matches."`
	Threshold float32 `json:"threshold,omitempty" jsonschema:"Match distance."`
}

// GenerateImageToolInput is the input of the generate_image tool. It covers
// both image routes: with Image set the tool repaints the masked area of that
// image, without it the tool makes new images. The client methods stay apart
// (GenerateImage and EditImage); only the tool merges them.
type GenerateImageToolInput struct {
	Prompt         string `json:"prompt"                    jsonschema:"What to show, or paint in the mask."`
	Model          string `json:"model,omitempty"           jsonschema:"Model; default first installed."`
	Size           string `json:"size,omitempty"            jsonschema:"WIDTHxHEIGHT, for example 512x512."`
	NegativePrompt string `json:"negative_prompt,omitempty" jsonschema:"What to leave out."`
	Steps          int    `json:"steps,omitempty"`
	N              int    `json:"n,omitempty"               jsonschema:"1 to 4. Default 1."`
	Image          string `json:"image,omitempty"           jsonschema:"Image to repaint; needs mask."`
	Mask           string `json:"mask,omitempty"            jsonschema:"Mask; white is repainted."`
}

// Actions of the voice, face and voice_profile tools.
const (
	ActionVerify   = "verify"
	ActionIdentify = "identify"
	ActionCreate   = "create"
	ActionList     = "list"
	ActionDelete   = "delete"
)

// VoiceToolInput is the input of the voice tool. One flat object: the action
// says which fields count, and the handler checks them.
type VoiceToolInput struct {
	Action    string  `json:"action"              jsonschema:"verify needs audio and audio2; identify needs audio."`
	Audio     string  `json:"audio"               jsonschema:"Audio file."`
	Audio2    string  `json:"audio2,omitempty"    jsonschema:"Second recording; verify."`
	Model     string  `json:"model,omitempty"     jsonschema:"Model; default first installed."`
	TopK      int     `json:"top_k,omitempty"     jsonschema:"Matches to return; identify."`
	Threshold float32 `json:"threshold,omitempty" jsonschema:"Match distance."`
}

// FaceToolInput is the input of the face tool, shaped like VoiceToolInput.
type FaceToolInput struct {
	Action    string  `json:"action"              jsonschema:"verify needs image and image2; identify needs image."`
	Image     string  `json:"image"               jsonschema:"Image file."`
	Image2    string  `json:"image2,omitempty"    jsonschema:"Second image; verify."`
	Model     string  `json:"model,omitempty"     jsonschema:"Model; default first installed."`
	TopK      int     `json:"top_k,omitempty"     jsonschema:"Matches to return; identify."`
	Threshold float32 `json:"threshold,omitempty" jsonschema:"Match distance."`
}

// VoiceProfileToolInput is the input of the voice_profile tool. One flat
// object: the action says which fields count, and the handler checks them.
type VoiceProfileToolInput struct {
	Action     string `json:"action"               jsonschema:"create needs name, audio, transcript; delete needs id."`
	Name       string `json:"name,omitempty"`
	Audio      string `json:"audio,omitempty"      jsonschema:"WAV file, 1 to 120 s."`
	Transcript string `json:"transcript,omitempty" jsonschema:"Exact words in the clip."`
	Language   string `json:"language,omitempty"`
	ID         string `json:"id,omitempty"`
}

// VoiceProfileSummary is what list shows of a profile.
type VoiceProfileSummary struct {
	ID         string `json:"id"`
	Name       string `json:"name"`
	Language   string `json:"language,omitempty"`
	Voice      string `json:"voice"`
	Shared     bool   `json:"shared,omitempty"`
	DurationMS int64  `json:"duration_ms"`
}

// VoiceProfileCreated is the result of create: the id, and the exact voice
// value to give to speak.
type VoiceProfileCreated struct {
	ID    string `json:"id"`
	Name  string `json:"name"`
	Voice string `json:"voice"`
	Note  string `json:"note,omitempty"`
}

// VoiceProfileVisibilityNote tells the model who sees a profile. The model
// must not promise privacy for a profile an administrator creates.
const VoiceProfileVisibilityNote = "Profiles created by an administrator are visible to all users."

// VoiceProfileList is the result of list: the profiles and the visibility note.
type VoiceProfileList struct {
	Profiles []VoiceProfileSummary `json:"profiles"`
	Note     string                `json:"note,omitempty"`
}
