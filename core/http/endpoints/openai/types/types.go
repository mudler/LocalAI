package types

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
)

// The voice the model uses to respond. Voice cannot be changed during the session once the model has responded with audio at least once. Current voice options are alloy, ash, ballad, coral, echo, sage, shimmer, verse, marin, and cedar. We recommend marin and cedar for best quality.
type Voice string

const (
	VoiceAlloy   Voice = "alloy"
	VoiceAsh     Voice = "ash"
	VoiceBallad  Voice = "ballad"
	VoiceCoral   Voice = "coral"
	VoiceEcho    Voice = "echo"
	VoiceSage    Voice = "sage"
	VoiceShimmer Voice = "shimmer"
	VoiceVerse   Voice = "verse"
	VoiceMarin   Voice = "marin"
	VoiceCedar   Voice = "cedar"
	VoiceFable   Voice = "fable"
	VoiceOnyx    Voice = "onyx"
	VoiceNova    Voice = "nova"
)

type AudioFormat string

const (
	AudioFormatPcm16    AudioFormat = "pcm16"
	AudioFormatG711Ulaw AudioFormat = "g711_ulaw"
	AudioFormatG711Alaw AudioFormat = "g711_alaw"
)

type Modality string

const (
	ModalityText  Modality = "text"
	ModalityAudio Modality = "audio"
)

type TurnDetectionType string

const (
	TurnDetectionTypeServerVad   TurnDetectionType = "server_vad"
	TurnDetectionTypeSemanticVad TurnDetectionType = "semantic_vad"
)

type ToolChoiceMode string

const (
	ToolChoiceModeNone     ToolChoiceMode = "none"
	ToolChoiceModeAuto     ToolChoiceMode = "auto"
	ToolChoiceModeRequired ToolChoiceMode = "required"
)

func (t ToolChoiceMode) ToolChoiceType() string {
	return string(t)
}

type ToolChoiceType string

const (
	ToolChoiceTypeFunction ToolChoiceType = "function"
	ToolChoiceTypeMCP      ToolChoiceType = "mcp"
)

type ToolChoiceFunction struct {
	// The name of the function to call.
	Name string `json:"name,omitempty"`
}

func (t ToolChoiceFunction) ToolChoiceType() string {
	return string(ToolChoiceTypeFunction)
}

func (t ToolChoiceFunction) MarshalJSON() ([]byte, error) {
	type typeAlias ToolChoiceFunction
	type typeWrapper struct {
		typeAlias
		Type string `json:"type"`
	}
	shadow := typeWrapper{
		typeAlias: typeAlias(t),
		Type:      t.ToolChoiceType(),
	}
	return json.Marshal(shadow)
}

type ToolChoiceMCP struct {
	// The label of the MCP server to use.
	ServerLabel string `json:"server_label,omitempty"`

	// The name of the tool to call on the server.
	Name string `json:"name,omitempty"`
}

func (t ToolChoiceMCP) ToolChoiceType() string {
	return string(ToolChoiceTypeMCP)
}

func (t ToolChoiceMCP) MarshalJSON() ([]byte, error) {
	type typeAlias ToolChoiceMCP
	type typeWrapper struct {
		typeAlias
		Type string `json:"type"`
	}
	shadow := typeWrapper{
		typeAlias: typeAlias(t),
		Type:      t.ToolChoiceType(),
	}
	return json.Marshal(shadow)
}

type ToolChoiceUnion struct {
	// Controls which (if any) tool is called by the model.
	//
	// none means the model will not call any tool and instead generates a message.
	//
	// auto means the model can pick between generating a message or calling one or more tools.
	//
	// required means the model must call one or more tools.
	Mode ToolChoiceMode `json:",omitempty"`

	// Use this option to force the model to call a specific function.
	Function *ToolChoiceFunction `json:",omitempty"`

	// Use this option to force the model to call a specific tool on a remote MCP server.
	MCP *ToolChoiceMCP `json:",omitempty"`
}

func (t ToolChoiceUnion) MarshalJSON() ([]byte, error) {
	if t.Function != nil {
		return json.Marshal(t.Function)
	}
	if t.MCP != nil {
		return json.Marshal(t.MCP)
	}
	return json.Marshal(t.Mode)
}

func (t *ToolChoiceUnion) UnmarshalJSON(data []byte) error {
	if isNull(data) {
		return nil
	}
	var u typeStruct
	if err := json.Unmarshal(data, &u); err != nil {
		t.Mode = ToolChoiceMode(bytes.Trim(data, "\""))
		return nil //nolint: nilerr // data is string instead of object
	}
	switch ToolChoiceType(u.Type) {
	case ToolChoiceTypeFunction:
		return json.Unmarshal(data, &t.Function)
	case ToolChoiceTypeMCP:
		return json.Unmarshal(data, &t.MCP)
	default:
		t.Mode = ToolChoiceMode(u.Type)
	}
	return nil
}

type ToolType string

const (
	ToolTypeFunction ToolType = "function"
	ToolTypeMCP      ToolType = "mcp"
)

type ToolFunction struct {
	// The name of the function.
	Name string `json:"name"`

	// The description of the function, including guidance on when and how to call it, and guidance about what to tell the user when calling (if anything).
	Description string `json:"description"`

	// The type of the tool, i.e. function.
	Parameters any `json:"parameters"`
}

