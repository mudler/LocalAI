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
	Model          string `json:"model,omitempty"           jsonschema:"From list_models."`
	Size           string `json:"size,omitempty"            jsonschema:"WIDTHxHEIGHT."`
	NegativePrompt string `json:"negative_prompt,omitempty" jsonschema:"What to leave out."`
	Steps          int    `json:"steps,omitempty"`
	N              int    `json:"n,omitempty"               jsonschema:"1 to 4. Default 1."`
	Seed           *int   `json:"seed,omitempty"            jsonschema:"Same seed, same image."`
	// Image is the starting image of an img2img call (the "file" field of the
	// API). Mask repainting goes through EditImage.
	Image string `json:"image,omitempty" jsonschema:"Start from this image file (img2img)."`
	// RefImages are reference images for models that take them (the
	// "ref_images" field of the API).
	RefImages []string `json:"ref_images,omitempty" jsonschema:"Image files the model uses as references."`
}

// ImageResult is the result of generate_image.
type ImageResult struct {
	Model     string       `json:"model"`
	ModelNote string       `json:"model_note,omitempty" jsonschema:"Say it to the user: the default that was used."`
	Images    []OutputFile `json:"images"`
}

// EditImageRequest is the input for generate_image when it gets an image.
type EditImageRequest struct {
	Prompt string `json:"prompt"          jsonschema:"What to paint."`
	Image  string `json:"image"           jsonschema:"Image file."`
	Mask   string `json:"mask"            jsonschema:"Mask file. White is repainted."`
	Model  string `json:"model,omitempty" jsonschema:"From list_models."`
	Steps  int    `json:"steps,omitempty"`
}

// SpeakRequest is the input for speak.
type SpeakRequest struct {
	Text         string   `json:"text"`
	Model        string   `json:"model,omitempty"        jsonschema:"From list_models."`
	Voice        string   `json:"voice,omitempty"        jsonschema:"Speaker id, or the voice of a voice_profile."`
	Language     string   `json:"language,omitempty"`
	Format       string   `json:"format,omitempty"       jsonschema:"wav, mp3, opus or flac."`
	Speed        *float32 `json:"speed,omitempty"        jsonschema:"0.25 to 4.0."`
	Instructions string   `json:"instructions,omitempty" jsonschema:"Voice style."`
	SampleRate   int      `json:"sample_rate,omitempty"  jsonschema:"Output Hz."`
}

// AudioResult is the result of speak and generate_sound.
type AudioResult struct {
	Model     string     `json:"model"`
	ModelNote string     `json:"model_note,omitempty" jsonschema:"Say it to the user: the default that was used."`
	Audio     OutputFile `json:"audio"`
}

// TranscribeAudioRequest is the input for transcribe_audio.
type TranscribeAudioRequest struct {
	Audio    string `json:"audio"              jsonschema:"Audio file."`
	Model    string `json:"model,omitempty"    jsonschema:"From list_models."`
	Language string `json:"language,omitempty"`
	Prompt   string `json:"prompt,omitempty"   jsonschema:"Expected words."`
}

// TranscriptResult is the result of transcribe_audio.
type TranscriptResult struct {
	Model     string  `json:"model"`
	ModelNote string  `json:"model_note,omitempty" jsonschema:"Say it to the user: the default that was used."`
	Text      string  `json:"text"`
	Language  string  `json:"language,omitempty"`
	Duration  float64 `json:"duration_seconds,omitempty"`
}

// DescribeImageRequest is the input for describe_image.
type DescribeImageRequest struct {
	Image    string `json:"image"              jsonschema:"Image file."`
	Question string `json:"question,omitempty" jsonschema:"Question."`
	Model    string `json:"model,omitempty"    jsonschema:"Vision model."`
}

// DescribeImageResult is the result of describe_image.
type DescribeImageResult struct {
	Model       string `json:"model"`
	ModelNote   string `json:"model_note,omitempty" jsonschema:"Say it to the user: the default that was used."`
	Description string `json:"description"`
}

// EmbedRequest is the input for embed.
type EmbedRequest struct {
	Input []string `json:"input"           jsonschema:"The texts to embed."`
	Model string   `json:"model,omitempty" jsonschema:"From list_models."`
}

// EmbedResult is the result of embed. The vectors go to a JSON file, because
// they are too long for a tool result.
type EmbedResult struct {
	Model      string      `json:"model"`
	ModelNote  string      `json:"model_note,omitempty" jsonschema:"Say it to the user: the default that was used."`
	Count      int         `json:"count"`
	Dimensions int         `json:"dimensions"`
	Vectors    OutputFile  `json:"vectors"           jsonschema:"JSON file, one vector per text."`
	Preview    [][]float32 `json:"preview,omitempty" jsonschema:"First values of each vector."`
}

