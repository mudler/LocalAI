// SPDX-License-Identifier: MIT
package assetpipeline

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/binary"
	"encoding/json"
	"errors"
	"image"
	_ "image/jpeg"
	_ "image/png"

	"github.com/google/uuid"
)

// Upload preserves original asset bytes in bounded chunks across the small
// host-operation envelope. Repeated chunks must match exactly, and finalization
// records the artifact reference in the same transaction as publication.
func (s *Store) Upload(ctx context.Context, resource, id, name string, mime ArtifactMIME, offset int, data []byte, final bool) (Artifact, error) {
	if len(name) > 160 {
		return Artifact{}, errors.New("asset name too long")
	}

	if _, err := uuid.Parse(id); err != nil {
		return Artifact{}, errors.New("invalid upload ID")
	}

	limit := 20 << 20
	if mime == MIMEGLB {
		limit = 128 << 20
	}
	if offset < 0 || offset > limit || len(data) > 512<<10 || len(data) > limit-offset {
		return Artifact{}, errors.New("asset upload exceeds its size limit")
	}
	if !mime.Valid() {
		return Artifact{}, errors.New("upload must be PNG, JPEG or GLB")
	}

	if _, err := s.Read(ctx, resource); err != nil {
		return Artifact{}, err
	}
	s.uploadMu.Lock()
	defer s.uploadMu.Unlock()

	var (
		body            []byte
		oldName, result string
		oldMime         ArtifactMIME
	)
	err := s.db.QueryRowContext(ctx, "SELECT name,mime,body,result FROM uploads WHERE resource=? AND id=?", resource, id).Scan(&oldName, &oldMime, &body, &result)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return Artifact{}, err
	}
	if err == nil && (oldName != name || oldMime != mime) {
		return Artifact{}, ErrConflict
	}
	if offset < len(body) {
		if offset+len(data) > len(body) || !bytes.Equal(body[offset:offset+len(data)], data) {
			return Artifact{}, ErrConflict
		}
	} else if offset == len(body) {
		if result != "" {
			return Artifact{}, ErrConflict
		}

		body = append(body, data...)
	} else {
		return Artifact{}, errors.New("upload chunk is out of order")
	}
	if result != "" {
		return s.Artifact(ctx, resource, result)
	}

	var otherBytes int64
	if err := s.db.QueryRowContext(ctx, "SELECT COALESCE(SUM(length(body)),0) FROM uploads WHERE resource=? AND id<>?", resource, id).Scan(&otherBytes); err != nil {
		return Artifact{}, err
	}
	if otherBytes+int64(len(body)) > 256<<20 {
		return Artifact{}, errors.New("asset upload storage quota of 256 MiB exceeded")
	}

	if _, err := s.db.ExecContext(ctx, "INSERT INTO uploads(resource,id,name,mime,body) VALUES(?,?,?,?,?) ON CONFLICT(resource,id) DO UPDATE SET body=excluded.body", resource, id, name, mime, body); err != nil {
		return Artifact{}, err
	}
	if !final {
		return Artifact{}, nil
	}
	if mime == MIMEGLB {
		if err := validateUploadedGLB(body); err != nil {
			return Artifact{}, err
		}

		return s.putArtifact(ctx, resource, name, mime, bytes.NewReader(body), id)
	}

	info, format, err := image.DecodeConfig(bytes.NewReader(body))
	if err != nil || info.Width < 1 || info.Height < 1 || int64(info.Width)*int64(info.Height) > 16_777_216 {
		return Artifact{}, errors.New("invalid image or image exceeds 16 megapixels")
	}
	if (format == "png" && mime != MIMEPNG) || (format == "jpeg" && mime != MIMEJPEG) {
		return Artifact{}, errors.New("image type does not match upload")
	}

	return s.putArtifact(ctx, resource, name, mime, bytes.NewReader(body), id)
}

// External resources would escape the pipeline's ownership boundary. Operation-
// specific mesh/skeleton checks remain with the selected backend.
func validateUploadedGLB(body []byte) error {
	invalid := errors.New("upload must be a valid self-contained GLB 2.0 file")
	if len(body) < 20 || string(body[:4]) != "glTF" || binary.LittleEndian.Uint32(body[4:]) != 2 || uint64(binary.LittleEndian.Uint32(body[8:])) != uint64(len(body)) {
		return invalid
	}

	offset, chunks := 12, 0
	for offset < len(body) {
		if len(body)-offset < 8 {
			return invalid
		}

		size := uint64(binary.LittleEndian.Uint32(body[offset:]))
		kind := binary.LittleEndian.Uint32(body[offset+4:])
		if size%4 != 0 || size > uint64(len(body)-offset-8) {
			return invalid
		}
		if chunks == 0 {
			if kind != 0x4e4f534a || size > 32<<20 {
				return invalid
			}

			var doc struct {
				Asset struct {
					Version string `json:"version"`
				} `json:"asset"`
				Buffers []struct {
					URI string `json:"uri"`
				} `json:"buffers"`
				Images []struct {
					URI string `json:"uri"`
				} `json:"images"`
			}
			if json.Unmarshal(body[offset+8:offset+8+int(size)], &doc) != nil || doc.Asset.Version != "2.0" {
				return invalid
			}

			for _, buffer := range doc.Buffers {
				if buffer.URI != "" {
					return invalid
				}
			}

			for _, image := range doc.Images {
				if image.URI != "" {
					return invalid
				}
			}
		} else if chunks > 1 || kind != 0x004e4942 {
			return invalid
		}
		offset += 8 + int(size)
		chunks++
	}

	return nil
}