func (t ToolFunction) ToolType() ToolType {
	return ToolTypeFunction
}

func (t ToolFunction) MarshalJSON() ([]byte, error) {
	type typeAlias ToolFunction
	type toolFunction struct {
		typeAlias
		Type ToolType `json:"type"`
	}
	shadow := toolFunction{
		typeAlias: typeAlias(t),
		Type:      t.ToolType(),
	}
	return json.Marshal(shadow)
}

type MCPToolFilter struct {
	// Indicates whether or not a tool modifies data or is read-only. If an MCP server is annotated with readOnlyHint, it will match this filter.
	ReadOnly bool `json:"read_only,omitempty"`

	// List of allowed tool names.
	ToolNames []string `json:"tool_names,omitempty"`
}

type MCPAllowedToolsUnion struct {
	// A string array of allowed tool names
	ToolNames []string `json:",omitempty"`

	// A filter object to specify which tools are allowed.
	Filter *MCPToolFilter `json:",omitempty"`
}

func (t MCPAllowedToolsUnion) MarshalJSON() ([]byte, error) {
	if len(t.ToolNames) > 0 {
		return json.Marshal(t.ToolNames)
	}
	return json.Marshal(t.Filter)
}

func (t *MCPAllowedToolsUnion) UnmarshalJSON(data []byte) error {
	if isNull(data) {
		return nil
	}
	if err := json.Unmarshal(data, &t.Filter); err == nil {
		return nil
	}
	return json.Unmarshal(data, &t.ToolNames)
}

type MCPRequireApprovalFilter struct {
	// A filter object to specify which tools are allowed.
	Always *MCPToolFilter `json:",omitempty"`

	// A filter object to specify which tools are allowed.
	Never *MCPToolFilter `json:",omitempty"`
}

type MCPToolRequireApprovalUnion struct {
	// Specify which of the MCP server's tools require approval. Can be always, never, or a filter object associated with tools that require approval.
	Filter *MCPRequireApprovalFilter `json:",omitempty"`

	// Specify a single approval policy for all tools. One of always or never. When set to always, all tools will require approval. When set to never, all tools will not require approval.
	Setting string `json:",omitempty"`
}

func (t MCPToolRequireApprovalUnion) MarshalJSON() ([]byte, error) {
	if t.Filter != nil {
		return json.Marshal(t.Filter)
	}
	return json.Marshal(t.Setting)
}

func (t *MCPToolRequireApprovalUnion) UnmarshalJSON(data []byte) error {
	if isNull(data) {
		return nil
	}
	if err := json.Unmarshal(data, &t.Filter); err == nil {
		return nil
	}
	return json.Unmarshal(data, &t.Setting)
}

type ToolMCP struct {
	// A label for this MCP server, used to identify it in tool calls.
	ServerLabel string `json:"server_label,omitempty"`

	// An OAuth access token that can be used with a remote MCP server, either with a custom MCP server URL or a service connector. Your application must handle the OAuth authorization flow and provide the token here.
	Authorization string `json:"authorization,omitempty"`

	// Optional description of the MCP server, used to provide more context.
	ServerDescription string `json:"server_description,omitempty"`

	// The URL for the MCP server. One of server_url or connector_id must be provided.
	ServerURL string `json:"server_url,omitempty"`

	// List of allowed tool names or a filter object.
	AllowedTools *MCPAllowedToolsUnion `json:"allowed_tools,omitempty"`

	// Optional HTTP headers to send to the MCP server. Use for authentication or other purposes.
	Headers map[string]string `json:"headers,omitempty"`

	// Specify which of the MCP server's tools require approval.
	RequireApproval *MCPToolRequireApprovalUnion `json:"require_approval,omitempty"`

	// Identifier for service connectors, like those available in ChatGPT. One of server_url or connector_id must be provided. Learn more about service connectors here.
	//
	// Currently supported connector_id values are:
	//
	// Dropbox: connector_dropbox
	// Gmail: connector_gmail
	// Google Calendar: connector_googlecalendar
	// Google Drive: connector_googledrive
	// Microsoft Teams: connector_microsoftteams
	// Outlook Calendar: connector_outlookcalendar
	// Outlook Email: connector_outlookemail
	// SharePoint: connector_sharepoint
	ConnectorID string `json:"connector_id,omitempty"`
}

func (t ToolMCP) ToolType() ToolType {
	return ToolTypeMCP
}

func (t ToolMCP) MarshalJSON() ([]byte, error) {
	type typeAlias ToolMCP
	type toolMCP struct {
		typeAlias
		Type ToolType `json:"type"`
	}
	shadow := toolMCP{
		typeAlias: typeAlias(t),
		Type:      t.ToolType(),
	}
	return json.Marshal(shadow)
}

type TracingConfiguration struct {
	GroupID      string `json:"group_id,omitempty"`
	Metadata     any    `json:"metadata,omitempty"`
	WorkflowName string `json:"workflow_name,omitempty"`
}

type ToolUnion struct {
	Function *ToolFunction `json:",omitempty"`

	// Give the model access to additional tools via remote Model Context Protocol (MCP) servers. Learn more about MCP.
	MCP *ToolMCP `json:",omitempty"`
}

