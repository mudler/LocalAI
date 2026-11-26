package enginesettings

import (
	"fmt"
	"sort"
	"strconv"
	"strings"
)

// PlacementMode names a tensor placement preset of the tensor-placement
// capability.
type PlacementMode string

const (
	// ExpertsCPUAll keeps the MoE expert tensors of every layer on the CPU.
	ExpertsCPUAll PlacementMode = "experts_cpu_all"
	// ExpertsCPULayers keeps the expert tensors of the listed layers on the CPU.
	ExpertsCPULayers PlacementMode = "experts_cpu_layers"
	// FirstLayersGPU keeps the first N layers entirely on the GPU and the
	// expert tensors of all later layers on the CPU.
	FirstLayersGPU PlacementMode = "first_layers_gpu"
)

// MaxLayer is the highest layer number a placement may name.
const MaxLayer = 999

// LayerRange is an inclusive range of layer numbers.
type LayerRange struct {
	From int `json:"from"`
	To   int `json:"to"`
}

// Placement is the structured form of a tensor placement entry.
type Placement struct {
	Mode PlacementMode `json:"mode"`
	// Layers is used by experts_cpu_layers.
	Layers []LayerRange `json:"layers,omitempty"`
	// FirstGPULayers is used by first_layers_gpu.
	FirstGPULayers int `json:"first_gpu_layers,omitempty"`
	// TotalLayers is optional for first_layers_gpu. When set, the entry
	// lists the remaining layers as one range; when 0, it matches every
	// layer from FirstGPULayers up.
	TotalLayers int `json:"total_layers,omitempty"`
}

// PlacementInfo links a placement options entry to its structured form.
// Placement is nil when the engine did not build the entry.
type PlacementInfo struct {
	Option    string     `json:"option"`
	Placement *Placement `json:"placement"`
}

// ParseLayerRanges reads "0-6,18-23" or "5". Ranges are sorted and merged.
func ParseLayerRanges(s string) ([]LayerRange, error) {
	var rs []LayerRange
	for _, part := range strings.Split(s, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		a, b, isRange := strings.Cut(part, "-")
		from, err := strconv.Atoi(strings.TrimSpace(a))
		if err != nil {
			return nil, fmt.Errorf("%q is not a layer number or a range", part)
		}
		to := from
		if isRange {
			if to, err = strconv.Atoi(strings.TrimSpace(b)); err != nil {
				return nil, fmt.Errorf("%q is not a layer number or a range", part)
			}
		}
		rs = append(rs, LayerRange{from, to})
	}
	return NormalizeLayerRanges(rs)
}

// FormatLayerRanges is the inverse of ParseLayerRanges.
func FormatLayerRanges(rs []LayerRange) string {
	parts := make([]string, 0, len(rs))
	for _, r := range rs {
		if r.From == r.To {
			parts = append(parts, strconv.Itoa(r.From))
		} else {
			parts = append(parts, fmt.Sprintf("%d-%d", r.From, r.To))
		}
	}
	return strings.Join(parts, ",")
}

// NormalizeLayerRanges validates, sorts and merges ranges. Ranges that
// overlap or touch become one.
func NormalizeLayerRanges(in []LayerRange) ([]LayerRange, error) {
	rs := append([]LayerRange(nil), in...)
	for _, r := range rs {
		if r.From < 0 || r.To > MaxLayer {
			return nil, fmt.Errorf("layers must be between 0 and %d", MaxLayer)
		}
		if r.From > r.To {
			return nil, fmt.Errorf("the range %d-%d runs backwards", r.From, r.To)
		}
	}
	sort.Slice(rs, func(i, j int) bool { return rs[i].From < rs[j].From })
	var out []LayerRange
	for _, r := range rs {
		if k := len(out); k > 0 && r.From <= out[k-1].To+1 {
			if r.To > out[k-1].To {
				out[k-1].To = r.To
			}
			continue
		}
		out = append(out, r)
	}
	return out, nil
}
