// SPDX-License-Identifier: MIT
package musicrequest

import (
	"encoding/json"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"unicode/utf8"
)

func (p *Parser) multipart(r *http.Request, cover bool) (fields map[string]json.RawMessage, audio *OwnedUpload, err error) {
	if err = mediaType(r, "multipart/form-data"); err != nil {
		return
	}
	_, params, _ := mime.ParseMediaType(r.Header.Get("Content-Type"))
	boundary := params["boundary"]
	if boundary == "" {
		err = failure(400, "malformed_multipart")
		return
	}
	body, e := boundedBody(r, MultipartLimit)
	if e != nil {
		err = e
		return
	}
	// Root storage retains all partial Create allocations until release ACK. This
	// defer also covers errors returned alongside a partially allocated resource.
	defer func() {
		if err != nil {
			release(audio)
			audio = nil
			fields = nil
		}
	}()
	reader := multipart.NewReader(body, boundary)
	fields = map[string]json.RawMessage{}
	seen := map[string]bool{}
	for count := 0; ; count++ {
		part, e := reader.NextRawPart()
		if e == io.EOF {
			break
		}
		if e != nil {
			err = readFailure(e)
			return
		}
		if count >= 8 {
			err = failure(413, "part_limit")
			return
		}
		if len(part.Header.Values("Content-Disposition")) != 1 {
			err = failure(400, "malformed_multipart")
			return
		}
		kind, disposition, e := mime.ParseMediaType(part.Header.Get("Content-Disposition"))
		if e != nil || kind != "form-data" {
			err = failure(400, "malformed_multipart")
			return
		}
		name := disposition["name"]
		if seen[name] {
			err = failure(400, "duplicate_field")
			return
		}
		seen[name] = true
		_, file := disposition["filename"]
		if part.Header.Get("Content-Transfer-Encoding") != "" {
			err = failure(400, "malformed_multipart")
			return
		}
		if name == "audio" {
			if !file {
				err = failure(400, "invalid_audio_part")
				return
			}
			if p.uploads == nil {
				err = failure(503, "storage_unavailable")
				return
			}
			audio, e = p.uploads.Create(r.Context())
			if e != nil {
				err = failure(503, "storage_unavailable")
				return
			}
			if audio == nil || audio.Writer == nil || audio.Cleanup == nil || audio.Ref == "" {
				err = failure(500, "storage_contract")
				return
			}
			// Copy at most the audio ceiling; the extra byte is probed, never written.
			writer := &uploadWriter{Writer: audio.Writer}
			n, e := io.CopyBuffer(writer, io.LimitReader(part, AudioLimit), make([]byte, 32<<10))
			if e != nil {
				if writer.err != nil {
					err = failure(500, "upload_failed")
				} else {
					err = readFailure(e)
				}
				return
			}
			var probe [1]byte
			extra, e := part.Read(probe[:])
			if extra != 0 {
				err = failure(413, "size_limit")
				return
			}
			if e != nil && e != io.EOF {
				err = readFailure(e)
				return
			}
			if n == 0 {
				err = failure(422, "empty_audio")
				return
			}
			if e = audio.Writer.Close(); e != nil {
				err = failure(500, "upload_failed")
				return
			}
			continue
		}
		limit := 0
		switch name {
		case "model":
			limit = 128
		case "transcription_model":
			if cover {
				limit = 128
			}
		case "style":
			if cover {
				limit = 4096
			}
		case "lyrics":
			if cover {
				limit = 16384
			}
		case "params":
			if cover {
				limit = 2048
			}
		}
		if limit == 0 {
			err = failure(400, "unknown_field")
			return
		}
		if file {
			err = failure(400, "invalid_type")
			return
		}
		data, e := io.ReadAll(io.LimitReader(part, int64(limit)+1))
		if e != nil {
			err = readFailure(e)
			return
		}
		if len(data) > limit {
			err = failure(413, "size_limit")
			return
		}
		if !utf8.Valid(data) {
			err = failure(400, "invalid_utf8")
			return
		}
		if name == "params" {
			fields[name] = data
		} else {
			fields[name], _ = json.Marshal(string(data))
		}
	}
	// Include epilogue in the total ceiling, rather than trusting Content-Length.
	if _, e = io.Copy(io.Discard, body); e != nil {
		err = readFailure(e)
		return
	}
	if !seen["audio"] {
		err = failure(400, "missing_audio")
		return
	}
	if cover {
		if _, e = text(fields, "transcription_model", 128, false); e != nil {
			err = e
			return
		}
	} else {
		if _, e = text(fields, "model", 128, false); e != nil {
			err = e
			return
		}
	}
	if e = r.Context().Err(); e != nil {
		err = readFailure(e)
		return
	}
	return
}

// Preserve the distinction between an invalid transport and a failed local sink.
type uploadWriter struct {
	io.Writer
	err error
}

func (w *uploadWriter) Write(b []byte) (int, error) {
	n, err := w.Writer.Write(b)
	if err == nil && n != len(b) {
		err = io.ErrShortWrite
	}
	w.err = err
	return n, err
}
