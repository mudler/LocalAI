// SPDX-License-Identifier: MIT
package config

import (
	"fmt"

	gguf "github.com/gpustack/gguf-parser-go"

	"github.com/mudler/LocalAI/core/config/harnessctx"
)

// HostMemory is the memory of the machine a model would run on, in the two
// figures the automatic context size reads: the smallest per-device VRAM
// (0 = no GPU or unknown) and the total system memory (0 = unknown).
type HostMemory struct {
	PerDeviceVRAM uint64
	SystemRAM     uint64
}

// ContextFit is the verdict of a context plan.
type ContextFit string

const (
	// FitFits means the model runs within the memory budget with a window of at
	// least the chat minimum of an administrator (harnessctx.MinContextAdmin).
	FitFits ContextFit = "fits"
	// FitTight means the model runs but with less room than a chat wants: a
	// window between the two chat minimums, or a footprint over the memory
	// budget that still fits in the memory the host has.
	FitTight ContextFit = "tight"
	// FitNone means the model cannot run a chat on this host: the window drops
	// below the chat minimum, or the footprint exceeds the memory.
	FitNone ContextFit = "does_not_fit"
	// FitUnknown means the footprint could not be estimated, or the host memory
	// is not known.
	FitUnknown ContextFit = "unknown"
)

// The reasons a plan gives next to its verdict. They are stable keys: a client
// turns them into text.
const (
	// ReasonWindowReduced: the window is below harnessctx.RecommendedContext.
	ReasonWindowReduced = "window_reduced"
	// ReasonLittleHeadroom: the model fits the memory, but not within the
	// headroom the loader keeps free.
	ReasonLittleHeadroom = "little_headroom"
	// ReasonBelowChatMinimum: even the largest window that fits is below
	// harnessctx.MinContext, which the chat agent needs.
	ReasonBelowChatMinimum = "below_chat_minimum"
	// ReasonExceedsMemory: the weights alone do not fit the memory.
	ReasonExceedsMemory = "exceeds_memory"
	// ReasonEstimateUnavailable: no footprint could be computed.
	ReasonEstimateUnavailable = "estimate_unavailable"
	// ReasonHardwareUnknown: the host memory is not known.
	ReasonHardwareUnknown = "hardware_unknown"
)

// ContextPlan is what installing a model on a host would give: the context
// window the loader would choose, the memory that window costs and a verdict.
type ContextPlan struct {
	ContextSize int
	// MemoryBytes is the estimated footprint at ContextSize: per-device VRAM on
	// a host with a GPU, system memory on one without. 0 when unknown.
	MemoryBytes uint64
	Fit         ContextFit
	Reasons     []string
}

// ModelProfile is what the context planner needs from the header of a GGUF
// file. It is a few hundred bytes and a property of the file bytes, so a caller
// may keep it for as long as the file does not change.
type ModelProfile struct {
	// FileSize is the size of the GGUF file, the weights the plan counts.
	FileSize uint64
	h        ggufHeader
	hasChat  bool
	trained  int
	hasTrain bool
}

// NewModelProfile reduces a parsed GGUF to a ModelProfile. It fails when the
// estimator cannot read the file, never by panicking.
func NewModelProfile(f *gguf.GGUFFile) (profile ModelProfile, err error) {
	if f == nil {
		return ModelProfile{}, fmt.Errorf("no GGUF file")
	}
	defer func() {
		if r := recover(); r != nil {
			profile, err = ModelProfile{}, fmt.Errorf("GGUF estimate failed: %v", r)
		}
	}()
	h := readGGUFHeader(f)
	if h.trainedPanic != nil {
		return ModelProfile{}, fmt.Errorf("GGUF estimate failed: %v", h.trainedPanic)
	}
	return ModelProfile{FileSize: uint64(f.Size), h: h, hasChat: (&ModelConfig{}).ServesChat(h.Architecture), trained: h.TrainedContext, hasTrain: h.TrainedContext > 0}, nil
}

// PlanContext says what window the loader gives this model on a host and
// whether the model fits there.
//
// The window is chosen by the same code as at load time, for a model that sets
// no context_size: autoContextSizeFor, with the chat target and the same ladder
// and memory budgets. It is not a second formula. Only the verdict is new: the
// loader never refuses a model, a client that recommends one has to.
func (p ModelProfile) PlanContext(hw HostMemory) ContextPlan {
	if !p.hasTrain {
		return ContextPlan{ContextSize: DefaultContextSize, Fit: FitUnknown, Reasons: []string{ReasonEstimateUnavailable}}
	}
	cfg := &ModelConfig{}
	ctx := autoContextSizeFor(hw.PerDeviceVRAM, hw.SystemRAM, p.h.footprintAt, p.h.ramFootprintAt, p.trained, cfg.AutoContextTarget(p.h.Architecture))

	onGPU := hw.PerDeviceVRAM > 0
	footprint, budget := p.h.ramFootprintAt(ctx), hw.SystemRAM
	if onGPU {
		footprint, budget = p.h.footprintAt(ctx), hw.PerDeviceVRAM
	}
	plan := ContextPlan{ContextSize: ctx, MemoryBytes: footprint}
	if budget == 0 {
		plan.Fit, plan.Reasons = FitUnknown, []string{ReasonHardwareUnknown}
		return plan
	}
	if footprint == 0 {
		plan.Fit, plan.Reasons = FitUnknown, []string{ReasonEstimateUnavailable}
		return plan
	}

	inBudget := contextFitsRAM(footprint, budget)
	if onGPU {
		inBudget = contextFitsVRAM(footprint, budget)
	}
	switch {
	case footprint > budget:
		plan.Fit, plan.Reasons = FitNone, []string{ReasonExceedsMemory}
	case ctx < harnessctx.MinContext:
		plan.Fit, plan.Reasons = FitNone, []string{ReasonBelowChatMinimum}
	case !inBudget:
		plan.Fit, plan.Reasons = FitTight, []string{ReasonLittleHeadroom}
	case ctx < harnessctx.MinContextAdmin:
		plan.Fit = FitTight
	default:
		plan.Fit = FitFits
	}
	if ctx < harnessctx.RecommendedContext && plan.Fit != FitNone {
		plan.Reasons = append(plan.Reasons, ReasonWindowReduced)
	}
	return plan
}
