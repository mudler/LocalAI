package config

import (
	"fmt"
	"reflect"
	"slices"
	"strings"
	"time"

	"github.com/mudler/LocalAI/pkg/vrambudget"
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
		if f.snapshotOnly || !isMemoryRow(f) {
			continue
		}
		if f.envSet(o, baseline) {
			f.snapshot(o, &out)
		}
	}
	return out
}

// ValidateMemorySettings checks the memory settings of s the way POST
// /api/settings does, so a value the server would ignore is refused.
func ValidateMemorySettings(s RuntimeSettings) error {
	for name, d := range map[string]*string{
		"watchdog_idle_timeout": s.WatchdogIdleTimeout, "watchdog_busy_timeout": s.WatchdogBusyTimeout,
		"watchdog_interval": s.WatchdogInterval, "lru_eviction_retry_interval": s.LRUEvictionRetryInterval,
	} {
		if d != nil {
			if _, err := time.ParseDuration(*d); err != nil {
				return fmt.Errorf("invalid %s: %w", name, err)
			}
		}
	}
	if s.MaxActiveBackends != nil && *s.MaxActiveBackends < 0 {
		return fmt.Errorf("max_active_backends must be 0 (no limit) or more")
	}
	if s.LRUEvictionMaxRetries != nil && *s.LRUEvictionMaxRetries < 0 {
		return fmt.Errorf("lru_eviction_max_retries must be 0 or more")
	}
	if s.MemoryReclaimerThreshold != nil && (*s.MemoryReclaimerThreshold <= 0 || *s.MemoryReclaimerThreshold > 1) {
		return fmt.Errorf("memory_reclaimer_threshold must be in (0, 1]")
	}
	if s.VRAMBudget != nil && *s.VRAMBudget != "" {
		if _, err := vrambudget.Parse(*s.VRAMBudget); err != nil {
			return fmt.Errorf("invalid vram_budget: %w", err)
		}
	}
	return nil
}

// isMemoryRow reports whether a registry row owns a memory setting.
func isMemoryRow(f fieldSpec) bool {
	return slices.ContainsFunc(f.jsonNames, func(n string) bool { return slices.Contains(MemorySettingKeys, n) })
}

// MemorySettingsWithDefaults returns the memory settings of doc with every
// one that doc does not set taken from an option-less run. In distributed
// mode a frontend applies the cluster document through it, so a setting
// the document lacks has the same value on every frontend, not the value of
// one frontend's file or env.
func MemorySettingsWithDefaults(doc RuntimeSettings) RuntimeSettings {
	mem, _ := SplitMemorySettings(doc)
	baseline := DefaultRuntimeBaseline()
	for _, f := range runtimeSettingsFields {
		if f.snapshotOnly || !isMemoryRow(f) || f.isSet(&mem) {
			continue
		}
		f.snapshot(baseline, &mem)
	}
	return mem
}
