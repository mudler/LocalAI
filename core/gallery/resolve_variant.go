package gallery

import (
	"errors"
	"fmt"
	"slices"
	"sort"
	"strings"
	"unicode"
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
	// Tags are the referenced entry's declared tags, and they are the
	// AUTHORITATIVE serving feature signal: a tag is something an author wrote
	// down on purpose, whereas an entry name only happens to contain a marker.
	//
	// Empty does not mean "no features". An entry nobody has tagged yet falls
	// back to its name, so tagging discipline improves ranking without being a
	// precondition for it.
	Tags []string
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
	// EnginePreference lists the ENGINE NAMES this host prefers, best first, as
	// SystemState.EnginePreferenceTokens reports them (e.g. NVIDIA gives
	// ["vllm", "sglang", "llama-cpp"], metal gives ["mlx", "llama-cpp"]). A
	// token is matched as a substring of a variant's backend name.
	//
	// The vocabulary is load bearing. VariantOption.Backend is a gallery entry's
	// `backend:` value, which is an engine name and never carries a build tag,
	// so build tags like "cuda" or "rocm" match NOTHING here. Do not wire
	// SystemState.BackendPreferenceTokens into this field: that reports build
	// tags for installed-build alias resolution, and the mismatch does not
	// error, it scores every candidate equally and silently reduces selection to
	// size alone. pkg/system/capabilities.go documents both tables.
	//
	// BackendCompatible answers "can this run here at all" and is a filter.
	// This answers "which of the things that CAN run here should win" and is
	// only a ranking. The two are deliberately separate: a Mac can run both an
	// MLX build and a llama.cpp build, so nothing is filtered, yet the native
	// accelerated runtime should still be installed even when the GGUF build is
	// the larger download.
	//
	// An empty list ranks every backend equally, which reduces selection to
	// ordering by size alone. That is the right default for a caller with no
	// view of the hardware, and it is what every host looked like before
	// preference existed.
	EnginePreference []string
	// ServingFeaturePreference lists the SERVING FEATURES to prefer, best first,
	// as system.ServingFeaturePreferenceTokens reports them (currently
	// ["dflash", "mtp"]). A token matches when it equals one of the variant's
	// declared TAGS, or failing that when it equals a whole SEGMENT of its
	// ENTRY NAME, splitting on every non-alphanumeric run.
	//
	// A third vocabulary, matched against a third thing. It is neither a build
	// tag nor an engine name: these name a way of serving the same weights
	// faster. A gallery tag is the declaration and takes precedence; the entry
	// name remains a fallback for entries nobody has tagged.
	// pkg/system/capabilities.go documents all three tables together and
	// justifies why the name half matches segments rather than substrings.
	//
	// An empty list ranks every build equally on this axis, which is what every
	// host looked like before serving features were ranked at all.
	ServingFeaturePreference []string
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

// preferenceRank scores a backend against this host's preferred engine order,
// lower being better.
//
// It walks EnginePreference generically and names no engine and no capability:
// everything a new runtime needs is expressed by the table in pkg/system, so
// adding one never reaches this function.
//
// A backend matching no token scores just below the least preferred known one.
// It is never dropped, and a field where nothing matches scores uniformly, so
// an unrecognised backend, an unrecognised capability and an absent preference
// list all degrade to the same predictable place: ordering by size alone.
func (e ResolveEnv) preferenceRank(backend string) int {
	name := strings.ToLower(backend)
	return preferenceIndex(e.EnginePreference, func(token string) bool {
		return strings.Contains(name, token)
	})
}

// servingFeatureRank scores a variant against the preferred serving feature
// order, lower being better.
//
// It names no feature, exactly as preferenceRank names no engine: the ordered
// list comes from pkg/system, so teaching LocalAI about a new serving feature
// is a one-line edit to that table and never reaches this file.
//
// Two signals, either of which is enough. A declared TAG is the authoritative
// one and is compared whole, because a tag is a deliberate statement rather
// than free text: an author who writes "mtp" in a tag list means the feature,
// so the substring risk that segment matching exists to avoid does not arise
// there. The ENTRY NAME stays as a fallback, matched by whole segment as
// before, so an entry nobody has tagged yet ranks exactly the way it did
// before tags were read at all. Tags-only would have been a regression on the
// day it shipped, since most MTP entries carried no tag.
//
// A build declaring no feature by either route is a plain build and scores
// just below the least preferred known one, which is the whole point: whenever
// a faster way to serve the same weights survived the filters, it outranks the
// plain build.
func (e ResolveEnv) servingFeatureRank(o VariantOption) int {
	segments := nameSegments(o.Variant.Model)
	tags := lowercased(o.Tags)
	return preferenceIndex(e.ServingFeaturePreference, func(token string) bool {
		return slices.Contains(tags, token) || slices.Contains(segments, token)
	})
}

// lowercased folds a tag list for comparison, so a gallery author writing
// "MTP" declares the same feature as one writing "mtp". Names are already
// folded by nameSegments, and the two signals must not disagree about case.
func lowercased(tags []string) []string {
	if len(tags) == 0 {
		return nil
	}
	out := make([]string, 0, len(tags))
	for _, t := range tags {
		out = append(out, strings.ToLower(strings.TrimSpace(t)))
	}
	return out
}

// preferenceIndex returns the position of the first token `matches` accepts.
//
// A subject matching no token scores just below the least preferred known one.
// It is never dropped, and a list where nothing matches scores uniformly, so an
// unrecognised subject and an absent preference list degrade to the same
// predictable place: this axis stops discriminating and the next key decides.
func preferenceIndex(tokens []string, matches func(token string) bool) int {
	if len(tokens) == 0 {
		return 0
	}
	for i, token := range tokens {
		if token != "" && matches(token) {
			return i
		}
	}
	return len(tokens)
}

// nameSegments splits a gallery entry name into its lowercased alphanumeric
// runs, so "qwen3.6-27b-nvfp4-mtp" yields ["qwen3", "6", "27b", "nvfp4", "mtp"].
//
// Whole-segment matching is what keeps a short marker from matching inside an
// unrelated word. Entry names are author-supplied free text, unlike the engine
// names preferenceRank matches as substrings, and those are a closed vocabulary
// LocalAI defines itself.
func nameSegments(name string) []string {
	return strings.FieldsFunc(strings.ToLower(name), func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsDigit(r)
	})
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
//  5. The survivors are ranked and the best one wins: fit tier first (rankOf
//     below), then this host's engine preference, then the serving feature
//     preference, then size. Both preferences beat size deliberately, so a Mac
//     takes an MLX build over a larger GGUF one, and a host that can hold a
//     speculative-decoding build of the same weights takes it over the plain
//     one.
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
		option     VariantOption
		memory     uint64
		rank       int
		preference int
		feature    int
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

		survivors = append(survivors, ranked{
			option:     o,
			memory:     memory,
			rank:       rankOf(o, env),
			preference: env.preferenceRank(o.Backend),
			feature:    env.servingFeatureRank(o),
		})
	}

	if len(survivors) == 0 {
		return VariantSelection{}, fmt.Errorf(
			"%w: %s of memory available; variants: %s",
			ErrNoVariantMatch, humanBytes(env.AvailableMemory), strings.Join(reasons, "; "),
		)
	}

	// Four keys, in this order, and the order is the whole design:
	//
	//  1. rank, the fit tier. Fit is a fact about whether the host can hold the
	//     build at all, so nothing outranks it.
	//  2. preference, the host's runtime order. Among builds the host can
	//     equally hold, the one on the more native runtime wins even when it is
	//     the smaller download. A Mac given an MLX build and a larger llama.cpp
	//     build should run MLX; ranking by size first would install the GGUF and
	//     leave the accelerated build unused. Do NOT move this below size.
	//  3. feature, the serving feature order. Among builds on an equally
	//     preferred runtime, one that speculates or predicts several tokens per
	//     step beats the plain build of the same weights, because it answers
	//     faster for the same output.
	//
	//     Engine outranks this deliberately. A serving feature makes the right
	//     engine faster; it does not make a wrong engine right. Wearing the
	//     wrong engine costs the whole GPU, whereas a plain build on the native
	//     engine is merely slower than it could be, so a DFlash build on the
	//     portable engine must not beat a plain build on the engine this host
	//     actually serves well.
	//  4. memory, largest first, because among builds on equally preferred
	//     runtimes with the same serving feature a bigger build is a higher
	//     quality quantization of the same model. Neither preference key may
	//     flatten this: two plain builds on one backend are still ordered by
	//     size.
	//
	// Stable so that options equal on all four keep their authored order, which
	// is the only thing order still decides.
	sort.SliceStable(survivors, func(i, j int) bool {
		if survivors[i].rank != survivors[j].rank {
			return survivors[i].rank < survivors[j].rank
		}
		if survivors[i].preference != survivors[j].preference {
			return survivors[i].preference < survivors[j].preference
		}
		if survivors[i].feature != survivors[j].feature {
			return survivors[i].feature < survivors[j].feature
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

// Fit tiers, best first. Within a tier the host's preferred runtime wins, then
// the preferred serving feature, then the larger footprint; see the sort in
// SelectVariant.
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
