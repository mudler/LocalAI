package templates

import (
	"bytes"
	"text/template"
)

func TemplateMultiModal(templateString string, templateID int, text string) (string, error) {
	// compile the template
	tmpl, err := template.New("template").Parse(templateString)
	if err != nil {
		return "", err
	}
	result := bytes.NewBuffer(nil)
	// execute the template
	err = tmpl.Execute(result, struct {
		ID   int
		Text string
	}{
		ID:   templateID,
		Text: text,
	})
	return result.String(), err
}