func (t ToolUnion) MarshalJSON() ([]byte, error) {
	if t.Function != nil {
		return json.Marshal(t.Function)
	}
	if t.MCP != nil {
		return json.Marshal(t.MCP)
	}
	return nil, errors.New("no tool")
}

func (t *ToolUnion) UnmarshalJSON(data []byte) error {
	if isNull(data) {
		return nil
	}
	var u typeStruct
	if err := json.Unmarshal(data, &u); err != nil {
		return err
	}
	switch ToolType(u.Type) {
	case ToolTypeFunction:
		return json.Unmarshal(data, &t.Function)
	case ToolTypeMCP:
		return json.Unmarshal(data, &t.MCP)
	default:
		return fmt.Errorf("unknown tool type: %s", u.Type)
	}
}

type TracingMode string

const (
	TracingModeAuto = "auto"
)

type TracingUnion struct {
	Mode          TracingMode           `json:",omitempty"`
	Configuration *TracingConfiguration `json:",omitempty"`
}

type TruncationStrategy string

const (
	TruncationStrategyAuto           TruncationStrategy = "auto"
	TruncationStrategyDisabled       TruncationStrategy = "disabled"
	TruncationStrategyRetentionRatio TruncationStrategy = "retention_ratio"
)

func (t TruncationStrategy) TruncationStrategy() string {
	return string(t)
}

type RetentionRatioTruncation struct {
	Ratio float32 `json:"retention_ratio,omitempty"`
}

func (t RetentionRatioTruncation) TruncationStrategy() string {
	return string(TruncationStrategyRetentionRatio)
}

type TruncationUnion struct {
	Strategy                 TruncationStrategy        `json:",omitempty"`
	RetentionRatioTruncation *RetentionRatioTruncation `json:",omitempty"`
}

const nullString = "null"

func isNull(data []byte) bool {
	return len(data) == len(nullString) && string(data) == nullString
}

func (t *TruncationUnion) UnmarshalJSON(data []byte) error {
	if isNull(data) {
		return nil
	}
	var u typeStruct
	if err := json.Unmarshal(data, &u); err != nil {
		t.Strategy = TruncationStrategy(bytes.Trim(data, "\""))
		return nil //nolint: nilerr // data is string instead of object
	}
	switch TruncationStrategy(u.Type) {
	case TruncationStrategyRetentionRatio:
		return json.Unmarshal(data, &t.RetentionRatioTruncation)
	case TruncationStrategyDisabled, TruncationStrategyAuto:
		t.Strategy = TruncationStrategy(data)
	default:
		return fmt.Errorf("unknown truncation strategy: %s", u.Type)
	}
	return nil
}

type ResponseAudioOutput struct {
	// The format of the output audio.
	Format *AudioFormatUnion `json:"format,omitempty"`

	// The voice the model uses to respond. Voice cannot be changed during the session once the model has responded with audio at least once. Current voice options are alloy, ash, ballad, coral, echo, sage, shimmer, verse, marin, and cedar. We recommend marin and cedar for best quality.
	Voice Voice `json:"voice,omitempty"`
}

type ResponseAudio struct {
	Output *ResponseAudioOutput `json:"output,omitempty"`
}

type MessageRole string

const (
	MessageRoleSystem    MessageRole = "system"
	MessageRoleAssistant MessageRole = "assistant"
	MessageRoleUser      MessageRole = "user"
)

type Tool struct {
	Type        ToolType `json:"type"`
	Name        string   `json:"name"`
	Description string   `json:"description"`
	Parameters  any      `json:"parameters"`
}

type ResponseMessageItem struct {
	MessageItemUnion
	// The object type, must be "realtime.item".
	Object string `json:"object,omitempty"`
}

type Error struct {
	// The type of error (e.g., "invalid_request_error", "server_error").
	Message string `json:"message,omitempty"`
	// Error code, if any.
	Type string `json:"type,omitempty"`
	// A human-readable error message.
	Code string `json:"code,omitempty"`
	// Parameter related to the error, if any.
	Param string `json:"param,omitempty"`
	// The event_id of the client event that caused the error, if applicable.
	EventID string `json:"event_id,omitempty"`
}

type AudioFormatType string

const (
	AudioFormatTypePCM  AudioFormatType = "audio/pcm"
	AudioFormatTypePCMU AudioFormatType = "audio/pcmu"
	AudioFormatTypePCMA AudioFormatType = "audio/pcma"
)

// The PCM audio format. Only a 24kHz sample rate is supported.
type AudioFormatPCM struct {
	// The sample rate of the audio. Always 24000.
	Rate int `json:"rate,omitempty"`
}

func (p AudioFormatPCM) AudioFormat() string {
	return string(AudioFormatTypePCM)
}

func (p AudioFormatPCM) MarshalJSON() ([]byte, error) {
	type typeAlias AudioFormatPCM
	type typeWrapper struct {
		typeAlias
		Type string `json:"type,omitempty"`
	}
	return json.Marshal(typeWrapper{
		typeAlias: typeAlias(p),
		Type:      p.AudioFormat(),
	})
}

// The G.711 μ-law format.
type AudioFormatPCMU struct {
}

func (p AudioFormatPCMU) AudioFormat() string {
	return string(AudioFormatTypePCMU)
}

