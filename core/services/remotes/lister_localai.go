package remotes

import (
	"context"
	"sort"

	"github.com/mudler/LocalAI/core/config"
)

type modelsList struct {
	Data []struct {
		ID string `json:"id"`
	} `json:"data"`
}

type capabilitiesList struct {
	Data []struct {
		ID           string   `json:"id"`
		Capabilities []string `json:"capabilities"`
	} `json:"data"`
}

// listLocalAI reads the model ids from /v1/models and what each model can do
// from /v1/models/capabilities. A remote that does not answer the second call
// (an older version, or a key that may not use it) gets chat for every model.
func (f fetcher) listLocalAI(ctx context.Context, t target) ([]UpstreamModel, error) {
	var list modelsList
	if err := f.get(ctx, t.endpoint("/v1/models", nil), bearer(t.key), &list); err != nil {
		return nil, err
	}
	usecases := map[string][]string{}
	var caps capabilitiesList
	if err := f.get(ctx, t.endpoint("/v1/models/capabilities", nil), bearer(t.key), &caps); err == nil {
		for _, m := range caps.Data {
			usecases[m.ID] = validUsecases(m.Capabilities)
		}
	}
	seen := map[string]bool{}
	models := make([]UpstreamModel, 0, len(list.Data))
	for _, m := range list.Data {
		if m.ID == "" || seen[m.ID] {
			continue
		}
		seen[m.ID] = true
		u, ok := usecases[m.ID]
		if !ok {
			u = []string{config.UsecaseChat}
		}
		models = append(models, UpstreamModel{ID: m.ID, Usecases: u})
	}
	sort.Slice(models, func(i, j int) bool { return models[i].ID < models[j].ID })
	return models, nil
}
