// SPDX-License-Identifier: MIT
package executionbridge

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"strconv"
	"strings"
	"unicode/utf8"
)

const (
	textCodec        = "host-execution-text"
	textCodecVersion = "1" // Corresponds to durable admission Codec == 1.
	maxTextBytes     = 1048576
	maxEnvelopeBytes = 6292480
)

var errTextCodec = errors.New("execution bridge codec: invalid or unavailable")

// textProjection is owned, opaque data, NOT an authorization capability. The
// closure also prevents fmt's nested/private-field fallback from leaking text.
// Disclosure requires an explicit accessor. No unsafe-reflection guarantee.
type textProjection struct {
	redacted
	open func() (string, string, string, string, string)
}

func (p textProjection) Text() string {
	if p.open == nil {
		return ""
	}
	text, _, _, _, _ := p.open()
	return text
}
func (p textProjection) Envelope() []byte {
	if p.open == nil {
		return nil
	}
	_, wire, _, _, _ := p.open()
	return []byte(wire)
}
func (p textProjection) Digests() (admission, provenance, projection string) {
	if p.open == nil {
		return "", "", ""
	}
	_, _, admission, provenance, projection = p.open()
	return
}
func ownText(text, wire, admission, provenance, projection string) textProjection {
	text, wire = strings.Clone(text), strings.Clone(wire)
	admission, provenance, projection = strings.Clone(admission), strings.Clone(provenance), strings.Clone(projection)
	return textProjection{open: func() (string, string, string, string, string) { return text, wire, admission, provenance, projection }}
}

func codecDigest(s string) bool {
	if len(s) != 64 {
		return false
	}
	for i := range s {
		if !(s[i] >= '0' && s[i] <= '9' || s[i] >= 'a' && s[i] <= 'f') {
			return false
		}
	}
	return true
}

// validateFreshText must precede the future shell's projection callback. The
// encoder independently checks the projected output; neither calls a supplier
// or authorizes release. Native Data (including typed nil) is a shell concern.
func validateFreshText(text string) error {
	if len(text) > maxTextBytes || !utf8.ValidString(text) {
		return errTextCodec
	}
	return nil
}

// Exact v1 schema: six required, case-sensitive string fields, no extensions.
// Admission is ownership.ExecutionAdmission.Digest(), covering the immutable
// admission and broker binding; provenance and projection are trusted host
// expected digests. Equality checks provide binding consistency, NOT message
// authentication, tamper-proof storage, or release permission.
type textEnvelope struct {
	Codec      string `json:"codec"`
	Version    string `json:"version"`
	Admission  string `json:"admission"`
	Provenance string `json:"provenance"`
	Projection string `json:"projection"`
	Text       string `json:"text"`
}

// encodeAuthorizedText accepts ONLY already-authorized projected text. It never
// sniffs envelope-looking input and is not an allow-by-default sanitizer.
func encodeAuthorizedText(text, admission, provenance, projection string) (textProjection, error) {
	if validateFreshText(text) != nil || !codecDigest(admission) || !codecDigest(provenance) || !codecDigest(projection) {
		return textProjection{}, errTextCodec
	}
	b, err := json.Marshal(textEnvelope{textCodec, textCodecVersion, admission, provenance, projection, text})
	if err != nil || len(b) > maxEnvelopeBytes {
		return textProjection{}, errTextCodec
	}
	return ownText(text, string(b), admission, provenance, projection), nil
}

// strictJSONUnicode complements encoding/json, which otherwise replaces invalid
// UTF8 and unpaired UTF16 surrogates silently. JSON syntax is validated first so
// escape lengths and hex digits are known valid before this bounded scan.
func strictJSONUnicode(b []byte) bool {
	if !utf8.Valid(b) || !json.Valid(b) {
		return false
	}
	quoted := false
	for i := 0; i < len(b); i++ {
		if b[i] == '"' {
			quoted = !quoted
			continue
		}
		if !quoted || b[i] != '\\' {
			continue
		}
		i++
		if b[i] != 'u' {
			continue
		}
		n, _ := strconv.ParseUint(string(b[i+1:i+5]), 16, 16)
		i += 4
		if n >= 0xdc00 && n <= 0xdfff {
			return false
		}
		if n >= 0xd800 && n <= 0xdbff {
			if i+6 >= len(b) || b[i+1] != '\\' || b[i+2] != 'u' {
				return false
			}
			low, err := strconv.ParseUint(string(b[i+3:i+7]), 16, 16)
			if err != nil || low < 0xdc00 || low > 0xdfff {
				return false
			}
			i += 6
		}
	}
	return true
}

// decodeTextEnvelope validates replay data, never re-encodes it. A future replay
// sanitizer must separately reauthorize release and return these SAME bytes.
// Size is checked before any parsing; all parser errors remain private.
func decodeTextEnvelope(b []byte, admission, provenance, projection string) (textProjection, error) {
	if len(b) > maxEnvelopeBytes || !codecDigest(admission) || !codecDigest(provenance) || !codecDigest(projection) || !strictJSONUnicode(b) {
		return textProjection{}, errTextCodec
	}
	d := json.NewDecoder(bytes.NewReader(b))
	start, err := d.Token()
	if err != nil || start != json.Delim('{') {
		return textProjection{}, errTextCodec
	}
	fields := make(map[string]string, 6)
	for d.More() {
		key, err := d.Token()
		if err != nil {
			return textProjection{}, errTextCodec
		}
		name, ok := key.(string)
		if !ok {
			return textProjection{}, errTextCodec
		}
		switch name {
		case "codec", "version", "admission", "provenance", "projection", "text":
		default:
			return textProjection{}, errTextCodec
		}
		if _, exists := fields[name]; exists {
			return textProjection{}, errTextCodec
		}
		value, err := d.Token()
		if err != nil {
			return textProjection{}, errTextCodec
		}
		text, ok := value.(string)
		if !ok {
			return textProjection{}, errTextCodec
		}
		fields[name] = text
	}
	end, err := d.Token()
	if err != nil || end != json.Delim('}') {
		return textProjection{}, errTextCodec
	}
	if _, err = d.Token(); err != io.EOF {
		return textProjection{}, errTextCodec
	}
	if len(fields) != 6 || fields["codec"] != textCodec || fields["version"] != textCodecVersion || fields["admission"] != admission || fields["provenance"] != provenance || fields["projection"] != projection || validateFreshText(fields["text"]) != nil {
		return textProjection{}, errTextCodec
	}
	return ownText(fields["text"], string(b), admission, provenance, projection), nil
}
