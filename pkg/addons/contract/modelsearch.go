// SPDX-License-Identifier: MIT
package contract

import (
	"fmt"
	"math"
	"slices"
	"unicode/utf8"
)

// The model-search provider slot. An add-on that provides it contributes one
// source of installable models to the host's search panel and to the
// assistant: data and typed filters only. It ships no UI; the host draws the
// rows and the filters, so every source looks the same.

// SlotModelSearch is the ID of the slot in provider-slots.json.
const SlotModelSearch = "model-search"

// The role of the slot that the search tool fills.
const RoleSearch = "search"

// Filter types, a closed list.
const (
	FilterEnum  = "enum"
	FilterBool  = "bool"
	FilterRange = "range"
	FilterText  = "text"
)

// Bounds of a model-search declaration.
const (
	MaxSearchFilters     = 8
	MaxFilterValues      = 32
	MaxFilterTextLength  = 200
	MaxSearchSorts       = 5
	MaxFilterLabelChars  = 40
	MaxFilterHelpChars   = 160
	MaxFilterValueChars  = 40
	maxSourceIconChars   = 40
	maxFilterUnitChars   = 16
	maxI18nFilterEntries = MaxSearchFilters
)

// SearchSorts are the sorts a source may declare. They are the sorts of the
// host's search route.
var SearchSorts = []string{"downloads", "likes", "updated", "trending", "latest"}

// ModelSearch is capabilities.modelSearch (feature level 9).
type ModelSearch struct {
	Source  ModelSearchSource `json:"source"`
	Sorts   []string          `json:"sorts,omitempty"`
	Filters []SearchFilter    `json:"filters,omitempty"`
}

// ModelSearchSource names the source. The ID is the provider ID that results,
// stored choices and the search route use, so it never changes once published.
type ModelSearchSource struct {
	ID    string `json:"id"`
	Title string `json:"title"`
	// Icon is the name of an icon of the host. An unknown name draws none.
	Icon string `json:"icon,omitempty"`
	// ListsLatest says that the source answers with no search text when the
	// sort is latest.
	ListsLatest bool `json:"listsLatest,omitempty"`
	// HasVariants says that opening a result of this source is likely to list
	// several quantizations, so the host shows the variants hint.
	HasVariants bool `json:"hasVariants,omitempty"`
	// P2P says that the models of this source come by torrent, so the host
	// puts the small p2p tag on its chip.
	P2P bool `json:"p2p,omitempty"`
}

// SearchFilter is one typed control that the host draws for the source.
type SearchFilter struct {
	ID    string `json:"id"`
	Type  string `json:"type"`
	Label string `json:"label"`
	Help  string `json:"help,omitempty"`
	// Values are the choices of an enum filter.
	Values []string `json:"values,omitempty"`
	// Default is a string (enum, text), a bool or a number (range).
	Default any `json:"default,omitempty"`
	// Min, Max, Step and Unit describe a range filter.
	Min  float64 `json:"min,omitempty"`
	Max  float64 `json:"max,omitempty"`
	Step float64 `json:"step,omitempty"`
	Unit string  `json:"unit,omitempty"`
	// MaxLength bounds a text filter; zero means MaxFilterTextLength.
	MaxLength int `json:"maxLength,omitempty"`
}

// ModelSearchText is the translated text of the source in one language. A
// field left out is shown in English.
type ModelSearchText struct {
	Title   string                      `json:"title,omitempty"`
	Filters map[string]SearchFilterText `json:"filters,omitempty"`
}

// SearchFilterText translates one filter by ID. Values translates the labels
// of the choices of an enum filter, by value.
type SearchFilterText struct {
	Label  string            `json:"label,omitempty"`
	Help   string            `json:"help,omitempty"`
	Values map[string]string `json:"values,omitempty"`
}

// TextLimit returns the length bound of a text filter.
func (f SearchFilter) TextLimit() int {
	if f.MaxLength > 0 {
		return f.MaxLength
	}
	return MaxFilterTextLength
}

// validateModelSearch checks the declaration against the slot and the tools.
func validateModelSearch(m Manifest) error {
	c := m.Capabilities
	ms := c.ModelSearch
	provides := c.ProvidesSlot(SlotModelSearch)
	if ms == nil {
		if provides {
			return fmt.Errorf("provider slot %s needs capabilities.modelSearch", SlotModelSearch)
		}
		return nil
	}
	if !provides {
		return fmt.Errorf("capabilities.modelSearch needs a provides entry for the slot %s", SlotModelSearch)
	}
	src := ms.Source
	if !ValidName(src.ID) || src.ID == ProviderBuiltin || src.ID == ProviderOff {
		return fmt.Errorf("modelSearch: invalid source id")
	}
	if !plainText(src.Title, MaxTitleChars) {
		return fmt.Errorf("modelSearch: invalid source title")
	}
	if src.Icon != "" && (!ValidName(src.Icon) || utf8.RuneCountInString(src.Icon) > maxSourceIconChars) {
		return fmt.Errorf("modelSearch: invalid source icon")
	}
	if len(ms.Sorts) > MaxSearchSorts {
		return fmt.Errorf("modelSearch: too many sorts")
	}
	seenSort := map[string]bool{}
	for _, s := range ms.Sorts {
		if !slices.Contains(SearchSorts, s) || seenSort[s] {
			return fmt.Errorf("modelSearch: invalid or repeated sort %q", s)
		}
		seenSort[s] = true
	}
	if len(ms.Filters) > MaxSearchFilters {
		return fmt.Errorf("modelSearch: at most %d filters", MaxSearchFilters)
	}
	seen := map[string]bool{}
	for _, f := range ms.Filters {
		if err := f.validate(); err != nil {
			return fmt.Errorf("modelSearch: filter %q: %w", f.ID, err)
		}
		if seen[f.ID] {
			return fmt.Errorf("modelSearch: filter %q is declared twice", f.ID)
		}
		seen[f.ID] = true
	}
	// The slot's role must name a tool that takes the arguments the host sends.
	for _, d := range c.Provides {
		if d.Slot != SlotModelSearch {
			continue
		}
		if len(d.Roles[RoleSearch]) != 1 {
			return fmt.Errorf("provider slot %s: role %s names one tool", SlotModelSearch, RoleSearch)
		}
	}
	// Translations name only what the manifest declares.
	for tag, t := range m.I18n {
		if t.ModelSearch == nil {
			continue
		}
		if err := t.ModelSearch.validate(*ms); err != nil {
			return fmt.Errorf("i18n %q: %w", tag, err)
		}
	}
	return nil
}