func (p AudioFormatPCMU) MarshalJSON() ([]byte, error) {
	type typeAlias AudioFormatPCMU
	type typeWrapper struct {
		typeAlias
		Type string `json:"type,omitempty"`
	}
	return json.Marshal(typeWrapper{
		typeAlias: typeAlias(p),
		Type:      p.AudioFormat(),
	})
}

// The G.711 A-law format.
type AudioFormatPCMA struct {
}

func (p AudioFormatPCMA) AudioFormat() string {
	return string(AudioFormatTypePCMA)
}

func (p AudioFormatPCMA) MarshalJSON() ([]byte, error) {
	type typeAlias AudioFormatPCMA
	type typeWrapper struct {
		typeAlias
		Type string `json:"type,omitempty"`
	}
	return json.Marshal(typeWrapper{
		typeAlias: typeAlias(p),
		Type:      p.AudioFormat(),
	})
}

type AudioFormatUnion struct {
	// The PCM audio format. Only a 24kHz sample rate is supported.
	PCM *AudioFormatPCM `json:",omitempty"`

	// The G.711 μ-law format.
	PCMU *AudioFormatPCMU `json:",omitempty"`

	// The G.711 A-law format.
	PCMA *AudioFormatPCMA `json:",omitempty"`
}

func (r AudioFormatUnion) MarshalJSON() ([]byte, error) {
	if r.PCM != nil {
		return json.Marshal(r.PCM)
	}
	if r.PCMU != nil {
		return json.Marshal(r.PCMU)
	}
	if r.PCMA != nil {
		return json.Marshal(r.PCMA)
	}
	return nil, errors.New("no audio format")
}

func (r *AudioFormatUnion) UnmarshalJSON(data []byte) error {
	if isNull(data) {
		return nil
	}
	type typeStruct struct {
		Type string `json:"type"`
	}
	var t typeStruct
	if err := json.Unmarshal(data, &t); err != nil {
		return err
	}
	switch AudioFormatType(t.Type) {
	case AudioFormatTypePCM:
		r.PCM = &AudioFormatPCM{}
		return json.Unmarshal(data, r.PCM)
	case AudioFormatTypePCMU:
		r.PCMU = &AudioFormatPCMU{}
		return json.Unmarshal(data, r.PCMU)
	case AudioFormatTypePCMA:
		r.PCMA = &AudioFormatPCMA{}
		return json.Unmarshal(data, r.PCMA)
	default:
		return fmt.Errorf("unknown audio format: %s", t.Type)
	}
}

type AudioNoiseReduction struct {
	// Type of noise reduction. near_field is for close-talking microphones such as headphones, far_field is for far-field microphones such as laptop or conference room microphones.
	Type NoiseReductionType `json:"type,omitempty"`
}

type ServerVad struct {
	// Optional timeout after which a model response will be triggered automatically. This is useful for situations in which a long pause from the user is unexpected, such as a phone call. The model will effectively prompt the user to continue the conversation based on the current context.
	//
	// The timeout value will be applied after the last model response's audio has finished playing, i.e. it's set to the response.done time plus audio playback duration.
	//
	// An input_audio_buffer.timeout_triggered event (plus events associated with the Response) will be emitted when the timeout is reached. Idle timeout is currently only supported for server_vad mode.
	IdleTimeoutMs int64 `json:"idle_timeout_ms,omitempty"`

	// Whether or not to automatically generate a response when a VAD stop event occurs.
	CreateResponse bool `json:"create_response,omitempty"`

	// Whether or not to automatically interrupt any ongoing response with output to the default conversation (i.e. conversation of auto) when a VAD start event occurs.
	InterruptResponse bool `json:"interrupt_response,omitempty"`

	// Used only for server_vad mode. Amount of audio to include before the VAD detected speech (in milliseconds). Defaults to 300ms.
	PrefixPaddingMs int64 `json:"prefix_padding_ms,omitempty"`

	// Used only for server_vad mode. Duration of silence to detect speech stop (in milliseconds). Defaults to 500ms. With shorter values the model will respond more quickly, but may jump in on short pauses from the user.
	SilenceDurationMs int64 `json:"silence_duration_ms,omitempty"`

	// Used only for server_vad mode. Activation threshold for VAD (0.0 to 1.0), this defaults to 0.5. A higher threshold will require louder audio to activate the model, and thus might perform better in noisy environments.
	Threshold float64 `json:"threshold,omitempty"`
}

func (r ServerVad) VadType() TurnDetectionType {
	return TurnDetectionTypeServerVad
}

func (r ServerVad) MarshalJSON() ([]byte, error) {
	type typeAlias ServerVad
	type typeWrapper struct {
		typeAlias
		Type TurnDetectionType `json:"type,omitempty"`
	}
	shadow := typeWrapper{
		typeAlias: typeAlias(r),
		Type:      TurnDetectionTypeServerVad,
	}
	return json.Marshal(shadow)
}

type RealtimeSessionSemanticVad struct {
	// Whether or not to automatically generate a response when a VAD stop event occurs.
	CreateResponse bool `json:"create_response,omitempty"`

	// Whether or not to automatically interrupt any ongoing response with output to the default conversation (i.e. conversation of auto) when a VAD start event occurs.
	InterruptResponse bool `json:"interrupt_response,omitempty"`

	// Used only for semantic_vad mode. The eagerness of the model to respond. low will wait longer for the user to continue speaking, high will respond more quickly. auto is the default and is equivalent to medium. low, medium, and high have max timeouts of 8s, 4s, and 2s respectively.
	Eagerness string `json:"eagerness,omitempty"`
}

