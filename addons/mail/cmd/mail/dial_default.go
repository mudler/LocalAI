// SPDX-License-Identifier: MIT

//go:build !mailtest

package main

import "github.com/mudler/LocalAI/addons/mail/internal/dial"

// dialOptions is the production policy: mail ports only, public addresses
// only, the system roots. The mailtest build tag swaps this file for one that
// lets an end-to-end test reach servers on loopback; a release is never built
// with that tag.
func dialOptions() dial.Options { return dial.Options{} }
