package enginesettings

import (
	"fmt"
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

// ParseResult is the outcome of ParseCommand.
type ParseResult struct {
	// Fields holds the model config keys the command maps to. It never has
	// an "options" key: see Options.
	Fields Config `json:"fields"`
	// Options is the "options" list: known entries in table order, then raw
	// passthrough entries in the order they appeared.
	Options []string `json:"options"`
	// Unmapped lists flags that were not turned into a first-class key.
	// Unknown flags are in Options as raw entries too, so nothing is lost.
	Unmapped []Unmapped `json:"unmapped"`
	Warnings []string   `json:"warnings"`
	// Placements describes every placement entry of Options. Only for
	// engines with the tensor-placement capability.
	Placements []PlacementInfo `json:"placements,omitempty"`
}

// ParseState is what a SpecialFlag parser can change.
type ParseState struct {
	cfg   Config
	known map[string]string // flag key -> rendered option entry
	res   *ParseResult
}

// Set stores a value at a config path.
func (p *ParseState) Set(path string, v any) { p.cfg.set(path, v) }

// Unset removes the value at a config path.
func (p *ParseState) Unset(path string) { p.cfg.unset(path) }

// SetOption stores the options entry of a table flag.
func (p *ParseState) SetOption(key, entry string) { p.known[key] = entry }

// UnsetOption removes the options entry of a table flag.
func (p *ParseState) UnsetOption(key string) { delete(p.known, key) }

// Warn adds a warning.
func (p *ParseState) Warn(format string, a ...any) {
	p.res.Warnings = append(p.res.Warnings, fmt.Sprintf(format, a...))
}

// Unmap reports a flag that was not turned into a setting.
func (p *ParseState) Unmap(flag, value, reason string) {
	p.res.Unmapped = append(p.res.Unmapped, Unmapped{Flag: flag, Value: value, Reason: reason})
}

func looksLikeValue(tok string) bool {
	if !strings.HasPrefix(tok, "-") || tok == "-" {
		return true
	}
	_, err := strconv.ParseFloat(tok, 64)
	return err == nil
}

type cliIndex struct {
	flags   map[string]*Flag
	negated map[string]*Flag
	ignored map[string]*IgnoredFlag
	special map[string]*SpecialFlag
}

func (e *Engine) index() cliIndex {
	idx := cliIndex{map[string]*Flag{}, map[string]*Flag{}, map[string]*IgnoredFlag{}, map[string]*SpecialFlag{}}
	for i := range e.Flags {
		for _, c := range e.Flags[i].CLI {
			idx.flags[c] = &e.Flags[i]
		}
		for _, c := range e.Flags[i].CLINegated {
			idx.negated[c] = &e.Flags[i]
		}
	}
	for i := range e.Command.Ignored {
		for _, c := range e.Command.Ignored[i].CLI {
			idx.ignored[c] = &e.Command.Ignored[i]
		}
	}
	for i := range e.Command.Special {
		for _, c := range e.Command.Special[i].CLI {
			idx.special[c] = &e.Command.Special[i]
		}
	}
	return idx
}

// ParseCommand reads a command line of the engine. It only tokenises the
// text; it never starts a shell or a process.
func (e *Engine) ParseCommand(cmd string) (ParseResult, error) {
	res := ParseResult{Fields: Config{}, Options: []string{}, Unmapped: []Unmapped{}, Warnings: []string{}}
	if e.Command == nil {
		return res, ErrUnsupported
	}
	if e.Placement != nil {
		res.Placements = []PlacementInfo{}
	}
	if len(cmd) > MaxCommandLength {
		return res, fmt.Errorf("the command is longer than %d bytes", MaxCommandLength)
	}
	toks, ops, err := e.Command.tokenize(cmd)
	if err != nil {
		return res, err
	}
	p := &ParseState{cfg: res.Fields, known: map[string]string{}, res: &res}
	for _, o := range ops {
		p.Warn("%q contains shell operators; they are kept as plain text and nothing is run", o)
	}

	idx := e.index()
	var raw []string
	addRaw := func(flag string, val *string) string {
		o, v := flag, ""
		if val != nil {
			o, v = flag+":"+*val, *val
		}
		raw = append(raw, o)
		return v
	}

	for i := 0; i < len(toks); i++ {
		tok := toks[i]
		if i == 0 && !strings.HasPrefix(tok, "-") && e.Command.isBinary(tok) {
			continue
		}
		if !strings.HasPrefix(tok, "-") || tok == "-" {
			p.Warn("ignored the argument %q: it does not follow a flag that takes a value", tok)
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

		if ig, ok := idx.ignored[name]; ok {
			val := ""
			if !ig.NoValue {
				val, _ = next()
			}
			p.Unmap(name, val, "ignored: "+ig.Reason)
			continue
		}
		if sp, ok := idx.special[name]; ok {
			v, has := next()
			sp.Parse(p, name, v, has)
			continue
		}
		if f, ok := idx.flags[name]; ok {
			e.applyKnown(p, f, name, toks, &i, hasInline, inline, next, &raw)
			continue
		}
		if f, ok := idx.negated[name]; ok {
			if hasInline {
				p.Unmap(name, inline, "this flag takes no value")
				continue
			}
			p.store(f, "false")
			continue
		}

		// Unknown flag: keep it as a raw passthrough option.
		var val *string
		if v, ok := next(); ok {
			val = &v
			if !hasInline {
				p.Warn("%s is not a known flag; %q was read as its value", name, v)
			}
		}
		v := addRaw(name, val)
		p.Unmap(name, v, "no first-class setting; kept as a raw passthrough option")
	}

	for i := range e.Flags {
		if o, ok := p.known[e.Flags[i].Key]; ok {
			res.Options = append(res.Options, o)
		}
	}
	res.Options = append(res.Options, raw...)
	if pl := e.Placement; pl != nil {
		for _, o := range res.Options {
			if name, _, _ := strings.Cut(o, ":"); name == pl.Option {
				info := PlacementInfo{Option: o}
				if pp, ok := pl.Recognise(o); ok {
					info.Placement = &pp
				}
				res.Placements = append(res.Placements, info)
			}
		}
	}
	return res, nil
}

// store writes a normalised value of a table flag.
func (p *ParseState) store(f *Flag, v string) {
	if f.Target != TargetOption {
		p.cfg.set(f.Path, typedValue(f, v))
		return
	}
	if f.Type == TypeBool && f.BareOption && v == "true" {
		p.known[f.Key] = f.Option
		return
	}
	p.known[f.Key] = f.Option + ":" + v
}

func (e *Engine) applyKnown(p *ParseState, f *Flag, name string, toks []string, i *int,
	hasInline bool, inline string, next func() (string, bool), raw *[]string) {
	switch {
	case f.Type == TypeBool:
		v := "true"
		if hasInline {
			n, err := NormalizeValue(f, inline)
			if err != nil {
				p.Unmap(name, inline, err.Error())
				return
			}
			v = n
		}
		p.store(f, v)
	case f.OptionalValue:
		v := f.BareValue
		if hasInline {
			v = inline
		} else if *i+1 < len(toks) {
			if _, err := NormalizeValue(f, toks[*i+1]); err == nil {
				*i++
				v = toks[*i]
			}
		}
		n, err := NormalizeValue(f, v)
		if err != nil {
			p.Unmap(name, v, err.Error())
			return
		}
		p.store(f, n)
	case f.Raw:
		v, ok := next()
		if !ok || v == "" {
			p.Unmap(name, "", "missing value")
			return
		}
		*raw = append(*raw, f.Option+":"+v)
	default:
		v, ok := next()
		if !ok {
			p.Unmap(name, "", "missing value")
			return
		}
		n, err := NormalizeValue(f, v)
		if err != nil {
			p.Unmap(name, v, err.Error())
			return
		}
		p.store(f, n)
	}
}
