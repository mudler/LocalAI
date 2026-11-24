package enginesettings

import (
	"encoding/json"
	"fmt"
	"math"
	"strconv"
	"strings"
)

// NormalizeValue checks a value against the type, enum and range of a flag
// and returns its canonical text form.
func NormalizeValue(f *Flag, v string) (string, error) {
	switch f.Type {
	case TypeInt:
		n, err := strconv.Atoi(v)
		if err != nil {
			return "", fmt.Errorf("%q is not an integer", v)
		}
		if err := checkRange(f, float64(n)); err != nil {
			return "", err
		}
		return strconv.Itoa(n), nil
	case TypeFloat:
		n, err := strconv.ParseFloat(v, 64)
		if err != nil || math.IsNaN(n) || math.IsInf(n, 0) {
			return "", fmt.Errorf("%q is not a number", v)
		}
		if err := checkRange(f, n); err != nil {
			return "", err
		}
		return strconv.FormatFloat(n, 'f', -1, 64), nil
	case TypeBool:
		switch strings.ToLower(v) {
		case "true", "1", "yes", "on", "enabled":
			return "true", nil
		case "false", "0", "no", "off", "disabled":
			return "false", nil
		}
		return "", fmt.Errorf("%q is not a boolean", v)
	case TypeEnum:
		lv := strings.ToLower(v)
		for _, e := range f.Enum {
			if e == lv {
				return e, nil
			}
		}
		return "", fmt.Errorf("%q is not one of %s", v, strings.Join(f.Enum, ", "))
	}
	if v == "" {
		return "", fmt.Errorf("empty value")
	}
	return v, nil
}

func checkRange(f *Flag, n float64) error {
	if f.Min != nil && n < *f.Min {
		return fmt.Errorf("%v is below the minimum %v", n, *f.Min)
	}
	if f.Max != nil && n > *f.Max {
		return fmt.Errorf("%v is above the maximum %v", n, *f.Max)
	}
	return nil
}

// typedValue turns a normalised text value into the Go value stored in a
// Config: int, float64, bool or string.
func typedValue(f *Flag, v string) any {
	switch f.Type {
	case TypeInt:
		n, _ := strconv.Atoi(v)
		return n
	case TypeFloat:
		n, _ := strconv.ParseFloat(v, 64)
		return n
	case TypeBool:
		return v == "true"
	}
	return v
}

// textValue writes a config value of a flag as command-line text. It accepts
// the Go types a Config holds after a JSON or YAML decode. ok is false for an
// unset value; err is set when the value has the wrong type.
func textValue(f *Flag, v any) (text string, ok bool, err error) {
	if v == nil {
		return "", false, nil
	}
	switch f.Type {
	case TypeInt:
		n, isNum := number(v)
		if !isNum || n != math.Trunc(n) || math.IsInf(n, 0) {
			return "", false, fmt.Errorf("%s: %v is not an integer", f.Path, v)
		}
		return strconv.FormatInt(int64(n), 10), true, nil
	case TypeFloat:
		n, isNum := number(v)
		if !isNum {
			return "", false, fmt.Errorf("%s: %v is not a number", f.Path, v)
		}
		return formatFloat(n), true, nil
	case TypeBool:
		b, isBool := v.(bool)
		if !isBool {
			return "", false, fmt.Errorf("%s: %v is not a boolean", f.Path, v)
		}
		return strconv.FormatBool(b), true, nil
	}
	s, isStr := v.(string)
	if !isStr {
		return "", false, fmt.Errorf("%s: %v is not a string", f.Path, v)
	}
	return s, s != "", nil
}

func number(v any) (float64, bool) {
	switch n := v.(type) {
	case int:
		return float64(n), true
	case int64:
		return float64(n), true
	case float64:
		return n, true
	case json.Number:
		f, err := n.Float64()
		return f, err == nil
	}
	return 0, false
}

// formatFloat writes whole numbers with one decimal ("1.0"), like the flags
// in command-line examples.
func formatFloat(n float64) string {
	t := strconv.FormatFloat(n, 'f', -1, 64)
	if !strings.ContainsAny(t, ".eE") {
		t += ".0"
	}
	return t
}
