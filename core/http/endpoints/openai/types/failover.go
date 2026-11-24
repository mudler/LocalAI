package types

import "encoding/json"

// ModelFailoverEvent is a LocalAI extension server event
// (localai.model.failover). It tells a client which target serves a
// pipeline stage that names a failover chain: once per chain stage at
// session start (reason "initial"), then on every switch of that chain.
type ModelFailoverEvent struct {
	ServerEventBase

	// The failover chain the stage names.
	Chain string `json:"chain"`

	// The pipeline stage: vad, transcription, llm, tts or sound_detection.
	Stage string `json:"stage"`

	// The target that served the stage before the switch; "" at session start.
	From string `json:"from"`

	// The target that serves the stage now.
	To string `json:"to"`

	// The chain state: primary, fallback or degraded.
	State string `json:"state"`

	// Why the chain switched, or "initial" at session start.
	Reason string `json:"reason"`
}

func (m ModelFailoverEvent) ServerEventType() ServerEventType {
	return ServerEventTypeModelFailover
}

func (m ModelFailoverEvent) MarshalJSON() ([]byte, error) {
	type typeAlias ModelFailoverEvent
	type typeWrapper struct {
		typeAlias
		Type ServerEventType `json:"type"`
	}
	shadow := typeWrapper{
		typeAlias: typeAlias(m),
		Type:      m.ServerEventType(),
	}
	return json.Marshal(shadow)
}
