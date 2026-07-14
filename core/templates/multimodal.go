package templates

import (
	"bytes"
	"text/template"

	"github.com/Masterminds/sprig/v3"
)

type MultiModalOptions struct {
	TotalImages int
	TotalAudios int
	TotalVideos int

	ImagesInMessage int
	AudiosInMessage int
	VideosInMessage int
}

type MultimodalContent struct {
	ID int
}

// DefaultMultiMediaMarker is the sentinel marker LocalAI emits in the rendered
// prompt for each image/audio item. It matches llama.cpp's historical
// mtmd_default_marker() ("<__media__>"). llama.cpp's server now picks a random
// per-server marker (see PR #21962) and reports it via ModelMetadataResponse.media_marker;
// callers substitute this sentinel with the backend-reported marker right before
// the gRPC call (core/backend/llm.go).
const DefaultMultiMediaMarker = "<__media__>"

// DefaultMultiModalTemplate renders a per-message media-marker prefix followed
// by the text content. The sentinel marker is substituted late, so this
// template does not need to know the backend-specific marker.
//
// References:
//   - https://github.com/ggml-org/llama.cpp/blob/79c137f77677b3c8ee3c60a7da033721b938399a/tools/mtmd/mtmd.cpp#L83
//   - https://github.com/ggml-org/llama.cpp/pull/21962
const DefaultMultiModalTemplate = "{{ range .Audio }}<__media__>{{end}}{{ range .Images }}<__media__>{{end}}{{ range .Video }}[vid-{{.ID}}]{{end}}{{.Text}}"

func TemplateMultiModal(templateString string, opts MultiModalOptions, text string) (string, error) {
	if templateString == "" {
		templateString = DefaultMultiModalTemplate
	}

	// compile the template
	tmpl, err := template.New("template").Funcs(sprig.FuncMap()).Parse(templateString)
	if err != nil {
		return "", err
	}

	videos := []MultimodalContent{}
	for i := range opts.VideosInMessage {
		videos = append(videos, MultimodalContent{ID: i + (opts.TotalVideos - opts.VideosInMessage)})
	}

	audios := []MultimodalContent{}
	for i := range opts.AudiosInMessage {
		audios = append(audios, MultimodalContent{ID: i + (opts.TotalAudios - opts.AudiosInMessage)})
	}

	images := []MultimodalContent{}
	for i := range opts.ImagesInMessage {
		images = append(images, MultimodalContent{ID: i + (opts.TotalImages - opts.ImagesInMessage)})
	}

	result := bytes.NewBuffer(nil)
	// execute the template
	err = tmpl.Execute(result, struct {
		Audio  []MultimodalContent
		Images []MultimodalContent
		Video  []MultimodalContent
		Text   string
	}{
		Audio:  audios,
		Images: images,
		Video:  videos,
		Text:   text,
	})
	return result.String(), err
}
