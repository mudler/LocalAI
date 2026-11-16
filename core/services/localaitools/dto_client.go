package localaitools

import "fmt"

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

// Actions of the projects tool, besides list, create and delete above.
const (
	ActionGet            = "get"
	ActionUpdate         = "update"
	ActionArchive        = "archive"
	ActionRestore        = "restore"
	ActionSetChatProject = "set_chat_project"
	ActionPull           = "pull"
	ActionUnpull         = "unpull"
	ActionAgents         = "agents"
	ActionConnections    = "connections"
	ActionTriggers       = "triggers"
	ActionLoops          = "loops"
	ActionShared         = "shared"
	// memory reads the memory of a project, write_memory writes one file of
	// it, chats lists its chats. shared_get reads a project that someone
	// shared with its memory (project is its ref).
	ActionMemory      = "memory"
	ActionWriteMemory = "write_memory"
	ActionChats       = "chats"
	ActionSharedGet   = "shared_get"
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

// ProjectsToolInput is the input of the projects tool. One flat object: the
// action says which fields count, and the handler checks them.
type ProjectsToolInput struct {
	Action  string `json:"action"                jsonschema:"Needs: get, agents, connections, triggers, loops, chats, memory, archive, restore, delete, update: project; create: name; set_chat_project, pull, unpull: chat_id, project; write_memory: project, file, content; shared_get: project (a ref from shared)."`
	Project string `json:"project,omitempty"     jsonschema:"Project id; a ref for shared_get."`
	Name    string `json:"name,omitempty"`
	// Description is a pointer so that update can clear it: "" is sent, absent is left alone.
	Description *string `json:"description,omitempty"`
	ChatID      string  `json:"chat_id,omitempty"`
	File        string  `json:"file,omitempty"        jsonschema:"Such as food.md; write_memory."`
	Content     *string `json:"content,omitempty"     jsonschema:"Whole new text, up to 16 KiB; write_memory."`
	// Attached is the attached block of the project: the tools its chats and agents get on. It replaces the block, except in a chat, where the entries are added and none is removed.
	Attached map[string][]string `json:"attached,omitempty"    jsonschema:"create, update: lists skills, addons, connectors, mcp_servers, collections; replaces the block."`
}

// ProjectAttached is what a project attaches, in the shape of the defaults of
// the project: five lists of names. Chats and agents of the project get these
// tools on and granted.
type ProjectAttached struct {
	Skills      []string `json:"skills,omitempty"`
	Addons      []string `json:"addons,omitempty"`
	Connectors  []string `json:"connectors,omitempty"`
	MCPServers  []string `json:"mcp_servers,omitempty"`
	Collections []string `json:"collections,omitempty"`
}

// ProjectAttachedFromMap reads the attached argument of the tool. A key that
// is not one of the five lists is an error, so a typo is not dropped.
func ProjectAttachedFromMap(m map[string][]string) (*ProjectAttached, error) {
	if m == nil {
		return nil, nil
	}
	var a ProjectAttached
	for k, v := range m {
		switch k {
		case "skills":
			a.Skills = v
		case "addons":
			a.Addons = v
		case "connectors":
			a.Connectors = v
		case "mcp_servers":
			a.MCPServers = v
		case "collections":
			a.Collections = v
		default:
			return nil, fmt.Errorf("projects: attached has no list %q (use skills, addons, connectors, mcp_servers, collections)", k)
		}
	}
	return &a, nil
}

// HoldsCredentials says the block attaches a kind of tool that can hold
// credentials: a connector, an add-on or an MCP server.
func (a *ProjectAttached) HoldsCredentials() bool {
	return a != nil && (len(a.Connectors) > 0 || len(a.Addons) > 0 || len(a.MCPServers) > 0)
}

// ProjectsRequest is what the client sends for one action of the projects
// tool. The shared code maps it to the project routes of the acting user.
type ProjectsRequest struct {
	Action      string
	Project     string
	Name        string
	Description *string
	ChatID      string
	File        string
	Content     *string
	Attached    *ProjectAttached
}

// ProjectInfo is one project as the tool shows it. The defaults of a project
// (the limit) stay out: they name skills, add-ons and servers the model does
// not need. What the project attaches is shown, so the model can answer what
// the project gives its chats.
type ProjectInfo struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Slug        string `json:"slug"`
	Description string `json:"description,omitempty"`
	Status      string `json:"status"`
	ChatCount   int    `json:"chat_count,omitempty"`
	AgentCount  int    `json:"agent_count,omitempty"`
	// Attached is what the project attaches.
	Attached ProjectAttached `json:"attached"`
}

