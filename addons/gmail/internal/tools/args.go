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
	// idShape is the shape of the ID of a message, a thread, a draft or an
	// attachment.
	idShape = regexp.MustCompile(`^[A-Za-z0-9_-]{1,256}$`)
)

func checkID(what, id string) error {
	if !idShape.MatchString(id) {
		return bad("%s is not an ID", what)
	}
	return nil
}