func (r RealtimeSessionSemanticVad) VadType() TurnDetectionType {
	return TurnDetectionTypeSemanticVad
}

func (r RealtimeSessionSemanticVad) MarshalJSON() ([]byte, error) {
	type typeAlias RealtimeSessionSemanticVad
	type typeWrapper struct {
		typeAlias
		Type TurnDetectionType `json:"type,omitempty"`
	}
	shadow := typeWrapper{
		typeAlias: typeAlias(r),
		Type:      TurnDetectionTypeSemanticVad,
	}
	return json.Marshal(shadow)
}

type TurnDetectionUnion struct {
	// Server-side voice activity detection (VAD) which flips on when user speech is detected and off after a period of silence.
	ServerVad *ServerVad `json:",omitempty"`

	// Server-side semantic turn detection which uses a model to determine when the user has finished speaking.
	SemanticVad *RealtimeSessionSemanticVad `json:",omitempty"`
}

func (r TurnDetectionUnion) MarshalJSON() ([]byte, error) {
	if r.ServerVad != nil {
		return json.Marshal(r.ServerVad)
	}
	if r.SemanticVad != nil {
		return json.Marshal(r.SemanticVad)
	}
	return nil, errors.New("no turn detection")
}

func (r *TurnDetectionUnion) UnmarshalJSON(data []byte) error {
	if isNull(data) {
		return nil
	}
	var t typeStruct
	if err := json.Unmarshal(data, &t); err != nil {
		return err
	}
	switch TurnDetectionType(t.Type) {
	case TurnDetectionTypeServerVad:
		return json.Unmarshal(data, &r.ServerVad)
	case TurnDetectionTypeSemanticVad:
		return json.Unmarshal(data, &r.SemanticVad)
	default:
		return fmt.Errorf("unknown turn detection type: %s", t.Type)
	}
}

type AudioTranscription struct {
	// The language of the input audio. Supplying the input language in ISO-639-1 (e.g. en) format will improve accuracy and latency.
	Language string `json:"language,omitempty"`

	// An optional text to guide the model's style or continue a previous audio segment. For whisper-1, the prompt is a list of keywords. For gpt-4o-transcribe models (excluding gpt-4o-transcribe-diarize), the prompt is a free text string, for example "expect words related to technology".
	Prompt string `json:"prompt,omitempty"`

	// The model to use for transcription. Current options are whisper-1, gpt-4o-mini-transcribe, gpt-4o-transcribe, and gpt-4o-transcribe-diarize. Use gpt-4o-transcribe-diarize when you need diarization with speaker labels.
	Model string `json:"model,omitempty"`
}

type SessionAudioInput struct {
	Format *AudioFormatUnion `json:"format,omitempty"`

	// Configuration for input audio noise reduction. This can be set to null to turn off. Noise reduction filters audio added to the input audio buffer before it is sent to VAD and the model. Filtering the audio can improve VAD and turn detection accuracy (reducing false positives) and model performance by improving perception of the input audio.
	NoiseReduction *AudioNoiseReduction `json:"noise_reduction,omitempty"`

	// Configuration for input audio transcription, defaults to off and can be set to null to turn off once on. Input audio transcription is not native to the model, since the model consumes audio directly. Transcription runs asynchronously through the /audio/transcriptions endpoint and should be treated as guidance of input audio content rather than precisely what the model heard. The client can optionally set the language and prompt for transcription, these offer additional guidance to the transcription service.
	TurnDetection *TurnDetectionUnion `json:"turn_detection,omitempty"`

	// Configuration for turn detection, ether Server VAD or Semantic VAD. This can be set to null to turn off, in which case the client must manually trigger model response.
	//
	// Server VAD means that the model will detect the start and end of speech based on audio volume and respond at the end of user speech.
	//
	// Semantic VAD is more advanced and uses a turn detection model (in conjunction with VAD) to semantically estimate whether the user has finished speaking, then dynamically sets a timeout based on this probability. For example, if user audio trails off with "uhhm", the model will score a low probability of turn end and wait longer for the user to continue speaking. This can be useful for more natural conversations, but may have a higher latency.
	Transcription *AudioTranscription `json:"transcription,omitempty"`
}

type SessionAudioOutput struct {
	Format *AudioFormatUnion `json:"format,omitempty"`
	Speed  float32           `json:"speed,omitempty"`
	Voice  Voice             `json:"voice,omitempty"`
}

type RealtimeSessionAudio struct {
	Input  *SessionAudioInput  `json:"input,omitempty"`
	Output *SessionAudioOutput `json:"output,omitempty"`
}

type TranscriptionSessionAudio struct {
	Input *SessionAudioInput `json:"input,omitempty"`
}

type PromptInputType string

const (
	PromptInputTypeText  PromptInputType = "input_text"
	PromptInputTypeImage PromptInputType = "input_image"
	PromptInputTypeFile  PromptInputType = "input_file"
)

// The detail level of the image to be sent to the model. One of `high`, `low`, or
// `auto`. Defaults to `auto`.
type ImageDetail string

const (
	ImageDetailLow  ImageDetail = "low"
	ImageDetailHigh ImageDetail = "high"
	ImageDetailAuto ImageDetail = "auto"
)

