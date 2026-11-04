package pdf

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/pdfcpu/pdfcpu/pkg/api"
	"github.com/pdfcpu/pdfcpu/pkg/pdfcpu/form"
)

// FormField is a named AcroForm text field and its current value.
type FormField struct {
	Name  string
	Value string
}

// Fields lists the named AcroForm text fields, sorted by name. Checkboxes,
// radio buttons, lists and other field kinds are left out. A PDF without a
// form gives an empty list.
func Fields(data []byte) ([]FormField, error) {
	if err := checkInput(data); err != nil {
		return nil, err
	}
	var out []FormField
	err := guard("fields", func() error {
		fields, err := api.FormFields(bytes.NewReader(data), newConf())
		if err != nil {
			// pdfcpu v0.15.0 has no sentinel for a missing form. A PDF
			// without an AcroForm has no text fields, so it is not an error.
			if strings.Contains(err.Error(), "no form available") {
				return nil
			}
			return fmt.Errorf("pdf: fields: %w", err)
		}
		seen := map[string]bool{}
		for _, f := range fields {
			if f.Typ != form.FTText || f.Name == "" || seen[f.Name] {
				continue
			}
			seen[f.Name] = true
			out = append(out, FormField{Name: f.Name, Value: f.V})
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

// FillFields sets the values of the named text fields and returns the new
// PDF. A name that is not a text field of the form is an error, and nothing
// is returned for it.
func FillFields(data []byte, values map[string]string) ([]byte, error) {
	if err := checkInput(data); err != nil {
		return nil, err
	}
	if len(values) == 0 {
		return nil, errors.New("pdf: fill: no values")
	}
	fields, err := Fields(data)
	if err != nil {
		return nil, err
	}
	known := make(map[string]bool, len(fields))
	for _, f := range fields {
		known[f.Name] = true
	}
	names := make([]string, 0, len(values))
	for name := range values {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		if !known[name] {
			return nil, fmt.Errorf("pdf: fill: no text field named %q", name)
		}
	}

	return guard2("fill", func() ([]byte, error) {
		group, err := api.ExportForm(bytes.NewReader(data), "", newConf())
		if err != nil {
			return nil, fmt.Errorf("pdf: fill: %w", err)
		}
		if len(group.Forms) == 0 {
			return nil, errors.New("pdf: fill: no form data")
		}
		set := 0
		for i := range group.Forms {
			for _, tf := range group.Forms[i].TextFields {
				if tf == nil {
					continue
				}
				if v, ok := values[tf.Name]; ok {
					tf.Value = v
					set++
				}
			}
		}
		if set == 0 {
			return nil, errors.New("pdf: fill: no field was set")
		}
		js, err := json.Marshal(group)
		if err != nil {
			return nil, fmt.Errorf("pdf: fill: encode values: %w", err)
		}
		var out bytes.Buffer
		if err := api.FillForm(bytes.NewReader(data), bytes.NewReader(js), &out, newConf()); err != nil {
			return nil, fmt.Errorf("pdf: fill: %w", err)
		}
		return out.Bytes(), nil
	})
}

// guard2 is guard for a call that returns bytes.
func guard2(op string, fn func() ([]byte, error)) ([]byte, error) {
	var out []byte
	err := guard(op, func() error {
		b, err := fn()
		out = b
		return err
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}
