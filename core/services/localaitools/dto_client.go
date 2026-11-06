package localaitools

// DTOs of the client tools: the inference operations that any user may run
// within their own permissions. File arguments are references that the
// FileStore resolves (a path in the user's files, or a data URL); a file
// result is a reference too. No tool carries file bytes.

// OutputFile describes a file a tool wrote. Ref is what the next tool takes
// as a file argument.
type OutputFile struct {
	Ref    string `json:"ref"              jsonschema:"Reference of the file. Pass it as a file argument of another tool, or tell the person where it is."`
	MIME   string `json:"mime,omitempty"`
	Size   int    `json:"size_bytes"`
	Width  int    `json:"width,omitempty"  jsonschema:"Image width in pixels, when the file is an image."`
	Height int    `json:"height,omitempty" jsonschema:"Image height in pixels, when the file is an image."`
}

// GenerateImageRequest is the input for generate_image.
type GenerateImageRequest struct {
	Prompt         string `json:"prompt"                    jsonschema:"What the image shows."`
	Model          string `json:"model,omitempty"           jsonschema:"Image model to use. Empty picks the first installed image model."`
	Size           string `json:"size,omitempty"            jsonschema:"Size as WIDTHxHEIGHT, for example 512x512. Empty uses the model default."`
	NegativePrompt string `json:"negative_prompt,omitempty" jsonschema:"What the image must not show."`
	Steps          int    `json:"steps,omitempty"           jsonschema:"Number of diffusion steps. Empty uses the model default."`
	N              int    `json:"n,omitempty"               jsonschema:"How many images to make, 1 to 4. Defaults to 1."`
}

// ImageResult is the result of generate_image and edit_image.
type ImageResult struct {
	Model  string       `json:"model"`
	Images []OutputFile `json:"images"`
}

// EditImageRequest is the input for edit_image.
type EditImageRequest struct {
	Prompt string `json:"prompt"          jsonschema:"What to paint in the masked area."`
	Image  string `json:"image"           jsonschema:"File reference of the image to edit."`
	Mask   string `json:"mask"            jsonschema:"File reference of the mask image. White marks the area to repaint."`
	Model  string `json:"model,omitempty" jsonschema:"Image model to use. Empty picks the first installed image model."`
	Steps  int    `json:"steps,omitempty" jsonschema:"Number of diffusion steps. Empty uses the model default."`
}

// SpeakRequest is the input for speak.
type SpeakRequest struct {
	Text         string   `json:"text"                   jsonschema:"The text to say."`
	Model        string   `json:"model,omitempty"        jsonschema:"Text-to-speech model to use. Empty picks the first installed one."`
	Voice        string   `json:"voice,omitempty"        jsonschema:"A speaker id of the model, or a saved voice profile URI from list_voice_profiles to speak in a cloned voice. The server takes no uploaded reference audio."`
	Language     string   `json:"language,omitempty"     jsonschema:"Language code, if the model needs it."`
	Format       string   `json:"format,omitempty"       jsonschema:"Audio format: wav, mp3, opus, flac. Empty uses the model default."`
	Speed        *float32 `json:"speed,omitempty"        jsonschema:"Speaking speed from 0.25 to 4.0."`
	Instructions string   `json:"instructions,omitempty" jsonschema:"Style of the voice, for models that take one."`
}

// AudioResult is the result of speak and generate_sound.
type AudioResult struct {
	Model string     `json:"model"`
	Audio OutputFile `json:"audio"`
}

// TranscribeAudioRequest is the input for transcribe_audio.
type TranscribeAudioRequest struct {
	Audio    string `json:"audio"              jsonschema:"File reference of the recording."`
	Model    string `json:"model,omitempty"    jsonschema:"Transcription model to use. Empty picks the first installed one."`
	Language string `json:"language,omitempty" jsonschema:"Spoken language code. Empty lets the model decide."`
	Prompt   string `json:"prompt,omitempty"   jsonschema:"Words or names that the recording is likely to contain."`
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
	Image    string `json:"image"              jsonschema:"File reference of the image."`
	Question string `json:"question,omitempty" jsonschema:"What to ask about the image. Empty asks for a full description with any text in it."`
	Model    string `json:"model,omitempty"    jsonschema:"Model that can look at images. Empty picks one."`
}

// DescribeImageResult is the result of describe_image.
type DescribeImageResult struct {
	Model       string `json:"model"`
	Description string `json:"description"`
}

// EmbedRequest is the input for embed.
type EmbedRequest struct {
	Input []string `json:"input"           jsonschema:"The texts to embed."`
	Model string   `json:"model,omitempty" jsonschema:"Embedding model to use. Empty picks the first installed one."`
}

// EmbedResult is the result of embed. The vectors go to a JSON file, because
// they are too long for a tool result.
type EmbedResult struct {
	Model      string      `json:"model"`
	Count      int         `json:"count"`
	Dimensions int         `json:"dimensions"`
	Vectors    OutputFile  `json:"vectors"           jsonschema:"JSON file with one vector per input text."`
	Preview    [][]float32 `json:"preview,omitempty" jsonschema:"The first values of each vector."`
}

