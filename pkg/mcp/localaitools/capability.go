package localaitools

// Capability is the human-readable tag the LLM uses to filter installed
// models by purpose. The chat handler maps it to the loader's bitflag
// (config.FLAG_*) — see inproc.Client.capabilityToFlag. The empty value
// means "no filter".
//
// Renaming or adding values is a public-API change: tool DTOs reference
// these constants in their jsonschema enum, so the LLM sees the canonical
// list at tools/list time.
type Capability string

const (
	// CapabilityAny is the explicit zero value — equivalent to no filter.
	CapabilityAny Capability = ""

	CapabilityChat       Capability = "chat"
	CapabilityCompletion Capability = "completion"
	CapabilityEmbeddings Capability = "embeddings"
	CapabilityImage      Capability = "image"
	CapabilityTTS        Capability = "tts"
	CapabilityTranscript Capability = "transcript"
	CapabilityRerank     Capability = "rerank"
	CapabilityVAD        Capability = "vad"
)
