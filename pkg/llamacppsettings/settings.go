package llamacppsettings

// Parameters is the subset of the model "parameters" block this package maps.
type Parameters struct {
	Model       string   `json:"model,omitempty" yaml:"model,omitempty"`
	Temperature *float64 `json:"temperature,omitempty" yaml:"temperature,omitempty"`
	TopP        *float64 `json:"top_p,omitempty" yaml:"top_p,omitempty"`
	TopK        *int     `json:"top_k,omitempty" yaml:"top_k,omitempty"`
	MinP        *float64 `json:"min_p,omitempty" yaml:"min_p,omitempty"`
	Batch       *int     `json:"batch,omitempty" yaml:"batch,omitempty"`
}

// Settings is the subset of a model config that has a llama-server flag. The
// field names are the model YAML keys.
type Settings struct {
	Parameters     *Parameters `json:"parameters,omitempty" yaml:"parameters,omitempty"`
	ContextSize    *int        `json:"context_size,omitempty" yaml:"context_size,omitempty"`
	Threads        *int        `json:"threads,omitempty" yaml:"threads,omitempty"`
	GPULayers      *int        `json:"gpu_layers,omitempty" yaml:"gpu_layers,omitempty"`
	FlashAttention *string     `json:"flash_attention,omitempty" yaml:"flash_attention,omitempty"`
	CacheTypeK     string      `json:"cache_type_k,omitempty" yaml:"cache_type_k,omitempty"`
	CacheTypeV     string      `json:"cache_type_v,omitempty" yaml:"cache_type_v,omitempty"`
	MMap           *bool       `json:"mmap,omitempty" yaml:"mmap,omitempty"`
	MMlock         *bool       `json:"mmlock,omitempty" yaml:"mmlock,omitempty"`
	Options        []string    `json:"options,omitempty" yaml:"options,omitempty"`
}

func (s *Settings) params() *Parameters {
	if s.Parameters == nil {
		s.Parameters = &Parameters{}
	}
	return s.Parameters
}

// clone copies the struct and the Parameters block, so a function that fills
// in blanks does not touch the caller's value.
func (s Settings) clone() Settings {
	if s.Parameters != nil {
		p := *s.Parameters
		s.Parameters = &p
	}
	s.Options = append([]string(nil), s.Options...)
	return s
}
