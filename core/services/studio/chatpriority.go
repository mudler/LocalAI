package studio

import (
	"github.com/mudler/LocalAI/core/priority"
	"github.com/mudler/LocalAI/core/services/studio/session"
)

// chatPriority is the class the backend serves a chat's model requests with.
// A chat a person types in, and a connector chat where someone waits on the
// other side, are Interactive. A sub-agent, and a chat a trigger, a job or a
// page started, run on their own and nobody watches them token by token, so
// they are Batch and yield to the chats people wait on.
func chatPriority(rec session.ChatRecord) priority.Class {
	if rec.Kind == session.KindSubAgent {
		return priority.Batch
	}
	switch rec.Origin {
	case "", session.OriginConnector:
		return priority.Interactive
	case session.OriginTrigger, session.OriginJob, session.OriginPage:
		return priority.Batch
	}
	return priority.Default
}
