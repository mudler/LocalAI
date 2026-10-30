package application

import (
	"sort"

	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/core/services/studio/media"
)

// chooseHelperModel returns the model a media tool uses: the one the setting
// names when it is installed, otherwise the first installed model, by name,
// that has the capability. It returns "" when there is none.
func chooseHelperModel(setting string, all []config.ModelConfig, flag config.ModelConfigUsecase) string {
	if setting != "" {
		for _, m := range all {
			if m.Name == setting {
				return setting
			}
		}
	}
	var names []string
	for i := range all {
		if all[i].HasUsecases(flag) {
			names = append(names, all[i].Name)
		}
	}
	if len(names) == 0 {
		return ""
	}
	sort.Strings(names)
	return names[0]
}

// chooseVisionModel returns the model describe_image uses in a chat whose own
// model is chatModel: the one the setting names when it is installed, then the
// chat's own model when it can look at images, then the first installed model
// that can. The chat's model comes before any other model because the person
// chose it, and it may be the only one they may use. It returns "" when no
// model can look at images.
func chooseVisionModel(setting, chatModel string, all []config.ModelConfig) string {
	if setting != "" {
		for _, m := range all {
			if m.Name == setting {
				return setting
			}
		}
	}
	if chatModel != "" {
		for i := range all {
			if all[i].Name == chatModel && all[i].HasUsecases(config.FLAG_VISION) {
				return chatModel
			}
		}
	}
	return chooseHelperModel("", all, config.FLAG_VISION)
}

// studioVisionModel is describe_image's chooser, read when the tool runs, so
// a change of the setting or of a model applies at once.
func (a *Application) studioVisionModel(chatModel string) string {
	return chooseVisionModel(a.applicationConfig.ChatsVisionModel, chatModel, a.ModelConfigLoader().GetAllModelsConfigs())
}

// studioHelperModel is the media tools' resolver: the instance settings, read
// when a tool runs, so a change applies at once.
func (a *Application) studioHelperModel(c media.Capability) string {
	cfg := a.applicationConfig
	all := a.ModelConfigLoader().GetAllModelsConfigs()
	if c == media.Vision {
		return chooseHelperModel(cfg.ChatsVisionModel, all, config.FLAG_VISION)
	}
	return chooseHelperModel(cfg.ChatsTranscriptionModel, all, config.FLAG_TRANSCRIPT)
}
