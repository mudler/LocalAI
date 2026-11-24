package gallery

import (
	"path"
	"regexp"
	"strings"
)

// quantizationToken matches one filename segment that names a weight format.
//
// The alternatives, in order: the GGUF k-quant/legacy family with any number of
// trailing qualifiers (`q8_0`, `q4_k_m`, `iq2_xxs`, `pq2_0`, `q2_0_g128`), the
// float formats (`f16`, `fp16`, `bf16`, `f32`) including the vendor 4-bit ones
// (`nvfp4`, `mxfp4`), and the bit-count spellings the MLX and AWQ ecosystems
// use (`4bit`, `int8`).
//
// It is anchored at both ends because it is applied to an already-split
// segment. Matching mid-string would let a repo or model name containing a
// quant-shaped substring be reported as the quantization.
var quantizationToken = regexp.MustCompile(`^(?:p?i?q[0-9]+(?:_[0-9a-z]+)*|f(?:p)?(?:8|16|32)|bf16|(?:nv|mx)fp[0-9]+|int[0-9]+|[0-9]+bit)$`)

// weightExtensions are stripped before a filename is split into segments, so a
// model whose quantization is the last thing in its name (`...-Q8_0.gguf`) is
// not read as the segment `gguf`.
var weightExtensions = []string{".gguf", ".safetensors", ".bin", ".pt", ".pth", ".onnx"}

// quantizationFromFilename reports the weight format named in a model
// filename, uppercased, or "" when the name does not declare one.
//
// Segments are scanned from the END because that is where the qualifier sits by
// near-universal convention (`Ternary-Bonsai-27B-Q2_g64.gguf`), and because a
// repo path may itself carry a quant-shaped segment that describes the
// repository rather than this file.
//
// Only `-` and `.` split segments; `_` deliberately does not, because it is the
// separator INSIDE a quant token (`Q4_K_M`) and splitting on it would report
// `Q4` for a build that is not Q4.
//
// Two passes, in decreasing order of confidence. A whole segment naming a
// format is the unambiguous case and always wins. Only when no segment does is
// the `_`-delimited tail of a segment considered, which catches the authoring
// style that runs the format into the model name (`gemma-4-E2B_q4_0-it.gguf`,
// a real and populous gallery family). Running the loose pass second rather
// than inline keeps a precise match from ever losing to a fuzzy one further
// right in the name.
//
// The result is uppercased so one gallery does not show `Q2_g64` next to
// `Q2_G64` for what is the same format spelled two ways by two authors.
func quantizationFromFilename(filename string) string {
	if filename == "" {
		return ""
	}

	base := path.Base(strings.TrimSpace(filename))
	lower := strings.ToLower(base)
	for _, ext := range weightExtensions {
		if strings.HasSuffix(lower, ext) {
			base = base[:len(base)-len(ext)]
			break
		}
	}

	segments := strings.FieldsFunc(base, func(r rune) bool { return r == '-' || r == '.' })

	for i := len(segments) - 1; i >= 0; i-- {
		if quantizationToken.MatchString(strings.ToLower(segments[i])) {
			return strings.ToUpper(segments[i])
		}
	}

	for i := len(segments) - 1; i >= 0; i-- {
		if tail := quantizationTail(segments[i]); tail != "" {
			return tail
		}
	}
	return ""
}

// quantizationTail reports a weight format run into the tail of a segment,
// uppercased, or "" when there is none.
//
// It walks the `_`-delimited tails from the LONGEST first, so `e2b_q4_0` yields
// `Q4_0` rather than the `0` a shortest-first walk would reach. The full
// segment is not retried here; the caller has already ruled it out.
func quantizationTail(segment string) string {
	for i, r := range segment {
		if r != '_' {
			continue
		}
		tail := segment[i+1:]
		if quantizationToken.MatchString(strings.ToLower(tail)) {
			return strings.ToUpper(tail)
		}
	}
	return ""
}

// quantizationOfEntry reports the weight format a gallery entry installs.
//
// `overrides.parameters.model` is preferred over the file list because it names
// the file the backend is actually pointed at. An entry routinely ships more
// than one weight file (a vision tower alongside the language model, for
// instance), and those companions carry their own, different quantization: the
// Bonsai entries pair a Q2_0 model with a Q8_0 mmproj, so reading the file list
// first would report Q8_0 for a Q2_0 build.
//
// Returning "" is a normal outcome, not a failure. A backend served from a
// directory of safetensors, or an entry whose name simply does not encode a
// format, has no quantization to report and callers must render its absence
// rather than a guess.
func quantizationOfEntry(entry *GalleryModel) string {
	if entry == nil {
		return ""
	}

	if q := quantizationFromFilename(overrideModelParameter(entry)); q != "" {
		return q
	}

	// The file list is the fallback for entries that install a config_file
	// naming no model parameter of their own. First file wins: the language
	// model is listed first by convention, and there is nothing better to go on
	// once the authoritative pointer is absent.
	for _, f := range entry.AdditionalFiles {
		if q := quantizationFromFilename(f.Filename); q != "" {
			return q
		}
	}
	return ""
}

// overrideModelParameter digs `overrides.parameters.model` out of an entry.
//
// Overrides are free-form YAML decoded into map[string]any, so every level has
// to be type-asserted; a gallery author who writes a list or a scalar where a
// map belongs gets "" here rather than a panic in the listing handler.
func overrideModelParameter(entry *GalleryModel) string {
	parameters, ok := entry.Overrides["parameters"].(map[string]any)
	if !ok {
		return ""
	}
	model, ok := parameters["model"].(string)
	if !ok {
		return ""
	}
	return model
}
