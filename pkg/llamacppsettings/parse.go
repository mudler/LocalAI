package llamacppsettings

import (
	"fmt"
	"path"
	"strconv"
	"strings"
)

// MaxCommandLength is the longest command line ParseCommand accepts.
const MaxCommandLength = 16 * 1024

// Unmapped is a flag that has no first-class model config key.
type Unmapped struct {
	Flag   string `json:"flag"`
	Value  string `json:"value"`
	Reason string `json:"reason"`
}

// PlacementInfo links a raw "-ot" option to its structured form. Placement is
// nil when the pattern is not one this package generates.
type PlacementInfo struct {
	Option    string     `json:"option"`
	Placement *Placement `json:"placement"`
}

// ParseResult is the outcome of ParseCommand.
type ParseResult struct {
	// Fields holds the model config keys the command maps to. Its Options
	// list is always empty: see Options.
	Fields Settings `json:"fields"`
	// Options is the "options" list: known entries in table order, then raw
	// passthrough entries in the order they appeared.
	Options []string `json:"options"`
	// Unmapped lists flags that were not turned into a first-class key.
	// Unknown flags are in Options as raw entries too, so nothing is lost.
	Unmapped []Unmapped `json:"unmapped"`
	Warnings []string   `json:"warnings"`
	// Placements describes every "-ot" entry of Options.
	Placements []PlacementInfo `json:"placements"`
}

func looksLikeValue(tok string) bool {
	if !strings.HasPrefix(tok, "-") || tok == "-" {
		return true
	}
	_, err := strconv.ParseFloat(tok, 64)
	return err == nil
}

