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

const DefaultMultiModalTemplate = "{{ range .Audio }}[audio-{{.ID}}]{{end}}{{ range .Images }}[img-{{.ID}}]{{end}}{{ range .Video }}[vid-{{.ID}}]{{end}}{{.Text}}"

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
	for i := 0; i < opts.VideosInMessage; i++ {
		videos = append(videos, MultimodalContent{ID: i + (opts.TotalVideos - opts.VideosInMessage)})
	}

	audios := []MultimodalContent{}
	for i := 0; i < opts.AudiosInMessage; i++ {
		audios = append(audios, MultimodalContent{ID: i + (opts.TotalAudios - opts.AudiosInMessage)})
	}

	images := []MultimodalContent{}
	for i := 0; i < opts.ImagesInMessage; i++ {
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