// SharedProjectInfo is one project of another person that is shared with the
// caller, as the shared action shows it. ref is what pull takes as project.
// Never the owner's account, a path or the manifest id.
type SharedProjectInfo struct {
	Ref       string `json:"ref"`
	Name      string `json:"name"`
	Mode      string `json:"mode"`
	Status    string `json:"status"`
	OwnerName string `json:"owner_name"`
}

// ProjectAgentInfo is one agent of a project as the agents action shows it:
// a brief row, never the instructions or the lists of the agent.
type ProjectAgentInfo struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	Model       string `json:"model,omitempty"`
	Unavailable bool   `json:"unavailable,omitempty"`
	FilesMode   string `json:"files_mode,omitempty"`
	MemoryWrite bool   `json:"memory_write,omitempty"`
}

// ProjectConnectionInfo is a messaging connection of a project agent, as the
// connections action shows it: never a token or a sender list.
type ProjectConnectionInfo struct {
	ID        string `json:"id"`
	Kind      string `json:"kind"`
	Name      string `json:"name"`
	AgentID   string `json:"agent_id"`
	AgentName string `json:"agent_name,omitempty"`
	Enabled   bool   `json:"enabled,omitempty"`
}

// ProjectTriggerInfo is a trigger of a project agent. Next is the next run of
// a schedule trigger, when it has one.
type ProjectTriggerInfo struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	Kind      string `json:"kind"`
	AgentName string `json:"agent_name,omitempty"`
	Schedule  string `json:"schedule,omitempty"`
	Folder    string `json:"folder,omitempty"`
	Enabled   bool   `json:"enabled,omitempty"`
	Next      string `json:"next,omitempty"`
}

// ProjectLoopInfo is a loop in a chat of a project agent; Prompt is cut short.
type ProjectLoopInfo struct {
	ID        string `json:"id"`
	ChatID    string `json:"chat_id"`
	ChatTitle string `json:"chat_title,omitempty"`
	Prompt    string `json:"prompt,omitempty"`
	State     string `json:"state"`
}

// ProjectMemoryInfo is the memory of a project: the text of index.md and the
// names of the topic files next to it. Size and time stay out.
type ProjectMemoryInfo struct {
	Index string   `json:"index"`
	Files []string `json:"files"`
}

// ProjectChatInfo is one chat of a project as the chats action shows it: a
// brief row, never a message.
type ProjectChatInfo struct {
	ID      string `json:"id"`
	Title   string `json:"title,omitempty"`
	AgentID string `json:"agent_id,omitempty"`
	Updated string `json:"updated,omitempty"`
}

// ProjectChat is the project part of a chat after set_chat_project, pull or unpull.
type ProjectChat struct {
	ID             string   `json:"id"`
	ProjectID      string   `json:"project_id,omitempty"`
	PulledProjects []string `json:"pulled_projects,omitempty"`
}

// ProjectsResult is the answer of the projects tool: the member that fits the
// action is set.
type ProjectsResult struct {
	Projects    []ProjectInfo           `json:"projects,omitempty"`
	Project     *ProjectInfo            `json:"project,omitempty"`
	Agents      []ProjectAgentInfo      `json:"agents,omitempty"`
	Connections []ProjectConnectionInfo `json:"connections,omitempty"`
	Triggers    []ProjectTriggerInfo    `json:"triggers,omitempty"`
	Loops       []ProjectLoopInfo       `json:"loops,omitempty"`
	Shared      []SharedProjectInfo     `json:"shared,omitempty"`
	SharedOne   *SharedProjectInfo      `json:"shared_project,omitempty"`
	Memory      *ProjectMemoryInfo      `json:"memory,omitempty"`
	Chats       []ProjectChatInfo       `json:"chats,omitempty"`
	Written     string                  `json:"written,omitempty"`
	Chat        *ProjectChat            `json:"chat,omitempty"`
	Deleted     bool                    `json:"deleted,omitempty"`
}
