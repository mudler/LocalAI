// SPDX-License-Identifier: MIT
package contract

import (
	"fmt"
	"regexp"
)

// MinFilesAPI is the first manifest API version that knows the files ability
// of a connector kind. A manifest that declares one must accept it.
const MinFilesAPI uint64 = 2

// Limits of the files ability.
const (
	// MaxDeclaredFileBytes is the largest limit a kind may declare: 1 TiB.
	// The host ceiling and the owner's settings still apply.
	MaxDeclaredFileBytes int64 = 1 << 40
	// MaxFileTypes bounds the media types of one direction.
	MaxFileTypes = 32
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

// ConnectorFiles is the manifest field files of a connector kind. A kind that
// declares none keeps the inline path, capped by MaxConnectorFileBytes.
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

func (f *ConnectorFiles) validate(api VersionRange) error {
	if f == nil {
		return nil
	}
	if api.Max < MinFilesAPI {
		return fmt.Errorf("a connector kind that declares files needs api %d", MinFilesAPI)
	}
	if f.Receive == nil && f.Send == nil {
		return fmt.Errorf("files must declare receive, send or both")
	}
	if err := f.Receive.validate("receive"); err != nil {
		return err
	}
	return f.Send.validate("send")
}
