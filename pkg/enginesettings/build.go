package enginesettings

import (
	"fmt"
	"strings"
)

// BuildResult is the outcome of BuildCommand.
type BuildResult struct {
	Command  string   `json:"command"`
	Warnings []string `json:"warnings"`
}

// BuildState is what a SpecialFlag builder can read.
type BuildState struct {
	cfg   Config
	known map[string]string // flag key -> normalised option value
}

// Value returns the value at a config path, or false when it is unset.
func (b *BuildState) Value(path string) (any, bool) {
	v, ok, _ := b.cfg.lookup(path)
	return v, ok
}

// Bool returns a bool at a config path, or nil when it is unset.
func (b *BuildState) Bool(path string) *bool {
	v, ok := b.Value(path)
	if !ok {
		return nil
	}
	x, isBool := v.(bool)
	if !isBool {
		return nil
	}
	return &x
}

// String returns a string at a config path, or "".
func (b *BuildState) String(path string) string {
	v, _ := b.Value(path)
	s, _ := v.(string)
	return s
}

// Option returns the normalised value of the options entry of a table flag.
func (b *BuildState) Option(key string) (string, bool) {
	v, ok := b.known[key]
	return v, ok
}

// checkConfig refuses a config whose known keys hold values of the wrong type.
func (e *Engine) checkConfig(cfg Config) error {
	for i := range e.Flags {
		f := &e.Flags[i]
		if f.Target != TargetConfig {
			continue
		}
		v, _, err := cfg.lookup(f.Path)
		if err != nil {
			return err
		}
		if _, _, err := textValue(f, v); err != nil {
			return err
		}
	}
	if e.Command != nil {
		for _, sp := range e.Command.Special {
			for path, t := range sp.Paths {
				v, _, err := cfg.lookup(path)
				if err != nil {
					return err
				}
				if _, _, err := textValue(&Flag{Path: path, Type: t}, v); err != nil {
					return err
				}
			}
		}
	}
	_, err := cfg.options()
	return err
}

// BuildCommand writes the command line that matches a partial model config.
// It is the inverse of ParseCommand and is meant for a live preview: the
// string is never run. Options that have no flag are listed in Warnings. A
// config whose known keys hold values of the wrong type is refused.
func (e *Engine) BuildCommand(cfg Config) (BuildResult, error) {
	res := BuildResult{Warnings: []string{}}
	cl := e.Command
	if cl == nil {
		return res, ErrUnsupported
	}
	if cfg == nil {
		cfg = Config{}
	}
	if err := e.checkConfig(cfg); err != nil {
		return res, err
	}
	words := []string{cl.Binary}
	add := func(w ...string) {
		for _, x := range w {
			words = append(words, Quote(x))
		}
	}

	// Sort the options into known ones and raw ones.
	opts, _ := cfg.options()
	known := map[string]string{}
	var raw []string
	for _, o := range opts {
		name, val, hasVal := strings.Cut(o, ":")
		if strings.HasPrefix(name, "-") {
			raw = append(raw, o)
			continue
		}
		f := e.optionFlag(name)
		if f == nil {
			res.Warnings = append(res.Warnings, fmt.Sprintf("the option %q has no %s flag and is not in the command", o, cl.Binary))
			continue
		}
		if !hasVal {
			val = "true"
		}
		n, err := NormalizeValue(f, val)
		if err != nil {
			res.Warnings = append(res.Warnings, fmt.Sprintf("the option %q is not valid: %v", o, err))
			continue
		}
		known[f.Key] = n
	}
	b := &BuildState{cfg: cfg, known: known}

	covered := map[string]*SpecialFlag{}
	for i := range cl.Special {
		sp := &cl.Special[i]
		if sp.Build == nil {
			continue
		}
		if len(sp.Covers) == 0 {
			add(sp.Build(b)...)
		}
		for _, k := range sp.Covers {
			covered[k] = sp
		}
	}
	done := map[*SpecialFlag]bool{}
	for i := range e.Flags {
		f := &e.Flags[i]
		if sp, ok := covered[f.Key]; ok {
			if !done[sp] {
				done[sp] = true
				add(sp.Build(b)...)
			}
			continue
		}
		if f.Raw {
			continue
		}
		var v string
		var ok bool
		if f.Target == TargetOption {
			v, ok = known[f.Key]
		} else {
			cv, _, _ := cfg.lookup(f.Path)
			v, ok, _ = textValue(f, cv)
		}
		if !ok {
			continue
		}
		switch {
		case f.Type == TypeBool && v == "true":
			add(f.CLI[0])
		case f.Type == TypeBool:
			if len(f.CLINegated) > 0 {
				add(f.CLINegated[0])
			}
		default:
			add(f.CLI[0], v)
		}
	}
	for _, o := range raw {
		name, val, hasVal := strings.Cut(o, ":")
		// A raw entry of a table flag is written with its first spelling.
		for i := range e.Flags {
			f := &e.Flags[i]
			if f.Raw && contains(f.CLI, name) {
				name = f.CLI[0]
			}
		}
		if hasVal {
			add(name, val)
		} else {
			add(name)
		}
	}
	res.Command = strings.Join(words, " ")
	return res, nil
}

func contains(l []string, s string) bool {
	for _, x := range l {
		if x == s {
			return true
		}
	}
	return false
}