// DetectRequest is the input for detect.
type DetectRequest struct {
	Image     string  `json:"image"               jsonschema:"Image file."`
	Model     string  `json:"model,omitempty"     jsonschema:"From list_models."`
	Prompt    string  `json:"prompt,omitempty"    jsonschema:"What to find."`
	Threshold float32 `json:"threshold,omitempty" jsonschema:"Minimum, 0 to 1."`
	// Points and Boxes are prompts for segmentation models, as the API takes
	// them: flat lists of numbers.
	Points []float32 `json:"points,omitempty" jsonschema:"Flat x,y,label triples."`
	Boxes  []float32 `json:"boxes,omitempty"  jsonschema:"Flat x1,y1,x2,y2 quads."`
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
	TopN      int      `json:"top_n,omitempty"  jsonschema:"Best N only."`
	Model     string   `json:"model,omitempty"  jsonschema:"From list_models."`
}

// RerankItem is one ranked document.
type RerankItem struct {
	Index int     `json:"index" jsonschema:"Position in the input."`
	Score float64 `json:"score"`
	Text  string  `json:"text,omitempty"`
}

// RerankResult is the result of rerank, best first.
type RerankResult struct {
	Model     string       `json:"model"`
	ModelNote string       `json:"model_note,omitempty" jsonschema:"Say it to the user: the default that was used."`
	Results   []RerankItem `json:"results"`
}

// VideoRefInput is one reference video: ordered frame images, because the
// API takes a reference video as a list of frames (ref_videos[].frames).
type VideoRefInput struct {
	Frames []string `json:"frames"`
	FPS    int      `json:"fps,omitempty"`
	Audio  string   `json:"audio,omitempty"`
}

// GenerateVideoRequest is the input for generate_video. The field names are
// the tool's; the request carries the API's (size becomes width and height,
// seconds times fps becomes num_frames, steps becomes step).
type GenerateVideoRequest struct {
	Prompt         string          `json:"prompt"`
	Model          string          `json:"model,omitempty"           jsonschema:"From list_models."`
	Size           string          `json:"size,omitempty"            jsonschema:"WIDTHxHEIGHT."`
	Seconds        string          `json:"seconds,omitempty"         jsonschema:"Length. fps defaults to 24."`
	FPS            int             `json:"fps,omitempty"`
	NegativePrompt string          `json:"negative_prompt,omitempty"`
	StartImage     string          `json:"start_image,omitempty"     jsonschema:"Image file."`
	EndImage       string          `json:"end_image,omitempty"       jsonschema:"Image file."`
	Audio          string          `json:"audio,omitempty"           jsonschema:"Audio file."`
	RefImages      []string        `json:"ref_images,omitempty"      jsonschema:"Image files."`
	RefVideos      []VideoRefInput `json:"ref_videos,omitempty"      jsonschema:"Image files per video."`
	RefAudios      []string        `json:"ref_audios,omitempty"      jsonschema:"Audio files."`
	Seed           *int            `json:"seed,omitempty"`
	CFGScale       *float32        `json:"cfg_scale,omitempty"`
	Steps          int             `json:"steps,omitempty"`
}

// VideoResult is the result of generate_video.
type VideoResult struct {
	Model     string     `json:"model"`
	ModelNote string     `json:"model_note,omitempty" jsonschema:"Say it to the user: the default that was used."`
	Video     OutputFile `json:"video"`
}

// UpscaleImageRequest is the input for upscale_image.
type UpscaleImageRequest struct {
	Image string `json:"image"           jsonschema:"Image file."`
	Scale int    `json:"scale"           jsonschema:"Model's factor, e.g. 4."`
	Model string `json:"model,omitempty" jsonschema:"From list_models."`
}

// TransformAudioRequest is the input for transform_audio. The fields are the
// ones of POST /audio/transformations.
type TransformAudioRequest struct {
	Audio      string            `json:"audio"                     jsonschema:"Audio file."`
	Reference  string            `json:"reference,omitempty"`
	Model      string            `json:"model,omitempty"           jsonschema:"From list_models."`
	Format     string            `json:"response_format,omitempty" jsonschema:"wav, mp3, ogg, flac."`
	SampleRate int               `json:"sample_rate,omitempty"     jsonschema:"Hz."`
	Params     map[string]string `json:"params,omitempty"          jsonschema:"noise_gate, noise_gate_threshold_dbfs, stem, language, text, target_text."`
}

