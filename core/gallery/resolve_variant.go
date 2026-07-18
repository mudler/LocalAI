package gallery

import (
	"errors"
	"fmt"
	"sort"
	"strings"
)

var (
	// ErrNoVariantMatch is returned when nothing at all is selectable, which
	// only happens for a caller that supplies no base option.
	ErrNoVariantMatch = errors.New("no model variant matches this system")
	// ErrPinNotFound is returned when a pinned variant is absent from the list.
	ErrPinNotFound = errors.New("pinned model variant not found")
)

// VariantOption is a variant paired with the facts a host is matched against.
//
// The install layer builds these by looking the referenced entries up in the
// live gallery. The selector never touches the catalog, which is what keeps it
// pure and testable across hardware shapes without touching the machine.
type VariantOption struct {
	Variant Variant
	// Backend is the engine the referenced entry resolves to (e.g. "llama-cpp",
	// "mlx", "vllm"). Hardware support is derived from this name alone, which is
	// precisely why a gallery author never has to describe hardware.
	Backend string
	// IsBase marks the declaring entry's own payload. It is exempt from every
	// filter and is the answer when nothing else survives, because the entry
	// must stay installable on every host and for every client.
	IsBase bool
	// ProbedMemory is the footprint measured live from the referenced entry's
	// weights, in bytes. It is only consulted when the variant declares no
	// min_memory of its own, because a human who measured a real load knows
	// more than a pre-download estimate does.
	//
	// Zero means the probe could not determine a size. That is an unknown, not
	// a zero requirement: a probe that cannot reach the network must never be
	// able to break an install.
	ProbedMemory uint64
}

// EffectiveMemory returns this option's memory requirement in bytes and whether
// one is known at all: the authored figure when there is one, else the live
// probe result, else nothing.
func (o VariantOption) EffectiveMemory() (uint64, bool, error) {
	size, known, err := o.Variant.AuthoredMinMemory()
	if err != nil || known {
		return size, known, err
	}
	if o.ProbedMemory > 0 {
		return o.ProbedMemory, true, nil
	}
	return 0, false, nil
}

// ResolveEnv describes the host a variant is selected for.
type ResolveEnv struct {
	// AvailableMemory is what a model may occupy on this host: VRAM when a
	// usable GPU was detected, system RAM otherwise. A model's footprint is
	// roughly the same in either, so one number and one comparison cover both.
	AvailableMemory uint64
	// BackendCompatible reports whether a backend can run here. The install
	// layer wires SystemState.IsBackendCompatible, which already derives
	// Darwin-only, NVIDIA-only, ROCm-only and SYCL-only from the backend name.
	//
	// A nil func treats every backend as runnable, the right default for a
	// caller with no view of the hardware.
	BackendCompatible func(backend string) bool
	// ProbeMemory measures how much memory a referenced gallery entry needs,
	// without downloading it. It is consulted only for variants that declare no
	// min_memory, and a zero result means "could not tell", never "needs
	// nothing".
	//
	// It is a func field rather than a live network handle so specs can pin an
	// exact size, or an exact failure, without reaching the internet. A nil func
	// leaves every unauthored variant unknown, which selection already handles.
	//
	// SelectVariant never calls this: the install layer resolves every size into
	// VariantOption.ProbedMemory first, so the selector stays pure.
	ProbeMemory func(entry *GalleryModel) uint64
}

func (e ResolveEnv) backendRuns(backend string) bool {
	if e.BackendCompatible == nil {
		return true
	}
	return e.BackendCompatible(backend)
}

// VariantSelection is the outcome of a selection pass.
type VariantSelection struct {
	Option VariantOption
	// FellBackToBase reports that no declared variant survived and the entry's
	// own payload was chosen instead. Callers log this, because a host quietly
	// taking the base when upgrades were on offer is worth being able to see.
	FellBackToBase bool
	// Reasons explains, one line per rejected variant, why it was dropped.
	Reasons []string
}

// SelectVariant picks the option to install for a host.
//
// The algorithm, in order:
//
//  1. An explicit pin wins outright, fit or no fit. It is a deliberate operator
//     override, so refusing it would defeat the point; the caller warns.
//  2. Variants whose backend cannot run here are dropped. This is the whole of
//     the hardware gate, derived from the backend name.
//  3. Variants whose known memory requirement exceeds what the host has are
//     dropped. A variant with an UNKNOWN requirement survives, because nothing
//     proves it does not fit and refusing on a size the probe could not read
//     would let a network hiccup silently downgrade what gets installed.
//  4. The largest survivor wins. A bigger footprint is a higher quality
//     quantization of the same model, so among things that fit, more is better.
//     Unknown requirements rank last, so a proven fit always beats a guess.
//  5. With no survivor the base option wins. The base always installs; this
//     never refuses.
func SelectVariant(options []VariantOption, env ResolveEnv, pin string) (VariantSelection, error) {
	if pin != "" {
		for _, o := range options {
			if strings.EqualFold(o.Variant.Model, pin) {
				return VariantSelection{Option: o}, nil
			}
		}
		return VariantSelection{}, fmt.Errorf("%w: %q is not among the %d variants of this model", ErrPinNotFound, pin, len(options))
	}

	type ranked struct {
		option VariantOption
		memory uint64
		known  bool
	}

	var base *VariantOption
	survivors := make([]ranked, 0, len(options))
	reasons := make([]string, 0, len(options))

	for i := range options {
		o := options[i]
		if o.IsBase {
			base = &options[i]
			continue
		}

		if !env.backendRuns(o.Backend) {
			reasons = append(reasons, fmt.Sprintf("%s needs backend %q, which cannot run on this system", o.Variant.Model, o.Backend))
			continue
		}

		memory, known, err := o.EffectiveMemory()
		if err != nil {
			return VariantSelection{}, err
		}
		if known && memory > env.AvailableMemory {
			reasons = append(reasons, fmt.Sprintf("%s needs %s of memory", o.Variant.Model, humanBytes(memory)))
			continue
		}

		survivors = append(survivors, ranked{option: o, memory: memory, known: known})
	}

	if len(survivors) > 0 {
		// Stable so that variants with identical requirements keep their
		// authored order, which is the only thing order still decides.
		sort.SliceStable(survivors, func(i, j int) bool {
			if survivors[i].known != survivors[j].known {
				return survivors[i].known
			}
			return survivors[i].memory > survivors[j].memory
		})
		return VariantSelection{Option: survivors[0].option, Reasons: reasons}, nil
	}

	if base != nil {
		return VariantSelection{Option: *base, FellBackToBase: true, Reasons: reasons}, nil
	}

	return VariantSelection{}, fmt.Errorf(
		"%w: %s of memory available; variants: %s",
		ErrNoVariantMatch, humanBytes(env.AvailableMemory), strings.Join(reasons, "; "),
	)
}

func humanBytes(b uint64) string {
	const unit = 1024
	if b < unit {
		return fmt.Sprintf("%dB", b)
	}
	div, exp := uint64(unit), 0
	for n := b / unit; n >= unit; n /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f%ciB", float64(b)/float64(div), "KMGTPE"[exp])
}