type PromptInputText struct {
	Text string `json:"text"`
}

func (r PromptInputText) PromptInputType() PromptInputType {
	return PromptInputTypeText
}

func (r PromptInputText) MarshalJSON() ([]byte, error) {
	type typeAlias PromptInputText
	type typeWrapper struct {
		typeAlias
		Type PromptInputType `json:"type,omitempty"`
	}
	shadow := typeWrapper{
		typeAlias: typeAlias(r),
		Type:      r.PromptInputType(),
	}
	return json.Marshal(shadow)
}

type PromptInputImage struct {
	Detail   ImageDetail `json:"detail,omitempty"`
	FileID   string      `json:"file_id,omitempty"`
	ImageURL string      `json:"image_url,omitempty"`
}

func (r PromptInputImage) PromptInputType() PromptInputType {
	return PromptInputTypeImage
}

func (r PromptInputImage) MarshalJSON() ([]byte, error) {
	type typeAlias PromptInputImage
	type typeWrapper struct {
		typeAlias
		Type PromptInputType `json:"type,omitempty"`
	}
	shadow := typeWrapper{
		typeAlias: typeAlias(r),
		Type:      r.PromptInputType(),
	}
	return json.Marshal(shadow)
}

type PromptInputFile struct {
	FileID   string `json:"file_id,omitempty"`
	FileData string `json:"file_data,omitempty"`
	FileURL  string `json:"file_url,omitempty"`
	Filename string `json:"filename,omitempty"`
}

func (r PromptInputFile) PromptInputType() PromptInputType {
	return PromptInputTypeFile
}

func (r PromptInputFile) MarshalJSON() ([]byte, error) {
	type typeAlias PromptInputFile
	type typeWrapper struct {
		typeAlias
		Type PromptInputType `json:"type,omitempty"`
	}
	shadow := typeWrapper{
		typeAlias: typeAlias(r),
		Type:      r.PromptInputType(),
	}
	return json.Marshal(shadow)
}

type PromptVariableUnion struct {
	String     string            `json:",omitempty"`
	InputText  *PromptInputText  `json:",omitempty"`
	InputImage *PromptInputImage `json:",omitempty"`
	InputFile  *PromptInputFile  `json:",omitempty"`
}

type typeStruct struct {
	Type string `json:"type"`
}

func (u *PromptVariableUnion) UnmarshalJSON(data []byte) error {
	if isNull(data) {
		return nil
	}
	var t typeStruct
	if err := json.Unmarshal(data, &t); err != nil {
		return err
	}
	switch PromptInputType(t.Type) {
	case PromptInputTypeText:
		u.InputText = &PromptInputText{}
		return json.Unmarshal(data, u.InputText)
	case PromptInputTypeImage:
		u.InputImage = &PromptInputImage{}
		return json.Unmarshal(data, u.InputImage)
	case PromptInputTypeFile:
		u.InputFile = &PromptInputFile{}
		return json.Unmarshal(data, u.InputFile)
	default:
		return fmt.Errorf("unknown input type: %s", t.Type)
	}
}

type PromptReference struct {
	// The unique identifier of the prompt template to use.
	ID string `json:"id,omitempty"`

	// Optional version of the prompt template.
	Version string `json:"version,omitempty"`

	// Optional map of values to substitute in for variables in your prompt. The substitution values can either be strings, or other Response input types like images or files.
	Variables map[string]PromptVariableUnion `json:"variables,omitempty"`
}

type SessionType string

const (
	SessionTypeRealtime      SessionType = "realtime"
	SessionTypeTranscription SessionType = "transcription"
)

type RealtimeSession struct {
	// Unique identifier for the session that looks like sess_1234567890abcdef.
	ID string `json:"id,omitempty"`

	// Expiration timestamp for the session, in seconds since epoch.
	ExpiresAt int64 `json:"expires_at,omitempty"`

	// The object type. Always realtime.session.
	Object string `json:"object,omitempty"`

	// Configuration for input and output audio.
	Audio *RealtimeSessionAudio `json:"audio,omitempty"`

	// Additional fields to include in server outputs.
	//
	// `item.input_audio_transcription.logprobs`: Include logprobs for input audio
	// transcription.
	//
	// Any of "item.input_audio_transcription.logprobs".
	Include []string `json:"include,omitempty"`

	// The default system instructions (i.e. system message) prepended to model calls. This field allows the client to guide the model on desired responses. The model can be instructed on response content and format, (e.g. "be extremely succinct", "act friendly", "here are examples of good responses") and on audio behavior (e.g. "talk quickly", "inject emotion into your voice", "laugh frequently"). The instructions are not guaranteed to be followed by the model, but they provide guidance to the model on the desired behavior.
	//
	// Note that the server sets default instructions which will be used if this field is not set and are visible in the session.created event at the start of the session.
	Instructions string `json:"instructions,omitempty"`

	// Maximum number of output tokens for a single assistant response, inclusive of tool calls. Provide an integer between 1 and 4096 to limit output tokens, or inf for the maximum available tokens for a given model. Defaults to inf.
	MaxOutputTokens IntOrInf `json:"max_output_tokens,omitempty"`

	// The Realtime model used for this session.
	Model string `json:"model,omitempty"`

	// The set of modalities the model can respond with. It defaults to ["audio"], indicating that the model will respond with audio plus a transcript. ["text"] can be used to make the model respond with text only. It is not possible to request both text and audio at the same time.
	OutputModalities []Modality `json:"output_modalities,omitempty"`

	// Reference to a prompt template and its variables.
	Prompt *PromptReference `json:"prompt,omitempty"`

	// How the model chooses tools. Provide one of the string modes or force a specific function/MCP tool.
	ToolChoice *ToolChoiceUnion `json:"tool_choice,omitempty"`

	// Tools available to the model.
	Tools []ToolUnion `json:"tools,omitempty"`

	// Realtime API can write session traces to the Traces Dashboard. Set to null to disable tracing. Once tracing is enabled for a session, the configuration cannot be modified.
	//
	// auto will create a trace for the session with default values for the workflow name, group id, and metadata.
	Tracing *TracingUnion `json:"tracing,omitempty"`

	// Controls how the realtime conversation is truncated prior to model inference. The default is auto.
	Truncation *TruncationUnion `json:"truncation,omitempty"`
}

