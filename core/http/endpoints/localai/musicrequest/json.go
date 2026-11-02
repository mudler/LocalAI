// SPDX-License-Identifier: MIT
package musicrequest

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"math"
	"mime"
	"net/http"
	"strconv"
	"strings"
	"unicode/utf8"
)

func mediaType(r *http.Request, want string) error {
	value, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || value != want {
		return failure(415, "unsupported_media_type")
	}
	return nil
}
func object(data []byte) (map[string]json.RawMessage, error) {
	if !utf8.Valid(data) {
		return nil, failure(400, "invalid_utf8")
	}
	if !validUnicodeEscapes(data) {
		return nil, failure(400, "invalid_unicode")
	}
	d := json.NewDecoder(bytes.NewReader(data))
	token, err := d.Token()
	if err != nil || token != json.Delim('{') {
		return nil, failure(400, "invalid_object")
	}
	result := map[string]json.RawMessage{}
	for d.More() {
		key, err := d.Token()
		if err != nil {
			return nil, failure(400, "malformed_json")
		}
		name, ok := key.(string)
		if !ok {
			return nil, failure(400, "malformed_json")
		}
		if _, ok := result[name]; ok {
			return nil, failure(400, "duplicate_field")
		}
		var value json.RawMessage
		if d.Decode(&value) != nil {
			return nil, failure(400, "malformed_json")
		}
		result[name] = value
	}
	if _, err = d.Token(); err != nil {
		return nil, failure(400, "malformed_json")
	}
	if _, err = d.Token(); err != io.EOF {
		return nil, failure(400, "trailing_data")
	}
	return result, nil
}
func scalarString(v json.RawMessage) (string, error) {
	var value string
	if len(v) == 0 || v[0] != '"' || json.Unmarshal(v, &value) != nil {
		return "", failure(400, "invalid_type")
	}
	if !utf8.ValidString(value) {
		return "", failure(400, "invalid_utf8")
	}
	return value, nil
}
func stringValue(v json.RawMessage) string { s, _ := scalarString(v); return s }
func text(fields map[string]json.RawMessage, key string, limit int, blank bool) (string, error) {
	v, ok := fields[key]
	if !ok {
		return "", failure(400, "missing_field")
	}
	value, err := scalarString(v)
	if err != nil {
		return "", err
	}
	if len(value) > limit {
		return "", failure(413, "size_limit")
	}
	if !blank && strings.TrimSpace(value) == "" {
		return "", failure(422, "blank_field")
	}
	return value, nil
}
func (p *Parser) generation(fields map[string]json.RawMessage, withABC bool) (Generation, error) {
	var g Generation
	for key := range fields {
		switch key {
		case "model", "style", "lyrics", "abc", "params":
		default:
			return g, failure(400, "unknown_field")
		}
	}
	var err error
	if g.Model, err = text(fields, "model", 128, false); err != nil {
		return Generation{}, err
	}
	if g.Style, err = text(fields, "style", 4096, false); err != nil {
		return Generation{}, err
	}
	if g.Lyrics, err = text(fields, "lyrics", 16384, true); err != nil {
		return Generation{}, err
	}
	if _, ok := fields["abc"]; ok {
		abc, e := text(fields, "abc", 256<<10, false)
		if e != nil {
			return Generation{}, e
		}
		if strings.ContainsRune(abc, 0) || p.validateABC(abc) != nil {
			return Generation{}, failure(422, "invalid_abc")
		}
		g.ABC = &abc
		withABC = true
	}
	g.Params, err = parameters(fields["params"], withABC)
	if err != nil {
		return Generation{}, err
	}
	return g, nil
}
func parameters(data json.RawMessage, withABC bool) (Params, error) {
	result := Params{COT: "full", Seed: 0, Guidance: 3, Steps: 32}
	if withABC {
		result.COT = "melody"
	}
	if data == nil {
		return result, nil
	}
	if len(data) > 2048 {
		return Params{}, failure(413, "size_limit")
	}
	values, err := object(data)
	if err != nil {
		return Params{}, err
	}
	for key, v := range values {
		switch key {
		case "cot":
			value, e := scalarString(v)
			if e != nil {
				return Params{}, e
			}
			if value != "off" && value != "melody" && value != "full" || withABC && value == "off" {
				return Params{}, failure(422, "invalid_params")
			}
			result.COT = value
		case "seed", "num_inference_steps":
			n, parseErr := strconv.ParseInt(string(v), 10, 64)
			if parseErr != nil {
				if errors.Is(parseErr, strconv.ErrRange) {
					return Params{}, failure(422, "invalid_params")
				}
				return Params{}, failure(400, "invalid_type")
			}
			if key == "seed" {
				if n < 0 || n > 2147483647 {
					return Params{}, failure(422, "invalid_params")
				}
				result.Seed = n
			} else {
				if n < 1 || n > 100 {
					return Params{}, failure(422, "invalid_params")
				}
				result.Steps = int(n)
			}
		case "guidance_scale":
			var n float64
			if string(v) == "null" || len(v) == 0 || v[0] == '"' || v[0] == '{' || v[0] == '[' || string(v) == "true" || string(v) == "false" {
				return Params{}, failure(400, "invalid_type")
			}
			if json.Unmarshal(v, &n) != nil || math.IsNaN(n) || math.IsInf(n, 0) || n < 0 || n > 20 {
				return Params{}, failure(422, "invalid_params")
			}
			result.Guidance = n
		default:
			return Params{}, failure(422, "unknown_param")
		}
	}
	return result, nil
}

// encoding/json replaces unpaired UTF-16 escapes with U+FFFD. Check the raw
// strings (including keys and nested values) before any lossy decoding occurs.
// Literal U+FFFD and escaped backslashes remain ordinary accepted data.
func validUnicodeEscapes(data []byte) bool {
	inString := false
	for i := 0; i < len(data); i++ {
		if data[i] == '"' {
			inString = !inString
			continue
		}
		if !inString || data[i] != '\\' {
			continue
		}
		i++
		if i >= len(data) {
			return false
		}
		if data[i] != 'u' {
			continue
		}
		value, ok := unicodeEscape(data, i)
		if !ok {
			return false
		}
		i += 4
		if value >= 0xdc00 && value <= 0xdfff {
			return false
		}
		if value < 0xd800 || value > 0xdbff {
			continue
		}
		if i+2 >= len(data) || data[i+1] != '\\' || data[i+2] != 'u' {
			return false
		}
		low, ok := unicodeEscape(data, i+2)
		if !ok || low < 0xdc00 || low > 0xdfff {
			return false
		}
		i += 6
	}
	return true
}
func unicodeEscape(data []byte, u int) (uint64, bool) {
	if u+5 > len(data) {
		return 0, false
	}
	value, err := strconv.ParseUint(string(data[u+1:u+5]), 16, 16)
	return value, err == nil
}