// DetectRequest is the input for detect.
type DetectRequest struct {
	Image     string  `json:"image"               jsonschema:"File reference of the image."`
	Model     string  `json:"model,omitempty"     jsonschema:"Detection model to use. Empty picks the first installed one."`
	Prompt    string  `json:"prompt,omitempty"    jsonschema:"What to find, for models that take a text prompt."`
	Threshold float32 `json:"threshold,omitempty" jsonschema:"Minimum confidence from 0 to 1."`
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
	Query     string   `json:"query"            jsonschema:"The query to rank the documents against."`
	Documents []string `json:"documents"        jsonschema:"The documents to rank."`
	TopN      int      `json:"top_n,omitempty"  jsonschema:"Return only the best N. Empty returns all."`
	Model     string   `json:"model,omitempty"  jsonschema:"Rerank model to use. Empty picks the first installed one."`
}

// RerankItem is one ranked document.
type RerankItem struct {
	Index int     `json:"index" jsonschema:"Position of the document in the input."`
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
	Prompt         string `json:"prompt"                    jsonschema:"What the video shows."`
	Model          string `json:"model,omitempty"           jsonschema:"Video model to use. Empty picks the first installed one."`
	Size           string `json:"size,omitempty"            jsonschema:"Size as WIDTHxHEIGHT, for example 512x512."`
	Seconds        string `json:"seconds,omitempty"         jsonschema:"Length in seconds."`
	FPS            int    `json:"fps,omitempty"             jsonschema:"Frames per second."`
	NegativePrompt string `json:"negative_prompt,omitempty" jsonschema:"What the video must not show."`
	StartImage     string `json:"start_image,omitempty"     jsonschema:"File reference of an image to start from."`
}

// VideoResult is the result of generate_video.
type VideoResult struct {
	Model string     `json:"model"`
	Video OutputFile `json:"video"`
}

// GenerateSoundRequest is the input for generate_sound.
type GenerateSoundRequest struct {
	Text         string   `json:"text"                       jsonschema:"Describes the sound or the music."`
	Model        string   `json:"model,omitempty"            jsonschema:"Sound generation model to use. Empty picks the first installed one."`
	Duration     *float32 `json:"duration_seconds,omitempty" jsonschema:"Length in seconds."`
	Lyrics       string   `json:"lyrics,omitempty"           jsonschema:"Lyrics to sing, for models that make songs."`
	Instrumental *bool    `json:"instrumental,omitempty"     jsonschema:"True for music with no voice."`
}

// VoiceVerifyRequest is the input for verify_voice.
type VoiceVerifyRequest struct {
	Audio1    string  `json:"audio1"              jsonschema:"File reference of the first recording."`
	Audio2    string  `json:"audio2"              jsonschema:"File reference of the second recording."`
	Model     string  `json:"model,omitempty"     jsonschema:"Speaker recognition model. Empty picks the first installed one."`
	Threshold float32 `json:"threshold,omitempty" jsonschema:"Distance under which the voices count as the same. Empty uses the model default."`
}

// VerifyResult is the result of verify_voice and verify_face.
type VerifyResult struct {
	Model      string  `json:"model"`
	Verified   bool    `json:"verified"`
	Distance   float32 `json:"distance"`
	Threshold  float32 `json:"threshold"`
	Confidence float32 `json:"confidence"`
}

// IdentifyVoiceRequest is the input for identify_voice.
type IdentifyVoiceRequest struct {
	Audio     string  `json:"audio"               jsonschema:"File reference of the recording."`
	Model     string  `json:"model,omitempty"     jsonschema:"Speaker recognition model. Empty picks the first installed one."`
	TopK      int     `json:"top_k,omitempty"     jsonschema:"How many matches to return."`
	Threshold float32 `json:"threshold,omitempty" jsonschema:"Distance under which a speaker counts as a match."`
}

// Match is one registered person that identify_voice or identify_face found.
type Match struct {
	ID         string            `json:"id"`
	Name       string            `json:"name"`
	Labels     map[string]string `json:"labels,omitempty"`
	Distance   float32           `json:"distance"`
	Confidence float32           `json:"confidence"`
	Match      bool              `json:"match"`
}

// IdentifyResult is the result of identify_voice and identify_face.
type IdentifyResult struct {
	Model   string  `json:"model"`
	Matches []Match `json:"matches"`
}

// VerifyFaceRequest is the input for verify_face.
type VerifyFaceRequest struct {
	Image1    string  `json:"image1"              jsonschema:"File reference of the first image."`
	Image2    string  `json:"image2"              jsonschema:"File reference of the second image."`
	Model     string  `json:"model,omitempty"     jsonschema:"Face recognition model. Empty picks the first installed one."`
	Threshold float32 `json:"threshold,omitempty" jsonschema:"Distance under which the faces count as the same person. Empty uses the model default."`
}

// IdentifyFaceRequest is the input for identify_face.
type IdentifyFaceRequest struct {
	Image     string  `json:"image"               jsonschema:"File reference of the image."`
	Model     string  `json:"model,omitempty"     jsonschema:"Face recognition model. Empty picks the first installed one."`
	TopK      int     `json:"top_k,omitempty"     jsonschema:"How many matches to return."`
	Threshold float32 `json:"threshold,omitempty" jsonschema:"Distance under which a face counts as a match."`
}