func (r RealtimeSession) Type() SessionType {
	return SessionTypeRealtime
}

func (r RealtimeSession) MarshalJSON() ([]byte, error) {
	type typeAlias RealtimeSession
	type typeWrapper struct {
		typeAlias
		Type SessionType `json:"type"`
	}
	shadow := typeWrapper{
		typeAlias: typeAlias(r),
		Type:      r.Type(),
	}
	return json.Marshal(shadow)
}

type TranscriptionSession struct {
	// Unique identifier for the session that looks like sess_1234567890abcdef.
	ID string `json:"id,omitempty"`

	// Expiration timestamp for the session, in seconds since epoch.
	ExpiresAt int64 `json:"expires_at,omitempty"`

	// The object type. Always realtime.transcription_session.
	Object string `json:"object,omitempty"`

	// Configuration for input audio.
	Audio *TranscriptionSessionAudio `json:"audio,omitempty"`

	// Additional fields to include in server outputs.
	//
	// `item.input_audio_transcription.logprobs`: Include logprobs for input audio
	// transcription.
	//
	// Any of "item.input_audio_transcription.logprobs".
	Include []string `json:"include,omitempty"`
}

func (r TranscriptionSession) Type() SessionType {
	return SessionTypeTranscription
}

func (r TranscriptionSession) MarshalJSON() ([]byte, error) {
	type typeAlias TranscriptionSession
	type typeWrapper struct {
		typeAlias
		Type SessionType `json:"type"`
	}
	shadow := typeWrapper{
		typeAlias: typeAlias(r),
		Type:      r.Type(),
	}
	return json.Marshal(shadow)
}

type SessionUnion struct {
	// Realtime session object configuration.
	Realtime *RealtimeSession `json:"realtime,omitempty"`

	// Realtime transcription session object configuration.
	Transcription *TranscriptionSession `json:"transcription,omitempty"`
}

func (r SessionUnion) MarshalJSON() ([]byte, error) {
	if r.Realtime != nil {
		return json.Marshal(r.Realtime)
	}
	if r.Transcription != nil {
		return json.Marshal(r.Transcription)
	}
	return nil, errors.New("no session type")
}

func (r *SessionUnion) UnmarshalJSON(data []byte) error {
	if isNull(data) {
		return nil
	}
	var t typeStruct
	if err := json.Unmarshal(data, &t); err != nil {
		return err
	}
	switch SessionType(t.Type) {
	case SessionTypeRealtime:
		return json.Unmarshal(data, &r.Realtime)
	case SessionTypeTranscription:
		return json.Unmarshal(data, &r.Transcription)
	default:
		return fmt.Errorf("unknown session type: %s", t.Type)
	}
}

type ItemStatus string

const (
	ItemStatusInProgress ItemStatus = "in_progress"
	ItemStatusCompleted  ItemStatus = "completed"
	ItemStatusIncomplete ItemStatus = "incomplete"
)

type Conversation struct {
	// The unique ID of the conversation.
	ID string `json:"id"`
	// The object type, must be "realtime.conversation".
	Object string `json:"object"`
}

type ResponseStatus string

const (
	ResponseStatusInProgress ResponseStatus = "in_progress"
	ResponseStatusCompleted  ResponseStatus = "completed"
	ResponseStatusCancelled  ResponseStatus = "cancelled"
	ResponseStatusIncomplete ResponseStatus = "incomplete"
	ResponseStatusFailed     ResponseStatus = "failed"
)

type UsageType string

const (
	UsageTypeTokens   UsageType = "tokens"
	UsageTypeDuration UsageType = "duration"
)

type CachedTokensDetails struct {
	TextTokens  int `json:"text_tokens"`
	AudioTokens int `json:"audio_tokens"`
}

type InputTokenDetails struct {
	CachedTokens        int                  `json:"cached_tokens"`
	TextTokens          int                  `json:"text_tokens"`
	AudioTokens         int                  `json:"audio_tokens"`
	CachedTokensDetails *CachedTokensDetails `json:"cached_tokens_details,omitempty"`
}

type OutputTokenDetails struct {
	TextTokens  int `json:"text_tokens"`
	AudioTokens int `json:"audio_tokens"`
}