// ParseCommand reads a "llama-server ..." command line. It only tokenises the
// text; it never starts a shell or a process.
func ParseCommand(cmd string) (ParseResult, error) {
	res := ParseResult{Options: []string{}, Unmapped: []Unmapped{}, Warnings: []string{}, Placements: []PlacementInfo{}}
	if len(cmd) > MaxCommandLength {
		return res, fmt.Errorf("the command is longer than %d bytes", MaxCommandLength)
	}
	toks, ops, err := Tokenize(cmd)
	if err != nil {
		return res, err
	}
	for _, o := range ops {
		res.Warnings = append(res.Warnings, fmt.Sprintf("%q contains shell operators; they are kept as plain text and nothing is run", o))
	}

	cli, neg := flagIndex(), negatedIndex()
	var s Settings
	known := map[string]string{} // option key -> rendered option
	var raw []string

	unmap := func(flag, value, reason string) {
		res.Unmapped = append(res.Unmapped, Unmapped{Flag: flag, Value: value, Reason: reason})
	}
	addRaw := func(flag string, val *string) string {
		o := flag
		v := ""
		if val != nil {
			o = flag + ":" + *val
			v = *val
		}
		raw = append(raw, o)
		return v
	}
	setOption := func(f *Flag, v string) {
		switch {
		case f.Type == TypeBool && f.bareOption && v == "true":
			known[f.Key] = f.Option
		default:
			known[f.Key] = f.Option + ":" + v
		}
	}

	for i := 0; i < len(toks); i++ {
		tok := toks[i]
		if i == 0 && !strings.HasPrefix(tok, "-") {
			base := path.Base(strings.ReplaceAll(tok, `\`, "/"))
			if strings.Contains(base, "llama") || strings.Contains(base, "server") {
				continue
			}
		}
		if !strings.HasPrefix(tok, "-") || tok == "-" {
			res.Warnings = append(res.Warnings, fmt.Sprintf("ignored the argument %q: it does not follow a flag that takes a value", tok))
			continue
		}
		if tok == "--" {
			break
		}
		name, inline, hasInline := tok, "", false
		if strings.HasPrefix(tok, "--") {
			if k := strings.IndexByte(tok, '='); k > 0 {
				name, inline, hasInline = tok[:k], tok[k+1:], true
			}
		}

		// next returns the value of a flag that needs one.
		next := func() (string, bool) {
			if hasInline {
				return inline, true
			}
			if i+1 < len(toks) && looksLikeValue(toks[i+1]) {
				i++
				return toks[i], true
			}
			return "", false
		}

		if reason, ok := ignoredFlags[name]; ok {
			val := ""
			if name != "-h" && name != "--help" && name != "--usage" && name != "--version" && name != "--license" &&
				name != "--list-devices" && name != "-cl" && name != "--cache-list" {
				val, _ = next()
			}
			unmap(name, val, "ignored: "+reason)
			continue
		}

		switch {
		case name == modelCLI[0] || name == modelCLI[1]:
			v, ok := next()
			if !ok || v == "" {
				unmap(name, "", "missing value")
				continue
			}
			s.params().Model = v
			if strings.ContainsAny(v, `/\`) {
				res.Warnings = append(res.Warnings, fmt.Sprintf("the model %q is a path; LocalAI expects a file name inside the models directory", v))
			}
			continue

		case name == loadModeCLI:
			v, ok := next()
			if !ok {
				unmap(name, "", "missing value")
				continue
			}
			applyLoadMode(&s, known, strings.ToLower(v), &res, unmap, name)
			continue
		}

		if f, ok := cli[name]; ok {
			applyKnown(f, name, &s, known, &raw, &res, unmap, next, toks, &i, hasInline, inline, setOption)
			continue
		}
		if f, ok := neg[name]; ok {
			if hasInline {
				unmap(name, inline, "this flag takes no value")
				continue
			}
			if f.Target == TargetOption {
				setOption(f, "false")
			} else {
				b := false
				*f.boolPtr(&s) = &b
			}
			continue
		}

		// Unknown flag: keep it as a raw passthrough option.
		var val *string
		if v, ok := next(); ok {
			val = &v
			if !hasInline {
				res.Warnings = append(res.Warnings, fmt.Sprintf("%s is not a known flag; %q was read as its value", name, v))
			}
		}
		v := addRaw(name, val)
		unmap(name, v, "no first-class setting; kept as a raw passthrough option")
	}

	for i := range Flags {
		f := &Flags[i]
		if o, ok := known[f.Key]; ok {
			res.Options = append(res.Options, o)
		}
	}
	res.Options = append(res.Options, raw...)
	for _, o := range res.Options {
		if name, _, _ := strings.Cut(o, ":"); name == "-ot" {
			info := PlacementInfo{Option: o}
			if p, ok := ParseOption(o); ok {
				info.Placement = &p
			}
			res.Placements = append(res.Placements, info)
		}
	}
	res.Fields = s
	return res, nil
}

func applyKnown(f *Flag, name string, s *Settings, known map[string]string, raw *[]string, res *ParseResult,
	unmap func(string, string, string), next func() (string, bool), toks []string, i *int,
	hasInline bool, inline string, setOption func(*Flag, string)) {

	store := func(v string) {
		if f.Target == TargetOption {
			setOption(f, v)
		} else {
			setValue(f, s, v)
		}
	}

	switch {
	case f.Type == TypeBool:
		v := "true"
		if hasInline {
			n, err := normalizeValue(f, inline)
			if err != nil {
				unmap(name, inline, err.Error())
				return
			}
			v = n
		}
		store(v)
	case f.optionalValue:
		v := f.bareValue
		if hasInline {
			v = inline
		} else if *i+1 < len(toks) {
			if _, err := normalizeValue(f, toks[*i+1]); err == nil {
				*i++
				v = toks[*i]
			}
		}
		n, err := normalizeValue(f, v)
		if err != nil {
			unmap(name, v, err.Error())
			return
		}
		store(n)
	case f.raw:
		v, ok := next()
		if !ok || v == "" {
			unmap(name, "", "missing value")
			return
		}
		*raw = append(*raw, f.Option+":"+v)
	default:
		v, ok := next()
		if !ok {
			unmap(name, "", "missing value")
			return
		}
		n, err := normalizeValue(f, v)
		if err != nil {
			unmap(name, v, err.Error())
			return
		}
		store(n)
	}
}

// applyLoadMode maps --load-mode onto mmap, mlock and direct_io. It sets all
// three, so the last of several flags wins.
func applyLoadMode(s *Settings, known map[string]string, mode string, res *ParseResult,
	unmap func(string, string, string), flag string) {
	mm, ml := new(bool), new(bool)
	dio := false
	switch strings.ReplaceAll(mode, "-", "_") {
	case "none":
	case "mmap":
		*mm = true
	case "mlock":
		*ml = true
	case "mmap_mlock", "mmap+mlock":
		*mm, *ml = true, true
	case "direct_io":
		dio = true
		mm, ml = nil, nil
	case "auto":
		res.Warnings = append(res.Warnings, "--load-mode auto is not a LocalAI setting; mmap and mlock stay unset so the LocalAI default applies")
		mm, ml = nil, nil
	default:
		unmap(flag, mode, "unknown load mode; expected one of "+strings.Join(loadModes, ", "))
		return
	}
	s.MMap, s.MMlock = mm, nil
	if ml != nil && *ml {
		s.MMlock = ml
	}
	if dio {
		known["direct_io"] = "direct_io:true"
	} else {
		delete(known, "direct_io")
	}
}
