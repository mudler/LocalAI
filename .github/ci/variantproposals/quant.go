package main

import (
	"regexp"
	"strconv"
	"strings"
)

// Quantization and precision markers that distinguish one build of a set of
// weights from another build of the same weights. Stripping them from a name
// is what lets the proposer notice that two entries are the same model.
//
// qat and apex are in this list on a maintainer ruling: they are quantization
// techniques applied to published weights, not separate weights. Names that use
// "apex" to mean a finetune are handled by the rejection ledger instead, because
// no amount of pattern matching can tell the two uses apart.
const quantAlternation = `q[2-8](?:_[0-9a-z]+)*|pq[2-8](?:_[0-9a-z]+)*|iq[1-9][0-9a-z]*(?:_[0-9a-z]+)*|i1|` +
	`f16|f32|bf16|fp16|fp32|fp8|fp4|nvfp4|mxfp4(?:_moe)*|awq|gptq|qat|apex|gguf|ggml|[0-9]+bit|g[0-9]+`

// quantSegment matches a whole hyphen-delimited segment of an entry name.
// Names separate their parts with "-" and keep quantization tokens internally
// joined with "_", so a segment is the right unit here: "q4_k_m" arrives whole.
var quantSegment = regexp.MustCompile(`^(?:` + quantAlternation + `)$`)

// quantFileSuffix matches a trailing quantization token in a weight filename.
// Filenames mix "-", "_" and "." as separators, so unlike entry names they
// cannot be split into segments up front without tearing "Q4_K_M" apart.
var quantFileSuffix = regexp.MustCompile(`(?i)[-_.](?:` + quantAlternation + `)$`)

var weightExtension = regexp.MustCompile(`(?i)\.(gguf|ggml|safetensors|bin|pt|pth|onnx)$`)

// IsQuantToken reports whether a single name segment is a quantization or
// precision marker rather than part of the model's identity.
func IsQuantToken(segment string) bool {
	return quantSegment.MatchString(strings.ToLower(segment))
}

// NameStem reduces an entry name to the identity it shares with its alternative
// builds: the config suffix after ":" is dropped, then trailing quantization
// segments are stripped.
//
// It implements the first two grouping signals together because they answer the
// same question. "foo:q8_0" and "foo-q8_0" are both alternative builds of "foo",
// and the caller that needs to report which convention was used can compare the
// name against the stem itself.
//
// At least one segment always survives, so a name made entirely of quantization
// tokens does not collapse to the empty stem and swallow every other such name.
func NameStem(name string) string {
	base := strings.ToLower(strings.TrimSpace(name))
	if i := strings.Index(base, ":"); i >= 0 {
		base = base[:i]
	}
	segments := strings.Split(base, "-")
	for len(segments) > 1 && quantSegment.MatchString(segments[len(segments)-1]) {
		segments = segments[:len(segments)-1]
	}
	return strings.Join(segments, "-")
}

// HasConfigSuffix reports whether a name uses the ":" convention for naming a
// config variant of another entry.
func HasConfigSuffix(name string) bool {
	return strings.Contains(name, ":")
}

// FileStem reduces a weight filename to the identity shared by its other
// quantizations: directories, extension and trailing quantization tokens go.
//
// This is the third grouping signal. It is the one that has misfired before, so
// callers must filter auxiliary files out before handing a filename here: a
// shared text encoder is not evidence of shared weights.
func FileStem(filename string) string {
	base := filename
	if i := strings.LastIndex(base, "/"); i >= 0 {
		base = base[i+1:]
	}
	base = weightExtension.ReplaceAllString(base, "")
	for {
		stripped := quantFileSuffix.ReplaceAllString(base, "")
		if stripped == base {
			break
		}
		base = stripped
	}
	return strings.ToLower(base)
}

// bitsPerWeight ranks quantization tokens so the smallest build of a family can
// be identified when no bare-named entry exists to be the parent.
//
// The figures are nominal bits per weight, not measured file sizes. Ranking is
// all that is asked of them, and a nominal figure is available from the name
// alone without downloading anything.
func bitsPerWeight(token string) (int, bool) {
	t := strings.ToLower(token)
	switch {
	case t == "i1":
		return 1, true
	case strings.HasPrefix(t, "nvfp4"), strings.HasPrefix(t, "mxfp4"), t == "fp4":
		return 4, true
	case t == "fp8":
		return 8, true
	case t == "f16", t == "bf16", t == "fp16":
		return 16, true
	case t == "f32", t == "fp32":
		return 32, true
	case t == "awq", t == "gptq":
		return 4, true
	}
	if m := regexp.MustCompile(`^p?q([1-9])`).FindStringSubmatch(t); m != nil {
		n, _ := strconv.Atoi(m[1])
		return n, true
	}
	if m := regexp.MustCompile(`^iq([1-9])`).FindStringSubmatch(t); m != nil {
		n, _ := strconv.Atoi(m[1])
		return n, true
	}
	if m := regexp.MustCompile(`^([0-9]+)bit$`).FindStringSubmatch(t); m != nil {
		n, _ := strconv.Atoi(m[1])
		return n, true
	}
	return 0, false
}

// unknownWidth sorts after every recognised quantization so an entry whose
// build cannot be read from its filename never wins the "smallest build" tie
// break by accident.
const unknownWidth = 1 << 10

// BuildWidth reports the nominal bits per weight of the build a filename holds.
// An unreadable filename gets unknownWidth.
func BuildWidth(filename string) int {
	base := filename
	if i := strings.LastIndex(base, "/"); i >= 0 {
		base = base[i+1:]
	}
	base = weightExtension.ReplaceAllString(base, "")
	best := unknownWidth
	for {
		m := quantFileSuffix.FindString(base)
		if m == "" {
			break
		}
		if bits, ok := bitsPerWeight(m[1:]); ok && bits < best {
			best = bits
		}
		base = base[:len(base)-len(m)]
	}
	return best
}
