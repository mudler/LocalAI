package meta

import (
	"reflect"
	"sort"
	"sync"
)

var (
	cachedMetadata *ConfigMetadata
	cacheMu        sync.RWMutex
)

// BuildConfigMetadata reflects on the given struct type (ModelConfig),
// merges the enrichment registry, and returns the full ConfigMetadata.
// The result is cached in memory after the first call.
func BuildConfigMetadata(modelConfigType reflect.Type) *ConfigMetadata {
	cacheMu.RLock()
	if cachedMetadata != nil {
		cacheMu.RUnlock()
		return cachedMetadata
	}
	cacheMu.RUnlock()

	cacheMu.Lock()
	defer cacheMu.Unlock()

	if cachedMetadata != nil {
		return cachedMetadata
	}

	cachedMetadata = buildConfigMetadataUncached(modelConfigType, DefaultRegistry())
	return cachedMetadata
}

// buildConfigMetadataUncached does the actual work without caching.
func buildConfigMetadataUncached(modelConfigType reflect.Type, registry map[string]FieldMetaOverride) *ConfigMetadata {
	fields := WalkModelConfig(modelConfigType)

	for i := range fields {
		override, ok := registry[fields[i].Path]
		if !ok {
			continue
		}
		applyOverride(&fields[i], override)
	}

	allSections := DefaultSections()

	sectionOrder := make(map[string]int, len(allSections))
	for _, s := range allSections {
		sectionOrder[s.ID] = s.Order
	}

	sort.SliceStable(fields, func(i, j int) bool {
		si := sectionOrder[fields[i].Section]
		sj := sectionOrder[fields[j].Section]
		if si != sj {
			return si < sj
		}
		return fields[i].Order < fields[j].Order
	})

	usedSections := make(map[string]bool)
	for _, f := range fields {
		usedSections[f.Section] = true
	}

	var sections []Section
	for _, s := range allSections {
		if usedSections[s.ID] {
			sections = append(sections, s)
		}
	}

	return &ConfigMetadata{
		Sections: sections,
		Fields:   fields,
	}
}

// applyOverride merges non-zero override values into the field.
func applyOverride(f *FieldMeta, o FieldMetaOverride) {
	if o.Section != "" {
		f.Section = o.Section
	}
	if o.Label != "" {
		f.Label = o.Label
	}
	if o.Description != "" {
		f.Description = o.Description
	}
	if o.Component != "" {
		f.Component = o.Component
	}
	if o.Placeholder != "" {
		f.Placeholder = o.Placeholder
	}
	if o.Default != nil {
		f.Default = o.Default
	}
	if o.Min != nil {
		f.Min = o.Min
	}
	if o.Max != nil {
		f.Max = o.Max
	}
	if o.Step != nil {
		f.Step = o.Step
	}
	if o.Options != nil {
		f.Options = o.Options
	}
	if o.AutocompleteProvider != "" {
		f.AutocompleteProvider = o.AutocompleteProvider
	}
	if o.VRAMImpact {
		f.VRAMImpact = true
	}
	if o.Advanced {
		f.Advanced = true
	}
	if o.Order != 0 {
		f.Order = o.Order
	}
}

// BuildForTest builds metadata without caching, for use in tests.
func BuildForTest(modelConfigType reflect.Type, registry map[string]FieldMetaOverride) *ConfigMetadata {
	return buildConfigMetadataUncached(modelConfigType, registry)
}

