package remotes

import (
	"context"
	"sort"
	"strings"
)

// nonChatMarkers are parts of a model id that mark a model that is not for
// chat. It is a heuristic: the OpenAI listing says nothing about what a model
// does, so the ids are judged by name. A model that is wrongly dropped can be
// added by a person later; one that is wrongly kept fails on its first chat.
var nonChatMarkers = []string{
	"embed", // embedding, embed-*
	"whisper",
	"transcribe",
	"tts",
	"dall-e",
	"image",
	"moderation",
	"audio",
	"realtime",
}

// looksLikeChatModel reports whether a model id from an OpenAI-compatible
// listing is probably a chat model.
func looksLikeChatModel(id string) bool {
	id = strings.ToLower(id)
	for _, m := range nonChatMarkers {
		if strings.Contains(id, m) {
			return false
		}
	}
	return true
}

// listOpenAI reads {base}/models and keeps the ids that look like chat models.
// The cloud kinds serve chat only, so the usecases stay empty.
func (f fetcher) listOpenAI(ctx context.Context, t target) ([]UpstreamModel, error) {
	var list modelsList
	if err := f.get(ctx, t.endpoint("/models", nil), bearer(t.key), &list); err != nil {
		return nil, err
	}
	seen := map[string]bool{}
	models := make([]UpstreamModel, 0, len(list.Data))
	for _, m := range list.Data {
		if m.ID == "" || seen[m.ID] || !looksLikeChatModel(m.ID) {
			continue
		}
		seen[m.ID] = true
		models = append(models, UpstreamModel{ID: m.ID, Usecases: []string{}})
	}
	sort.Slice(models, func(i, j int) bool { return models[i].ID < models[j].ID })
	return models, nil
}
