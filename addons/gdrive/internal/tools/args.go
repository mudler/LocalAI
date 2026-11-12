// SPDX-License-Identifier: MIT
package tools

import (
	"regexp"

	"github.com/mudler/LocalAI/addons/internal/argutil"
)

// bad is a refusal of the request with a short, fixed reason. The reason
// reaches the model, so it never holds text from the service or from the
// token.
var bad = argutil.Bad

var (
	// idShape is the shape of a file or folder ID, and of the word "root".
	idShape = regexp.MustCompile(`^[A-Za-z0-9_-]{1,128}$`)
	// mimeShape is the shape of a media type used in a search.
	mimeShape = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9!#$&^_.+-]{0,126}/[A-Za-z0-9][A-Za-z0-9!#$&^_.+-]{0,126}$`)
)

func checkID(what, id string) error {
	if !idShape.MatchString(id) {
		return bad("%s is not a file ID", what)
	}
	return nil
}
