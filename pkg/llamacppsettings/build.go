package llamacppsettings

import (
	"fmt"
	"strings"
)

// BuildResult is the outcome of BuildCommand.
type BuildResult struct {
	Command  string   `json:"command"`
	Warnings []string `json:"warnings"`
}

// BuildCommand writes the llama-server command line that matches a model
// config subset. It is the inverse of ParseCommand and is meant for a live
// preview: the string is never run.
//
// The LocalAI-only options (context_shift and the like) have no flag and are
// listed in Warnings.
func BuildCommand(in Settings) BuildResult {
	res := BuildResult{Warnings: []string{}}
	s := in.clone()
	words := []string{"llama-server"}
	add := func(w ...string) {
		for _, x := range w {
			words = append(words, quote(x))
		}
	}

	if s.Parameters != nil && s.Parameters.Model != "" {
		add(modelCLI[0], s.Parameters.Model)
	}

	// Sort the options into known ones and raw ones.
	known := map[string]string{}
	var raw []string
	for _, o := range s.Options {
		name, val, hasVal := strings.Cut(o, ":")
		if strings.HasPrefix(name, "-") {
			raw = append(raw, o)
			continue
		}
		f := optionFlag(name)
		if f == nil {
			res.Warnings = append(res.Warnings, fmt.Sprintf("the option %q has no llama-server flag and is not in the command", o))
			continue
		}
		if !hasVal {
			val = "true"
		}
		n, err := normalizeValue(f, val)
		if err != nil {
			res.Warnings = append(res.Warnings, fmt.Sprintf("the option %q is not valid: %v", o, err))
			continue
		}
		known[f.Key] = n
	}

	loadModeDone := false
	loadMode := func() {
		if loadModeDone {
			return
		}
		loadModeDone = true
		_, dio := known["direct_io"]
		dio = dio && known["direct_io"] == "true"
		if s.MMap == nil && s.MMlock == nil && !dio {
			return
		}
		mm := s.MMap != nil && *s.MMap
		ml := s.MMlock != nil && *s.MMlock
		mode := "none"
		switch {
		case dio:
			mode = "direct_io"
		case mm && ml:
			mode = "mmap_mlock"
		case ml:
			mode = "mlock"
		case mm:
			mode = "mmap"
		}
		add(loadModeCLI, mode)
	}

	for i := range Flags {
		f := &Flags[i]
		switch f.Key {
		case "mmap", "mmlock", "direct_io":
			loadMode()
			continue
		case "override_tensor":
			continue
		}
		var v string
		var ok bool
		if f.Target == TargetOption {
			v, ok = known[f.Key]
		} else {
			v, ok = getValue(f, &s)
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
		if name == "--override-tensor" {
			name = "-ot"
		}
		if hasVal {
			add(name, val)
		} else {
			add(name)
		}
	}
	res.Command = strings.Join(words, " ")
	return res
}
