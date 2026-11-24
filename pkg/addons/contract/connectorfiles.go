// SPDX-License-Identifier: MIT
package contract

import (
	"fmt"
	"regexp"
)

// Limits of the files ability.
const (
	// MaxDeclaredFileBytes is the largest limit a kind may declare: 1 TiB.
	// The host ceiling and the owner's settings still apply.
	MaxDeclaredFileBytes int64 = 1 << 40
	// MaxFileTypes bounds the media types of one direction.
	MaxFileTypes = 32
	// DefaultFileCeilingBytes is the host ceiling of a file, 100 MB, when the
	// owner policy sets none.
	DefaultFileCeilingBytes int64 = 100 << 20
)

// FileAbility is what a connector kind can move in one direction.
type FileAbility struct {
	// MaxBytes is the largest file the platform lets the add-on move, in
	// bytes. The host uses it as an upper bound that the owner can lower. It
	// is not proof that a transfer works.
	MaxBytes int64 `json:"maxBytes"`
	// Types lists the media types the kind moves, such as application/pdf or
	// image/*. Empty means every type the host allows.
	Types []string `json:"types,omitempty"`
}

// ConnectorFiles is the manifest field files of a connector kind. The bytes of
// a file always travel by handle (ConnectorTransfer). A kind that declares none
// moves no files.
type ConnectorFiles struct {
	// Receive is the ability to read files that people send to the bot.
	Receive *FileAbility `json:"receive,omitempty"`
	// Send is the ability to upload files from the bot.
	Send *FileAbility `json:"send,omitempty"`
}

var mediaTypePattern = regexp.MustCompile(`^[a-z0-9][a-z0-9!#$&^_.+-]{0,63}/(\*|[a-z0-9][a-z0-9!#$&^_.+-]{0,63})$`)

func (a *FileAbility) validate(dir string) error {
	if a == nil {
		return nil
	}
	if a.MaxBytes < 1 || a.MaxBytes > MaxDeclaredFileBytes {
		return fmt.Errorf("files.%s.maxBytes must be 1 to %d", dir, MaxDeclaredFileBytes)
	}
	if len(a.Types) > MaxFileTypes {
		return fmt.Errorf("files.%s.types has more than %d entries", dir, MaxFileTypes)
	}
	seen := map[string]bool{}
	for _, t := range a.Types {
		if !mediaTypePattern.MatchString(t) || seen[t] {
			return fmt.Errorf("files.%s.types needs distinct media types such as application/pdf or image/*", dir)
		}
		seen[t] = true
	}
	return nil
}

func (f *ConnectorFiles) validate() error {
	if f == nil {
		return nil
	}
	if f.Receive == nil && f.Send == nil {
		return fmt.Errorf("files must declare receive, send or both")
	}
	if err := f.Receive.validate("receive"); err != nil {
		return err
	}
	return f.Send.validate("send")
}

// Host operations with which an add-on moves the bytes of a file by handle. A
// handle is issued by the host with one connector call (ConnectorRequest.Transfer),
// is single use, bound to that call, that add-on and that binding, and ends
// when the call ends. The add-on needs the operation prefix "connector." in
// its host policy.
const (
	// ConnectorFilePutOp writes the next chunk of an inbound file.
	ConnectorFilePutOp = "connector.file.put"
	// ConnectorFileGetOp reads the next chunk of an outbound file.
	ConnectorFileGetOp = "connector.file.get"
	// TransferChunkBytes is the largest chunk of data of one put or get. It
	// is far below the payload limit of a host operation after base64.
	TransferChunkBytes = 256 << 10
	// MaxTransferHandle bounds the length of a handle.
	MaxTransferHandle = 128
)

// ConnectorTransfer is the handle that the host gives to the add-on with the
// ops media (inbound, write only) and send_file (outbound, read only).
type ConnectorTransfer struct {
	// Handle is random and means nothing to the add-on.
	Handle string `json:"handle"`
	// MaxBytes is the largest inbound file the host takes, the effective
	// limit. The add-on should refuse a larger file before it downloads it.
	MaxBytes int64 `json:"maxBytes,omitempty"`
	// Name, MIME and Size describe the outbound file. Name is already safe.
	Name string `json:"name,omitempty"`
	MIME string `json:"mime,omitempty"`
	Size int64  `json:"size,omitempty"`
}

// ConnectorFilePut is the argument object of ConnectorFilePutOp. Offset must
// equal the bytes the host already has; when it does not, the host answers
// with Received and the add-on resumes there. Final ends the file.
type ConnectorFilePut struct {
	Handle string `json:"handle"`
	Offset int64  `json:"offset"`
	Data   []byte `json:"data,omitempty"`
	Final  bool   `json:"final,omitempty"`
}

// ConnectorFilePutResult is the answer of ConnectorFilePutOp.
type ConnectorFilePutResult struct {
	Received int64 `json:"received"`
}

// ConnectorFileGet is the argument object of ConnectorFileGetOp. Length is at
// most TransferChunkBytes.
type ConnectorFileGet struct {
	Handle string `json:"handle"`
	Offset int64  `json:"offset"`
	Length int64  `json:"length"`
}

// ConnectorFileGetResult is the answer of ConnectorFileGetOp. EOF is true
// when Data ends the file.
type ConnectorFileGetResult struct {
	Data []byte `json:"data,omitempty"`
	EOF  bool   `json:"eof,omitempty"`
}

// ConnectorReceived is what the add-on answers to the op media when it wrote
// the file by handle: Name and MIME. The host that holds the handle overwrites
// Size and SHA256 with its own record, so the add-on cannot choose them.
type ConnectorReceived struct {
	Name   string `json:"name"`
	MIME   string `json:"mime,omitempty"`
	Size   int64  `json:"size,omitempty"`
	SHA256 string `json:"sha256,omitempty"`
}
