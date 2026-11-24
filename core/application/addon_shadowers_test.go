// SPDX-License-Identifier: MIT
package application

import "github.com/mudler/LocalAI/core/services/studio"

// The chats service holds the late broker, not the broker itself. It must
// forward the list of add-ons that replace a built-in tool, or the chats see
// no stopped add-on that replaces bash and an administrator cannot pick one.
var (
	_ studio.ShadowLister = lateStudioAddons{}
	_ studio.ShadowLister = (*studioAddons)(nil)
)