// AudioStem is one named output of a separation.
type AudioStem struct {
	Name  string     `json:"name"`
	Audio OutputFile `json:"audio"`
}

// TransformAudioResult is the result of transform_audio.
type TransformAudioResult struct {
	Model string     `json:"model"`
	Audio OutputFile `json:"audio"`
	// Stems names every output of a separation; the one the body carried
	// shares its file with Audio.
	Stems []AudioStem `json:"stems,omitempty"`
	Note  string      `json:"note,omitempty"`
}

// GenerateSoundRequest is the input for generate_sound. The fields are those
// of the sound-generation route, except the source audio, which the API
// leaves out on purpose.
type GenerateSoundRequest struct {
	Text            string   `json:"text"                       jsonschema:"Sound or music wanted."`
	Model           string   `json:"model,omitempty"            jsonschema:"From list_models."`
	Duration        *float32 `json:"duration_seconds,omitempty" jsonschema:"Length in seconds."`
	Lyrics          string   `json:"lyrics,omitempty"           jsonschema:"Lyrics, for song models."`
	Instrumental    *bool    `json:"instrumental,omitempty"     jsonschema:"Music with no voice."`
	Caption         string   `json:"caption,omitempty"          jsonschema:"Style caption."`
	BPM             *int     `json:"bpm,omitempty"`
	Keyscale        string   `json:"keyscale,omitempty"         jsonschema:"For example C major."`
	Timesignature   string   `json:"timesignature,omitempty"    jsonschema:"For example 4/4."`
	Language        string   `json:"language,omitempty"`
	VocalLanguage   string   `json:"vocal_language,omitempty"`
	Think           *bool    `json:"think,omitempty"`
	DoSample        *bool    `json:"do_sample,omitempty"`
	PromptInfluence *float32 `json:"prompt_influence,omitempty" jsonschema:"Sampling temperature."`
	ExpansionModel  string   `json:"expansion_model,omitempty"  jsonschema:"From list_models."`
}

// VoiceVerifyRequest is the input for voice with action verify.
type VoiceVerifyRequest struct {
	Audio1    string  `json:"audio1"              jsonschema:"First recording."`
	Audio2    string  `json:"audio2"              jsonschema:"Second recording."`
	Model     string  `json:"model,omitempty"     jsonschema:"From list_models."`
	Threshold float32 `json:"threshold,omitempty" jsonschema:"Distance."`
	// AntiSpoofing asks the model to check that the recording is live.
	AntiSpoofing bool `json:"anti_spoofing,omitempty"`
}

// VerifyResult is the result of voice and face with action verify. The
// liveness fields are there only when the call set anti_spoofing and the
// route answered them (the face route does).
type VerifyResult struct {
	Model              string   `json:"model"`
	Verified           bool     `json:"verified"`
	Distance           float32  `json:"distance"`
	Threshold          float32  `json:"threshold"`
	Confidence         float32  `json:"confidence"`
	Img1IsReal         *bool    `json:"img1_is_real,omitempty"`
	Img1AntispoofScore *float32 `json:"img1_antispoof_score,omitempty"`
	Img2IsReal         *bool    `json:"img2_is_real,omitempty"`
	Img2AntispoofScore *float32 `json:"img2_antispoof_score,omitempty"`
}

// IdentifyVoiceRequest is the input for voice with action identify.
type IdentifyVoiceRequest struct {
	Audio     string  `json:"audio"               jsonschema:"Audio file."`
	Model     string  `json:"model,omitempty"     jsonschema:"From list_models."`
	TopK      int     `json:"top_k,omitempty"     jsonschema:"How many matches."`
	Threshold float32 `json:"threshold,omitempty" jsonschema:"Distance."`
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
	Image1       string  `json:"image1"              jsonschema:"First image."`
	Image2       string  `json:"image2"              jsonschema:"Second image."`
	Model        string  `json:"model,omitempty"     jsonschema:"From list_models."`
	Threshold    float32 `json:"threshold,omitempty" jsonschema:"Distance."`
	AntiSpoofing bool    `json:"anti_spoofing,omitempty"`
}

// IdentifyFaceRequest is the input for face with action identify.
type IdentifyFaceRequest struct {
	Image     string  `json:"image"               jsonschema:"Image file."`
	Model     string  `json:"model,omitempty"     jsonschema:"From list_models."`
	TopK      int     `json:"top_k,omitempty"     jsonschema:"How many matches."`
	Threshold float32 `json:"threshold,omitempty" jsonschema:"Distance."`
}

