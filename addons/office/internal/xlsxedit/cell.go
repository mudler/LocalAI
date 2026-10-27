package xlsxedit

import (
	"errors"
	"fmt"
	"math"
	"strconv"
	"strings"
)

// Limits of the format.
const (
	maxSheetName = 31
	maxRow       = 1048576
	maxCol       = 16384
)

type kind int

const (
	kindNone kind = iota
	kindString
	kindNumber
	kindBool
	kindFormula
)

// Value is the content to write in a cell. Build one with Str, Num, Boolean,
// Formula or FormulaCached. The zero Value is not valid.
type Value struct {
	kind    kind
	str     string
	num     float64
	b       bool
	formula string
	cached  *Value
}

// Str returns a text value.
func Str(s string) Value { return Value{kind: kindString, str: s} }

// Num returns a number value.
func Num(n float64) Value { return Value{kind: kindNumber, num: n} }

// Boolean returns a boolean value.
func Boolean(b bool) Value { return Value{kind: kindBool, b: b} }

// Formula returns a formula value with no cached result. A leading "=" is
// dropped. SetCell rejects an empty formula.
func Formula(expr string) Value {
	return Value{kind: kindFormula, formula: strings.TrimPrefix(strings.TrimSpace(expr), "=")}
}

// FormulaCached returns a formula value with a cached result. The cached
// value must be a text, number or boolean value.
func FormulaCached(expr string, cached Value) Value {
	v := Formula(expr)
	v.cached = &cached
	return v
}

func fmtFloat(f float64) string { return strconv.FormatFloat(f, 'g', -1, 64) }

func boolStr(b bool) string {
	if b {
		return "1"
	}
	return "0"
}

func validateScalar(v Value) error {
	switch v.kind {
	case kindString, kindBool:
	case kindNumber:
		if math.IsNaN(v.num) || math.IsInf(v.num, 0) {
			return errors.New("number is not finite")
		}
	default:
		return errors.New("value is empty or has no valid kind")
	}
	if v.kind == kindString {
		for _, r := range v.str {
			if r < 0x20 && r != '\t' && r != '\n' && r != '\r' {
				return fmt.Errorf("text contains the control character U+%04X", r)
			}
		}
	}
	return nil
}

func validateValue(v Value) error {
	if v.kind != kindFormula {
		return validateScalar(v)
	}
	if strings.TrimSpace(v.formula) == "" {
		return errors.New("formula is empty")
	}
	for _, r := range v.formula {
		if r < 0x20 && r != '\t' && r != '\n' && r != '\r' {
			return fmt.Errorf("formula contains the control character U+%04X", r)
		}
	}
	if v.cached != nil {
		if v.cached.kind == kindFormula {
			return errors.New("cached value of a formula cannot be a formula")
		}
		if err := validateScalar(*v.cached); err != nil {
			return fmt.Errorf("cached value: %w", err)
		}
	}
	return nil
}

func valueXML(prefix, ref, style string, v Value) string {
	var b strings.Builder
	b.WriteString("<" + prefix + `c r="` + ref + `"`)
	if style != "" {
		b.WriteString(` s="` + esc(style) + `"`)
	}
	open := func(typ string) {
		if typ != "" {
			b.WriteString(` t="` + typ + `"`)
		}
		b.WriteString(">")
	}
	el := func(name, body string) string {
		return "<" + prefix + name + ">" + body + "</" + prefix + name + ">"
	}
	switch v.kind {
	case kindString:
		open("inlineStr")
		b.WriteString("<" + prefix + `is><` + prefix + `t xml:space="preserve">` + esc(v.str) + "</" + prefix + "t></" + prefix + "is>")
	case kindNumber:
		open("")
		b.WriteString(el("v", fmtFloat(v.num)))
	case kindBool:
		open("b")
		b.WriteString(el("v", boolStr(v.b)))
	case kindFormula:
		typ := ""
		if v.cached != nil {
			switch v.cached.kind {
			case kindString:
				typ = "str"
			case kindBool:
				typ = "b"
			}
		}
		open(typ)
		b.WriteString(el("f", esc(v.formula)))
		if c := v.cached; c != nil {
			switch c.kind {
			case kindString:
				b.WriteString(el("v", esc(c.str)))
			case kindNumber:
				b.WriteString(el("v", fmtFloat(c.num)))
			case kindBool:
				b.WriteString(el("v", boolStr(c.b)))
			}
		}
	}
	b.WriteString("</" + prefix + "c>")
	return b.String()
}

// parseRef splits an upper case A1 reference into a 1-based column and row.
func parseRef(ref string) (col, row int, err error) {
	i := 0
	for i < len(ref) && ref[i] >= 'A' && ref[i] <= 'Z' {
		col = col*26 + int(ref[i]-'A') + 1
		i++
		if col > maxCol {
			return 0, 0, fmt.Errorf("cell ref %q: column is beyond XFD", ref)
		}
	}
	if i == 0 || i == len(ref) || ref[i] == '0' {
		return 0, 0, fmt.Errorf("cell ref %q is not valid, use upper case letters then digits such as B3", ref)
	}
	for ; i < len(ref); i++ {
		if ref[i] < '0' || ref[i] > '9' {
			return 0, 0, fmt.Errorf("cell ref %q is not valid, use upper case letters then digits such as B3", ref)
		}
		row = row*10 + int(ref[i]-'0')
		if row > maxRow {
			return 0, 0, fmt.Errorf("cell ref %q: row is beyond %d", ref, maxRow)
		}
	}
	return col, row, nil
}

func parseRowNumber(s string) (int, error) {
	n, err := strconv.Atoi(s)
	if err != nil || n < 1 || n > maxRow {
		return 0, fmt.Errorf("row number %q is not valid", s)
	}
	return n, nil
}

func cellName(col, row int) string {
	var s []byte
	for ; col > 0; col = (col - 1) / 26 {
		s = append([]byte{byte('A' + (col-1)%26)}, s...)
	}
	return string(s) + strconv.Itoa(row)
}

func validateSheetName(name string) error {
	if name == "" {
		return errors.New("sheet name is empty")
	}
	if n := len([]rune(name)); n > maxSheetName {
		return fmt.Errorf("sheet name %q is longer than %d characters", name, maxSheetName)
	}
	if strings.ContainsAny(name, `[]:*?/\`) {
		return fmt.Errorf("sheet name %q contains one of []:*?/\\", name)
	}
	if strings.HasPrefix(name, "'") || strings.HasSuffix(name, "'") {
		return fmt.Errorf("sheet name %q starts or ends with an apostrophe", name)
	}
	for _, r := range name {
		if r < 0x20 {
			return fmt.Errorf("sheet name %q contains a control character", name)
		}
	}
	return nil
}
