package modelsearch

import (
	"encoding/json"
	"fmt"
	"math"
	"slices"
	"sort"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/mudler/LocalAI/pkg/addons/contract"
)

// Source describes one source of models for the panel and for the assistant:
// who it is and which controls it offers. The host draws them; nothing here
// is markup.
type Source struct {
	ID    string `json:"id"`
	Title string `json:"title"`
	Icon  string `json:"icon,omitempty"`
	// Kind is "builtin" or "addon".
	Kind        string                              `json:"kind"`
	ListsLatest bool                                `json:"lists_latest,omitempty"`
	HasVariants bool                                `json:"has_variants,omitempty"`
	P2P         bool                                `json:"p2p,omitempty"`
	Sorts       []string                            `json:"sorts,omitempty"`
	Filters     []contract.SearchFilter             `json:"filters,omitempty"`
	I18n        map[string]contract.ModelSearchText `json:"i18n,omitempty"`
	// Running is false for an add-on that is installed and not running.
	Running bool `json:"running"`
}

// Describer is implemented by a provider that can describe its source.
type Describer interface {
	Source() Source
}

// FilterError is a filter value the source does not accept. The route turns
// it into a 400 that names the filter.
type FilterError struct {
	Source, Filter, Reason string
}

func (e *FilterError) Error() string {
	return fmt.Sprintf("filter %q of source %q: %s", e.Filter, e.Source, e.Reason)
}

// ValidateFilters checks the values a request gives for one source against its
// declared filters and returns them in canonical form (trimmed, bool as true or
// false, a range as the shortest number). An unknown filter, or a value that
// does not fit the type, is a *FilterError.
func ValidateFilters(src Source, values map[string]string) (map[string]string, error) {
	out := make(map[string]string, len(values))
	for id, raw := range values {
		i := slices.IndexFunc(src.Filters, func(f contract.SearchFilter) bool { return f.ID == id })
		if i < 0 {
			return nil, &FilterError{src.ID, id, "unknown filter"}
		}
		f := src.Filters[i]
		v := strings.TrimSpace(raw)
		switch f.Type {
		case contract.FilterEnum:
			if !slices.Contains(f.Values, v) {
				return nil, &FilterError{src.ID, id, "the value must be one of " + strings.Join(f.Values, ", ")}
			}
		case contract.FilterBool:
			b, err := strconv.ParseBool(v)
			if err != nil || (v != "true" && v != "false" && v != "1" && v != "0") {
				return nil, &FilterError{src.ID, id, "the value must be true or false"}
			}
			v = strconv.FormatBool(b)
		case contract.FilterRange:
			n, err := strconv.ParseFloat(v, 64)
			if err != nil || math.IsNaN(n) || math.IsInf(n, 0) || n < f.Min || n > f.Max {
				return nil, &FilterError{src.ID, id, fmt.Sprintf("the value must be a number from %g to %g", f.Min, f.Max)}
			}
			v = strconv.FormatFloat(n, 'f', -1, 64)
		case contract.FilterText:
			if utf8.RuneCountInString(v) > f.TextLimit() {
				return nil, &FilterError{src.ID, id, fmt.Sprintf("the text is longer than %d characters", f.TextLimit())}
			}
		}
		out[id] = v
	}
	return out, nil
}

// typedFilters turns validated values into the typed values that a tool takes.
func typedFilters(src Source, values map[string]string) map[string]any {
	if len(values) == 0 {
		return nil
	}
	out := make(map[string]any, len(values))
	for id, v := range values {
		i := slices.IndexFunc(src.Filters, func(f contract.SearchFilter) bool { return f.ID == id })
		if i < 0 {
			continue
		}
		switch src.Filters[i].Type {
		case contract.FilterBool:
			out[id] = v == "true"
		case contract.FilterRange:
			n, _ := strconv.ParseFloat(v, 64)
			out[id] = n
		default:
			out[id] = v
		}
	}
	return out
}

// filtersKey is the part of a cache key that the filters of one source make.
// It is the JSON of the pairs in ID order, so no value can run into another
// pair whatever characters it holds.
func filtersKey(values map[string]string) string {
	if len(values) == 0 {
		return ""
	}
	ids := make([]string, 0, len(values))
	for id := range values {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	pairs := make([][2]string, len(ids))
	for i, id := range ids {
		pairs[i] = [2]string{id, values[id]}
	}
	b, _ := json.Marshal(pairs)
	return string(b)
}

// withDefaults adds the default that a source declares for each filter the
// request leaves out, so the value the panel shows and the value the add-on
// receives are the same.
func withDefaults(src Source, own map[string]string) map[string]string {
	var out map[string]string
	for _, f := range src.Filters {
		if _, ok := own[f.ID]; ok || f.Default == nil {
			continue
		}
		var v string
		switch d := f.Default.(type) {
		case string:
			v = d
		case bool:
			v = strconv.FormatBool(d)
		case float64:
			v = strconv.FormatFloat(d, 'f', -1, 64)
		default:
			continue
		}
		if out == nil {
			out = make(map[string]string, len(own)+1)
			for k, x := range own {
				out[k] = x
			}
		}
		out[f.ID] = v
	}
	if out == nil {
		return own
	}
	return out
}
