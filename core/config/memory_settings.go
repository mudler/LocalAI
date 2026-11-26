package config

import (
	"reflect"
	"slices"
	"strings"
)

// MemorySettingKeys are the runtime settings that decide which models stay
// in memory: the model limit, eviction, model swap, the watchdog, the memory
// reclaimer and the VRAM budget. In distributed mode they are cluster
// settings: one value stored in the database for every frontend, applied per
// worker node (see core/services/clustersettings).
var MemorySettingKeys = []string{
	"watchdog_enabled", "watchdog_idle_enabled", "watchdog_busy_enabled",
	"watchdog_idle_timeout", "watchdog_busy_timeout", "watchdog_interval",
	"max_active_backends", "single_backend",
	"memory_reclaimer_enabled", "memory_reclaimer_threshold",
	"force_eviction_when_busy", "size_aware_eviction",
	"lru_eviction_max_retries", "lru_eviction_retry_interval",
	"model_swap", "vram_budget",
}

func jsonName(f reflect.StructField) string {
	name, _, _ := strings.Cut(f.Tag.Get("json"), ",")
	return name
}

// SplitMemorySettings splits s into the memory settings and the rest. Both
// keep only the fields that s sets.
func SplitMemorySettings(s RuntimeSettings) (memory, rest RuntimeSettings) {
	src := reflect.ValueOf(s)
	mem := reflect.ValueOf(&memory).Elem()
	oth := reflect.ValueOf(&rest).Elem()
	t := src.Type()
	for i := range t.NumField() {
		if slices.Contains(MemorySettingKeys, jsonName(t.Field(i))) {
			mem.Field(i).Set(src.Field(i))
		} else {
			oth.Field(i).Set(src.Field(i))
		}
	}
	return memory, rest
}

// IsEmpty reports whether s sets no field.
func (s RuntimeSettings) IsEmpty() bool {
	v := reflect.ValueOf(s)
	for i := range v.NumField() {
		if f := v.Field(i); f.Kind() == reflect.Pointer && !f.IsNil() {
			return false
		}
	}
	return true
}

// SetKeys lists the JSON names of the fields that s sets, in field order.
func (s RuntimeSettings) SetKeys() []string {
	v := reflect.ValueOf(s)
	var out []string
	for i := range v.NumField() {
		if f := v.Field(i); f.Kind() == reflect.Pointer && !f.IsNil() {
			out = append(out, jsonName(v.Type().Field(i)))
		}
	}
	return out
}

// EnvMemorySettings returns the memory settings that env or CLI flags set on
// o: the ones that differ from an option-less run. In distributed mode they
// seed an empty cluster store and are otherwise only compared with it.
func EnvMemorySettings(o *ApplicationConfig) RuntimeSettings {
	baseline := DefaultRuntimeBaseline()
	var out RuntimeSettings
	for _, f := range runtimeSettingsFields {
		if f.snapshotOnly || !slices.ContainsFunc(f.jsonNames, func(n string) bool { return slices.Contains(MemorySettingKeys, n) }) {
			continue
		}
		if f.envSet(o, baseline) {
			f.snapshot(o, &out)
		}
	}
	return out
}
