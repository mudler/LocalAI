package templates

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"text/template"

	"github.com/mudler/LocalAI/pkg/utils"

	"github.com/Masterminds/sprig/v3"
)

// Keep this in sync with config.TemplateConfig. Is there a more idiomatic way to accomplish this in go?
// Technically, order doesn't _really_ matter, but the count must stay in sync, see tests/integration/reflect_test.go
type TemplateType int

type templateCache struct {
	mu            sync.Mutex
	templatesPath string
	templates     map[TemplateType]map[string]*template.Template
}

func newTemplateCache(templatesPath string) *templateCache {
	tc := &templateCache{
		templatesPath: templatesPath,
		templates:     make(map[TemplateType]map[string]*template.Template),
	}
	return tc
}

func (tc *templateCache) initializeTemplateMapKey(tt TemplateType) {
	if _, ok := tc.templates[tt]; !ok {
		tc.templates[tt] = make(map[string]*template.Template)
	}
}

func (tc *templateCache) existsInModelPath(s string) bool {
	return utils.ExistsInPath(tc.templatesPath, s)
}
func (tc *templateCache) loadTemplateIfExists(templateType TemplateType, templateName string) error {

	// Check if the template was already loaded
	if _, ok := tc.templates[templateType][templateName]; ok {
		return nil
	}

	// Check if the model path exists
	// skip any error here - we run anyway if a template does not exist
	modelTemplateFile := fmt.Sprintf("%s.tmpl", templateName)

	dat := ""
	file := filepath.Join(tc.templatesPath, modelTemplateFile)

	// Security check
	if err := utils.VerifyPath(modelTemplateFile, tc.templatesPath); err != nil {
		return fmt.Errorf("template file outside path: %s", file)
	}

	// can either be a file in the system or a string with the template
	if tc.existsInModelPath(modelTemplateFile) {
		d, err := os.ReadFile(file)
		if err != nil {
			return err
		}
		dat = string(d)
	} else {
		dat = templateName
	}

	// Parse the template
	tmpl, err := template.New("prompt").Funcs(sprig.FuncMap()).Parse(dat)
	if err != nil {
		return err
	}
	tc.templates[templateType][templateName] = tmpl

	return nil
}

func (tc *templateCache) evaluateTemplate(templateType TemplateType, templateNameOrContent string, in interface{}) (string, error) {
	tc.mu.Lock()
	defer tc.mu.Unlock()

	tc.initializeTemplateMapKey(templateType)
	m, ok := tc.templates[templateType][templateNameOrContent]
	if !ok {
		// return "", fmt.Errorf("template not loaded: %s", templateName)
		loadErr := tc.loadTemplateIfExists(templateType, templateNameOrContent)
		if loadErr != nil {
			return "", loadErr
		}
		m = tc.templates[templateType][templateNameOrContent] // ok is not important since we check m on the next line, and wealready checked
	}
	if m == nil {
		return "", fmt.Errorf("failed loading a template for %s", templateNameOrContent)
	}

	var buf bytes.Buffer

	if err := m.Execute(&buf, in); err != nil {
		return "", err
	}
	return buf.String(), nil
}