type TokenUsage struct {
	TotalTokens  int `json:"total_tokens"`
	InputTokens  int `json:"input_tokens"`
	OutputTokens int `json:"output_tokens"`
	// Input token details.
	InputTokenDetails *InputTokenDetails `json:"input_token_details,omitempty"`
	// Output token details.
	OutputTokenDetails *OutputTokenDetails `json:"output_token_details,omitempty"`
}

func (u TokenUsage) UsageType() UsageType {
	return UsageTypeTokens
}

type DurationUsage struct {
	Seconds float64 `json:"seconds"`
}

func (u DurationUsage) UsageType() UsageType {
	return UsageTypeDuration
}

type UsageUnion struct {
	Tokens   *TokenUsage    `json:",omitempty"`
	Duration *DurationUsage `json:",omitempty"`
}

func (u *UsageUnion) UnmarshalJSON(data []byte) error {
	if isNull(data) {
		return nil
	}
	var t typeStruct
	if err := json.Unmarshal(data, &t); err != nil {
		return err
	}
	switch UsageType(t.Type) {
	case UsageTypeTokens:
		return json.Unmarshal(data, &u.Tokens)
	case UsageTypeDuration:
		return json.Unmarshal(data, &u.Duration)
	default:
		return fmt.Errorf("unknown usage type: %s", t.Type)
	}
}

type StatusDetail struct {
	Error  *Error `json:"error,omitempty"`
	Reason string `json:"reason,omitempty"`
	Type   string `json:"type,omitempty"`
}

type ResponseCreateParams struct {
	// Configuration for audio input and output.
	Audio *ResponseAudio `json:"audio,omitempty"`

	// Controls which conversation the response is added to. Currently supports auto and none, with auto as the default value. The auto value means that the contents of the response will be added to the default conversation. Set this to none to create an out-of-band response which will not add items to default conversation.
	Conversation string `json:"conversation,omitempty"`

	// Input items to include in the prompt for the model. Using this field creates a new context for this Response instead of using the default conversation. An empty array [] will clear the context for this Response. Note that this can include references to items that previously appeared in the session using their id.
	Input []MessageItemUnion `json:"input,omitempty"`

	// The default system instructions (i.e. system message) prepended to model calls. This field allows the client to guide the model on desired responses. The model can be instructed on response content and format, (e.g. "be extremely succinct", "act friendly", "here are examples of good responses") and on audio behavior (e.g. "talk quickly", "inject emotion into your voice", "laugh frequently"). The instructions are not guaranteed to be followed by the model, but they provide guidance to the model on the desired behavior. Note that the server sets default instructions which will be used if this field is not set and are visible in the session.created event at the start of the session.
	Instructions string `json:"instructions,omitempty"`

	// Maximum number of output tokens for a single assistant response, inclusive of tool calls. Provide an integer between 1 and 4096 to limit output tokens, or inf for the maximum available tokens for a given model. Defaults to inf.
	MaxOutputTokens IntOrInf `json:"max_output_tokens,omitempty"`

	// Set of 16 key-value pairs that can be attached to an object. This can be useful for storing additional information about the object in a structured format, and querying for objects via API or the dashboard.
	//
	// Keys are strings with a maximum length of 64 characters. Values are strings with a maximum length of 512 characters.
	Metadata map[string]string `json:"metadata,omitempty"`

	// The set of modalities the model used to respond, currently the only possible values are [\"audio\"], [\"text\"]. Audio output always include a text transcript. Setting the output to mode text will disable audio output from the model.
	OutputModalities []Modality `json:"output_modalities,omitempty"`

	// Reference to a prompt template and its variables.
	//
	// See https://platform.openai.com/docs/guides/text?api-mode=responses#reusable-prompts.
	Prompt *PromptReference `json:"prompt,omitempty"`

	// How the model chooses tools. Provide one of the string modes or force a specific function/MCP tool.
	ToolChoice *ToolChoiceUnion `json:"tool_choice,omitempty"`

	// Tools available to the model.
	Tools []ToolUnion `json:"tools,omitempty"`
}

type Response struct {
	Audio *ResponseAudio `json:"audio,omitempty"`

	ConversationID string `json:"conversation_id,omitempty"`

	// The unique ID of the response.
	ID string `json:"id"`

	MaxOutputTokens IntOrInf `json:"max_output_tokens,omitempty"`

	Metadata map[string]string `json:"metadata,omitempty"`

	// The object type, must be "realtime.response".
	Object string `json:"object,omitempty"`

	Output []MessageItemUnion `json:"output,omitempty"`

	OutputModalities []Modality `json:"output_modalities,omitempty"`

	// The status of the response.
	Status ResponseStatus `json:"status,omitempty"`
	// Additional details about the status.
	StatusDetails *StatusDetail `json:"status_details,omitempty"`

	Usage *TokenUsage `json:"usage,omitempty"`
}

type RateLimit struct {
	// The name of the rate limit ("requests", "tokens", "input_tokens", "output_tokens").
	Name string `json:"name,omitempty"`
	// The maximum allowed value for the rate limit.
	Limit int `json:"limit,omitempty"`
	// The remaining value before the limit is reached.
	Remaining int `json:"remaining,omitempty"`
	// Seconds until the rate limit resets.
	ResetSeconds float64 `json:"reset_seconds,omitempty"`
}
