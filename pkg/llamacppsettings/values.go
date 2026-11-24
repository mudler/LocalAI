package llamacppsettings

import (
	"fmt"
	"math"
	"strconv"
	"strings"
)

// normalizeValue checks a value against the type, enum and range of a flag
// and returns its canonical text form.
func normalizeValue(f *Flag, v string) (string, error) {
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

// setValue stores a normalised value in a field or parameter flag.
func setValue(f *Flag, s *Settings, v string) {
	switch {
	case f.intPtr != nil:
		n, _ := strconv.Atoi(v)
		*f.intPtr(s) = &n
	case f.floatPtr != nil:
		n, _ := strconv.ParseFloat(v, 64)
		*f.floatPtr(s) = &n
	case f.boolPtr != nil:
		b := v == "true"
		*f.boolPtr(s) = &b
	case f.strPP != nil:
		*f.strPP(s) = &v
	case f.strPtr != nil:
		*f.strPtr(s) = v
	}
}

// getValue reads a field or parameter flag as text.
func getValue(f *Flag, s *Settings) (string, bool) {
	switch {
	case f.intPtr != nil:
		if p := *f.intPtr(s); p != nil {
			return strconv.Itoa(*p), true
		}
	case f.floatPtr != nil:
		if p := *f.floatPtr(s); p != nil {
			return formatFloat(*p), true
		}
	case f.boolPtr != nil:
		if p := *f.boolPtr(s); p != nil {
			return strconv.FormatBool(*p), true
		}
	case f.strPP != nil:
		if p := *f.strPP(s); p != nil && *p != "" {
			return *p, true
		}
	case f.strPtr != nil:
		if p := *f.strPtr(s); p != "" {
			return p, true
		}
	}
	return "", false
}

// formatFloat writes whole numbers with one decimal ("1.0"), like the flags
// in llama-server examples.
func formatFloat(n float64) string {
	t := strconv.FormatFloat(n, 'f', -1, 64)
	if !strings.ContainsAny(t, ".eE") {
		t += ".0"
	}
	return t
}
