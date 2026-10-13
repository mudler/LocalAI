// SPDX-License-Identifier: MIT
package gateway

import (
	"context"
	"io"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/mudler/LocalAI/pkg/addons/contract"
)

// ArtifactState is where a private file result is in its life.
type ArtifactState string

const (
	// ArtifactTransferring means the owner is still copying the bytes from the
	// addon. It is never downloadable.
	ArtifactTransferring ArtifactState = "transferring"
	// ArtifactComplete means every byte arrived and matched the declared size.
	ArtifactComplete ArtifactState = "complete"
	// ArtifactFailed means the transfer stopped before completion. Its partial
	// bytes are gone and it never becomes downloadable.
	ArtifactFailed ArtifactState = "failed"
	// ArtifactCancelled means the transfer was withdrawn.
	ArtifactCancelled ArtifactState = "cancelled"
)

// ArtifactResource is one source resource of an artifact.
type ArtifactResource struct {
	AddonID    string `json:"addonId"`
	ResourceID string `json:"resourceId"`
}

// Artifact is the public view of a private, temporary file result. It carries
// no storage path, no addon output handle and no credential.
type Artifact struct {
	// ID is the host-generated opaque identifier. It is the only handle a
	// client ever holds for the file.
	ID string `json:"id"`
	// Principal is the user who requested the result. Only this user can read
	// or download the artifact.
	Principal string `json:"principal"`
	// Sources are the resources the bytes were produced from. Every one of
	// them must still be readable by the principal at completion and at each
	// download.
	Sources []ArtifactResource `json:"sources"`
	// AddonID and Generation name the addon build that produced the result.
	AddonID    string `json:"addonId"`
	Generation uint64 `json:"generation"`
	// OperationID is the operation of the request that produced the output.
	OperationID string `json:"operationId"`
	// Name is the sanitized attachment name and MediaType the safe media type
	// the download is served with.
	Name      string `json:"name"`
	MediaType string `json:"mediaType"`
	// Size is the exact number of bytes of a complete artifact.
	Size  int64         `json:"size"`
	State ArtifactState `json:"state"`
	// Reason says why a transfer failed or was cancelled. It never carries
	// addon text.
	Reason    string    `json:"reason,omitempty"`
	CreatedAt time.Time `json:"createdAt"`
	// ExpiresAt is when the artifact stops being served and is removed.
	ExpiresAt time.Time `json:"expiresAt"`
}

// ArtifactProvider is the owner-held private artifact store. Only the
// designated owner implements it; other frontends reach it through the carrier.
type ArtifactProvider interface {
	// Serve authorizes the actor for the artifact and streams it to dst. begin
	// is called once, after authorization and before the first byte, with the
	// description the response headers are built from. It must not write.
	Serve(ctx context.Context, actor contract.Actor, id string, begin func(Artifact) error, dst io.Writer) error
	// Status returns the artifact of the actor. An unknown identifier and an
	// artifact of another principal give the same denial.
	Status(ctx context.Context, actor contract.Actor, id string) (Artifact, error)
	// Adopt replaces the addon output handles of a read result by private
	// artifact identifiers and starts the transfers. The addon handles never
	// leave the owner.
	Adopt(ctx context.Context, actor contract.Actor, request contract.Request, result contract.Result) (contract.Result, error)
}

// artifactCall is the body a frontend forwards for an artifact lookup.
type artifactCall struct {
	Op string `json:"op"`
	ID string `json:"id"`
}

const artifactStatus = "status"

// ArtifactStatus returns the artifact of the actor from the owner. It is
// read-only and answers the same denial for unknown and foreign identifiers.
func (g *Gateway) ArtifactStatus(ctx context.Context, a contract.Actor, id string) (Artifact, error) {
	if e := g.revalidate(ctx, a); e != nil {
		return Artifact{}, e
	}
	if g.Forward != nil {
		return g.Forward.artifactStatus(ctx, a, id)
	}
	return g.artifactStatusLocal(ctx, a, id)
}

func (g *Gateway) artifactStatusLocal(ctx context.Context, a contract.Actor, id string) (Artifact, error) {
	if g.Artifacts == nil {
		return Artifact{}, ErrForbidden
	}
	return g.Artifacts.Status(ctx, a, id)
}

// ContentDisposition is the RFC 6266 attachment header for a download name. It
// always says attachment, so a browser saves the bytes and never renders them.
// The plain filename is an ASCII fallback and filename* carries the UTF-8 name
// percent-encoded, so no byte of the name can end the quoted string or start
// another parameter.
func ContentDisposition(name string) string {
	var plain, encoded strings.Builder
	for _, r := range name {
		switch {
		case r == utf8.RuneError || r >= unicode.MaxASCII || r < ' ' || r == 0x7f:
			plain.WriteByte('_')
		case r == '"' || r == '\\' || r == '%' || r == ';' || r == '/' || r == ':':
			plain.WriteByte('_')
		default:
			plain.WriteRune(r)
		}
		var buf [utf8.UTFMax]byte
		for _, b := range buf[:utf8.EncodeRune(buf[:], r)] {
			if b >= 'a' && b <= 'z' || b >= 'A' && b <= 'Z' || b >= '0' && b <= '9' || strings.IndexByte("!#$&+-.^_`|~", b) >= 0 {
				encoded.WriteByte(b)
			} else {
				const hex = "0123456789ABCDEF"
				encoded.WriteByte('%')
				encoded.WriteByte(hex[b>>4])
				encoded.WriteByte(hex[b&15])
			}
		}
	}
	return `attachment; filename="` + plain.String() + `"; filename*=UTF-8''` + encoded.String()
}