func (f SearchFilter) validate() error {
	// The ID of a filter reaches the add-on as a key of the arguments, so the
	// names that no tool argument may use are refused here as well.
	if !ValidName(f.ID) || f.ID == "q" || f.ID == "limit" || f.ID == "sort" || ForbiddenToolProperty(f.ID) {
		return fmt.Errorf("invalid id")
	}
	if !plainText(f.Label, MaxFilterLabelChars) {
		return fmt.Errorf("invalid label")
	}
	if f.Help != "" && !plainText(f.Help, MaxFilterHelpChars) {
		return fmt.Errorf("invalid help")
	}
	if f.Type != FilterEnum && (len(f.Values) > 0) {
		return fmt.Errorf("only an enum takes values")
	}
	if f.Type != FilterRange && (f.Min != 0 || f.Max != 0 || f.Step != 0 || f.Unit != "") {
		return fmt.Errorf("only a range takes min, max, step and unit")
	}
	if f.Type != FilterText && f.MaxLength != 0 {
		return fmt.Errorf("only a text takes maxLength")
	}
	switch f.Type {
	case FilterEnum:
		if len(f.Values) == 0 || len(f.Values) > MaxFilterValues {
			return fmt.Errorf("an enum takes 1 to %d values", MaxFilterValues)
		}
		seen := map[string]bool{}
		for _, v := range f.Values {
			if !plainText(v, MaxFilterValueChars) || seen[v] {
				return fmt.Errorf("invalid or repeated value %q", v)
			}
			seen[v] = true
		}
		if d, ok := f.Default.(string); !ok || !slices.Contains(f.Values, d) {
			return fmt.Errorf("an enum needs a default that is one of its values")
		}
	case FilterBool:
		if _, ok := f.Default.(bool); f.Default != nil && !ok {
			return fmt.Errorf("a bool default is true or false")
		}
	case FilterRange:
		if math.IsNaN(f.Min) || math.IsNaN(f.Max) || f.Max <= f.Min || f.Step < 0 || (f.Unit != "" && !plainText(f.Unit, maxFilterUnitChars)) {
			return fmt.Errorf("invalid range")
		}
		if f.Default != nil {
			d, ok := f.Default.(float64)
			if !ok || d < f.Min || d > f.Max {
				return fmt.Errorf("the default is outside the range")
			}
		}
	case FilterText:
		if f.MaxLength < 0 || f.MaxLength > MaxFilterTextLength {
			return fmt.Errorf("maxLength is at most %d", MaxFilterTextLength)
		}
		if d, ok := f.Default.(string); f.Default != nil && (!ok || utf8.RuneCountInString(d) > f.TextLimit()) {
			return fmt.Errorf("invalid default")
		}
	default:
		return fmt.Errorf("unknown type %q", f.Type)
	}
	return nil
}

func (t ModelSearchText) validate(ms ModelSearch) error {
	if t.Title == "" && len(t.Filters) == 0 {
		return fmt.Errorf("modelSearch translates nothing")
	}
	if t.Title != "" && !plainText(t.Title, MaxTitleChars) {
		return fmt.Errorf("invalid source title")
	}
	if len(t.Filters) > maxI18nFilterEntries {
		return fmt.Errorf("too many filters")
	}
	for id, ft := range t.Filters {
		i := slices.IndexFunc(ms.Filters, func(f SearchFilter) bool { return f.ID == id })
		if i < 0 {
			return fmt.Errorf("translates the filter %q that the manifest does not declare", id)
		}
		f := ms.Filters[i]
		if ft.Label != "" && !plainText(ft.Label, MaxFilterLabelChars) || ft.Help != "" && !plainText(ft.Help, MaxFilterHelpChars) {
			return fmt.Errorf("filter %q: invalid text", id)
		}
		for v, label := range ft.Values {
			if f.Type != FilterEnum || !slices.Contains(f.Values, v) || !plainText(label, MaxFilterValueChars) {
				return fmt.Errorf("filter %q: invalid value text for %q", id, v)
			}
		}
	}
	return nil
}

// ModelSearchRequest is what the host sends to the search tool of a source.
type ModelSearchRequest struct {
	Query  string `json:"q"`
	Limit  int    `json:"limit"`
	Sort   string `json:"sort,omitempty"`
	Format string `json:"format,omitempty"`
	// Filters holds the values the person chose, typed: a string for an enum
	// or a text, a bool, a number for a range.
	Filters map[string]any `json:"filters,omitempty"`
}