// GenerateImageToolInput is the input of the generate_image tool. It covers
// both image routes: with Image and Mask the tool repaints the masked area,
// otherwise it makes new images (from Image as a start, when given). The client methods stay apart
// (GenerateImage and EditImage); only the tool merges them.
type GenerateImageToolInput struct {
	Prompt         string   `json:"prompt"                    jsonschema:"What to show or paint."`
	Model          string   `json:"model,omitempty"           jsonschema:"From list_models."`
	Size           string   `json:"size,omitempty"            jsonschema:"WIDTHxHEIGHT."`
	NegativePrompt string   `json:"negative_prompt,omitempty" jsonschema:"What to leave out."`
	Steps          int      `json:"steps,omitempty"`
	N              int      `json:"n,omitempty"               jsonschema:"1 to 4. Default 1."`
	Seed           *int     `json:"seed,omitempty"            jsonschema:"Same seed, same image."`
	Image          string   `json:"image,omitempty"           jsonschema:"Start image."`
	Mask           string   `json:"mask,omitempty"            jsonschema:"White is repainted."`
	RefImages      []string `json:"ref_images,omitempty"    jsonschema:"Reference image files. Not with mask."`
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
	Action       string  `json:"action"              jsonschema:"verify: audio, audio2; identify: audio."`
	Audio        string  `json:"audio"               jsonschema:"Audio file."`
	Audio2       string  `json:"audio2,omitempty"    jsonschema:"Second recording; verify."`
	Model        string  `json:"model,omitempty"     jsonschema:"From list_models."`
	TopK         int     `json:"top_k,omitempty"     jsonschema:"Count; identify."`
	Threshold    float32 `json:"threshold,omitempty" jsonschema:"Distance."`
	AntiSpoofing bool    `json:"anti_spoofing,omitempty" jsonschema:"Liveness; verify."`
}

// FaceToolInput is the input of the face tool, shaped like VoiceToolInput.
type FaceToolInput struct {
	Action       string  `json:"action"              jsonschema:"verify: image, image2; identify: image."`
	Image        string  `json:"image"               jsonschema:"Image file."`
	Image2       string  `json:"image2,omitempty"    jsonschema:"Second image; verify."`
	Model        string  `json:"model,omitempty"     jsonschema:"From list_models."`
	TopK         int     `json:"top_k,omitempty"     jsonschema:"Count; identify."`
	Threshold    float32 `json:"threshold,omitempty" jsonschema:"Distance."`
	AntiSpoofing bool    `json:"anti_spoofing,omitempty" jsonschema:"Liveness; verify."`
}

// VoiceProfileToolInput is the input of the voice_profile tool. One flat
// object: the action says which fields count, and the handler checks them.
type VoiceProfileToolInput struct {
	Action     string `json:"action"               jsonschema:"create: name, audio, transcript; delete: id."`
	Name       string `json:"name,omitempty"`
	Audio      string `json:"audio,omitempty"      jsonschema:"WAV file, 1 to 120 s."`
	Transcript string `json:"transcript,omitempty" jsonschema:"Exact words in the clip."`
	Language   string `json:"language,omitempty"`
	// Description and ExtraClips are for create.
	Description string      `json:"description,omitempty"`
	ExtraClips  []VoiceClip `json:"extra_clips,omitempty" jsonschema:"More WAV clips, each with its transcript. Up to 9."`
	ID          string      `json:"id,omitempty"`
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

// ListModelsRequest is the input for list_models.
type ListModelsRequest struct {
	Capability string `json:"capability,omitempty" jsonschema:"image, tts, transcript, sound_generation, embeddings, detection, video, rerank, chat. Empty: all."`
	Query      string `json:"query,omitempty"      jsonschema:"Part of a name or description."`
	Offset     int    `json:"offset,omitempty"     jsonschema:"Models to skip."`
	Limit      int    `json:"limit,omitempty"      jsonschema:"Page size, max 60."`
}

// ModelInfo is one model the acting user may use.
type ModelInfo struct {
	Name         string   `json:"name"`
	Capabilities []string `json:"capabilities,omitempty"`
	Description  string   `json:"description,omitempty"`
}

// ModelList is the result of list_models, by name.
type ModelList struct {
	Models []ModelInfo `json:"models"`
	// Total counts the models that match the filters, before paging.
	Total int `json:"total"`
	// Offset is the number of matching models skipped before this page.
	Offset int `json:"offset"`
	// Returned is the number of models in this page.
	Returned int `json:"returned"`
	// More counts the matching models after this page.
	More int `json:"more,omitempty"`
	// Hint names the next call when more models remain, and says what to do
	// when the list is empty.
	Hint string `json:"hint,omitempty"`
}
