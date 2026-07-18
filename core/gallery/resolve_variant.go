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
	// filter, because the entry must stay installable on every host and for
	// every client, but it is otherwise an ordinary candidate: it is ranked
	// against the declared variants rather than consulted only once they have
	// all been rejected.
	IsBase bool
	// ProbedMemory is the footprint measured live from the referenced entry's
	// weights, in bytes. It is the only source of a variant's size. An author
	// who needs to correct a bad estimate sets `size:` on the referenced entry,
	// which the estimator behind the probe already prefers over its own
	// guesswork, so the correction lands everywhere the size is used.
	//
	// Zero means the probe could not determine a size. That is an unknown, not
	// a zero requirement: a probe that cannot reach the network must never be
	// able to break an install.
	ProbedMemory uint64
}

// EffectiveMemory returns this option's memory requirement in bytes and whether
// one is known at all.
func (o VariantOption) EffectiveMemory() (uint64, bool) {
	if o.ProbedMemory > 0 {
		return o.ProbedMemory, true
	}
	return 0, false
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
	// without downloading it. A zero result means "could not tell", never
	// "needs nothing".
	//
	// It is a func field rather than a live network handle so specs can pin an
	// exact size, or an exact failure, without reaching the internet. A nil func
	// leaves every variant unknown, which selection already handles.
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
	// FellBackToBase reports that no declared variant survived the filters and
	// the entry's own payload was all that remained. Callers log this, because a
	// host quietly taking the base when upgrades were on offer is worth being
	// able to see.
	//
	// It is deliberately narrower than "the base was selected": the base also
	// wins on merit whenever it outranks every surviving variant, and that is an
	// ordinary, uninteresting outcome rather than something to warn about.
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
//  4. The base survives both filters unconditionally, and then competes. It is
//     a candidate like any other, not a last resort: an entry whose own build is
//     the largest thing that fits must win against a smaller variant, and a base
//     of known size must win against a variant whose size nothing could measure.
//  5. The survivors are ranked and the best one wins, by rankOf below.
//  6. With no survivor at all, which can only happen when the caller supplied no
//     base, there is nothing to install and this reports ErrNoVariantMatch.
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
		rank   int
	}

	survivors := make([]ranked, 0, len(options))
	reasons := make([]string, 0, len(options))
	survivingVariants := 0

	for i := range options {
		o := options[i]
		memory, known := o.EffectiveMemory()

		// The base skips both gates. There is nothing below it, so refusing it
		// would make an entry every older LocalAI installs fine uninstallable on
		// newer ones.
		if !o.IsBase {
			if !env.backendRuns(o.Backend) {
				reasons = append(reasons, fmt.Sprintf("%s needs backend %q, which cannot run on this system", o.Variant.Model, o.Backend))
				continue
			}
			if known && memory > env.AvailableMemory {
				reasons = append(reasons, fmt.Sprintf("%s needs %s of memory", o.Variant.Model, humanBytes(memory)))
				continue
			}
			survivingVariants++
		}

		survivors = append(survivors, ranked{option: o, memory: memory, rank: rankOf(o, env)})
	}

	if len(survivors) == 0 {
		return VariantSelection{}, fmt.Errorf(
			"%w: %s of memory available; variants: %s",
			ErrNoVariantMatch, humanBytes(env.AvailableMemory), strings.Join(reasons, "; "),
		)
	}

	// Stable so that options within one rank and of identical size keep their
	// authored order, which is the only thing order still decides.
	sort.SliceStable(survivors, func(i, j int) bool {
		if survivors[i].rank != survivors[j].rank {
			return survivors[i].rank < survivors[j].rank
		}
		return survivors[i].memory > survivors[j].memory
	})

	winner := survivors[0].option
	return VariantSelection{
		Option: winner,
		// Only a base that won by default is worth reporting. A base that
		// outranked live competition is an ordinary selection.
		FellBackToBase: winner.IsBase && survivingVariants == 0,
		Reasons:        reasons,
	}, nil
}

// Ranks, best first. Within a rank the larger footprint wins, because a bigger
// build is a higher quality quantization of the same model.
const (
	// rankProvenFit is a measured size that the host is measured to satisfy.
	rankProvenFit = iota
	// rankBase is the entry's own build when it is not a proven fit: either it
	// needs more memory than the host reports, or its size could not be
	// measured either. It still outranks any unsized variant, because it is the
	// payload the entry is guaranteed to be able to install and a variant of
	// unmeasurable size is a guess. Taking the guess on a host that cannot be
	// shown to accommodate it is how an unreachable network silently changes
	// what gets installed.
	rankBase
	// rankUnknownFit is a variant whose size nothing could measure. Nothing
	// proves it does not fit, so it is not dropped, but nothing proves it does
	// either, so it ranks last.
	rankUnknownFit
)

func rankOf(o VariantOption, env ResolveEnv) int {
	if memory, known := o.EffectiveMemory(); known && memory <= env.AvailableMemory {
		return rankProvenFit
	}
	if o.IsBase {
		return rankBase
	}
	return rankUnknownFit
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
